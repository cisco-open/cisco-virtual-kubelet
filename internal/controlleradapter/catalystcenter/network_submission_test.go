// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type networkSubmissionFixture struct {
	mu                   sync.Mutex
	sites                []Site
	data                 map[string]map[string]any
	writes, siteReads    int
	stamp                int64
	taskState            string
	treeReads, taskReads int
	afterTree            func()
	failRead, lostAck    bool
	changeAt             int
	change               func()
	body                 map[string]any
}

func networkSubmissionFixtureExecutor(t *testing.T, kind string) (*networkConfigurationExecutor, *networkSubmissionFixture) {
	t.Helper()
	bindings, err := parseNetworkSettings(allNetworkExecutionModel())
	if err != nil {
		t.Fatal(err)
	}
	var selected, peer networkSetting
	for _, s := range bindings[0].settings {
		if s.kind == kind {
			selected = s
		}
		if s.kind == "banner" {
			peer = s
		}
	}
	if kind == "banner" {
		for _, s := range bindings[0].settings {
			if s.kind == "timezone" {
				peer = s
			}
		}
	}
	f := &networkSubmissionFixture{sites: sampleSites(), data: map[string]map[string]any{}}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		taskID := "task"
		if f.writes > 1 {
			taskID = fmt.Sprintf("task-%d", f.writes)
		}
		if r.Method == "GET" && r.URL.Path == taskPath+taskID+"/tree" {
			f.treeReads++
			root := map[string]any{"id": taskID, "rootId": taskID, "startTime": f.stamp, "endTime": f.stamp, "isError": false}
			child := map[string]any{"id": "child", "parentId": taskID, "rootId": taskID, "startTime": f.stamp, "endTime": f.stamp, "isError": false}
			switch f.taskState {
			case "child-running":
				delete(child, "endTime")
			case "child-failed":
				child["isError"] = true
				child["errorCode"] = "NCSP99999"
			case "disconnected":
				child["parentId"] = "foreign"
			case "foreign-tree":
				root["id"] = "foreign"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []any{root, child}})
			if f.afterTree != nil {
				callback := f.afterTree
				f.afterTree = nil
				callback()
			}
			return
		}
		if r.Method == "GET" && r.URL.Path == taskPath+taskID {
			f.taskReads++
			root := map[string]any{"id": taskID, "startTime": f.stamp, "endTime": f.stamp, "isError": false}
			switch f.taskState {
			case "root-running":
				delete(root, "endTime")
			case "root-failed":
				root["isError"] = true
				root["errorCode"] = "NCSP99999"
			case "foreign-root":
				root["id"] = "foreign"
			case "stale-root":
				root["startTime"] = f.stamp - 24*60*60*1000
				root["endTime"] = f.stamp - 24*60*60*1000
			}
			json.NewEncoder(w).Encode(map[string]any{"response": root})
			return
		}
		if r.Method == "GET" && r.URL.Path == sitesPath {
			f.siteReads++
			if f.changeAt == f.siteReads && f.change != nil {
				f.change()
			}
			json.NewEncoder(w).Encode(map[string]any{"response": f.sites})
			return
		}
		path := strings.TrimPrefix(r.URL.Path, sitesPath+"/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 {
			t.Error("unexpected endpoint")
			w.WriteHeader(400)
			return
		}
		apiKind := strings.TrimSuffix(parts[1], "Settings")
		key := parts[0] + "/" + apiKind
		if r.Method == "GET" {
			if f.failRead {
				w.WriteHeader(503)
				return
			}
			body := f.data[key]
			if body == nil {
				body = map[string]any{apiKind: nil}
				if apiKind == "telemetry" {
					body = map[string]any{}
					for _, g := range telemetryGroups {
						body[g] = nil
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": body})
			return
		}
		if r.Method != "PUT" {
			t.Error("unexpected mutation")
			w.WriteHeader(400)
			return
		}
		f.writes++
		if err := json.NewDecoder(r.Body).Decode(&f.body); err != nil {
			t.Error(err)
		}
		if apiKind == "telemetry" {
			merged := map[string]any{}
			for _, group := range telemetryGroups {
				merged[group] = nil
			}
			for group, value := range f.data[key] {
				merged[group] = value
			}
			for group, value := range f.body {
				merged[group] = value
			}
			f.data[key] = merged
		} else {
			f.data[key] = f.body
		}
		// The actual API normalizes an accepted empty server reset to an empty
		// group; request null and response null are different contracts.
		if apiKind == "ntp" || apiKind == "dhcp" {
			fields, _ := f.body[apiKind].(map[string]any)
			if value, present := fields["servers"]; present && value == nil {
				f.data[key] = map[string]any{apiKind: map[string]any{}}
			}
		}
		f.stamp = time.Now().UnixMilli()
		if f.lostAck {
			fmt.Fprint(w, `{}`)
			return
		}
		submittedTask := "task"
		if f.writes > 1 {
			submittedTask = fmt.Sprintf("task-%d", f.writes)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": submittedTask}})
	})
	return &networkConfigurationExecutor{client: c, bindings: []networkBinding{{path: "Global/Lab", kind: "area", settings: []networkSetting{selected, peer}}}}, f
}
func networkFixturePayload(t *testing.T, s networkSetting) map[string]any {
	t.Helper()
	raw, err := networkSettingPayload(s)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("payload")
	}
	return body
}
func networkFixtureAPIKind(kind string) string {
	if kind == "timezone" {
		return "timeZone"
	}
	return kind
}

func TestNetworkSubmissionFrozenCompleteSelection(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"unchanged", "late-local", "late-peer", "inherited", "explicit-provenance", "ancestor", "second-read", "read-error", "unrelated", "caller-mutated", "lost-ack", "concurrent"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				e, f := networkSubmissionFixtureExecutor(t, kind)
				wanted := e.bindings[0].settings[0]
				original := networkFixturePayload(t, wanted)
				if mode == "explicit-provenance" {
					alternate := wanted
					alternate.servers = []string{"192.0.2.254"}
					alternate.domain = "other.test"
					alternate.timezone = "Etc/UTC"
					alternate.banner = "Other"
					alternate.telemetry = cloneNetworkTelemetry(wanted.telemetry)
					if kind == "telemetry" {
						alternate.telemetry["wiredDataCollection"]["enableWiredDataCollection"] = !wanted.telemetry["wiredDataCollection"]["enableWiredDataCollection"].(bool)
					}
					f.data["root/"+networkFixtureAPIKind(kind)] = networkFixturePayload(t, alternate)
				}
				write, err := e.next(ctx)
				if err != nil || write == nil {
					t.Fatal("planning", err)
				}
				f.mu.Lock()
				switch mode {
				case "late-local":
					f.data["area/"+networkFixtureAPIKind(kind)] = original
				case "late-peer":
					s := e.bindings[0].settings[1]
					f.data["area/"+networkFixtureAPIKind(s.kind)] = networkFixturePayload(t, s)
				case "inherited":
					f.data["root/"+networkFixtureAPIKind(kind)] = original
				case "explicit-provenance":
					f.data["area/"+networkFixtureAPIKind(kind)] = f.data["root/"+networkFixtureAPIKind(kind)]
				case "ancestor":
					f.sites[0].ID = "new-root"
					f.sites[1].ParentID = "new-root"
				case "second-read":
					f.changeAt = 3
					f.change = func() { f.data["area/"+networkFixtureAPIKind(kind)] = original }
				case "read-error":
					f.failRead = true
				case "unrelated":
					f.sites = append(f.sites, Site{ID: "other", Name: "Other", NameHierarchy: "Global/Other", ParentID: "root", Type: "area"})
					f.data["other/banner"] = map[string]any{"banner": map[string]any{"type": "Custom", "message": "Unrelated"}}
				case "caller-mutated":
					e.bindings[0].path = "Global/Other"
					e.bindings[0].settings[0].servers = []string{"192.0.2.99"}
					e.bindings[0].settings[0].banner = "changed"
					if kind == "telemetry" {
						e.bindings[0].settings[0].telemetry["wiredDataCollection"]["enableWiredDataCollection"] = false
						e.bindings[0].settings[0].telemetry["syslogs"]["externalSyslogServers"].([]string)[0] = "192.0.2.99"
					}
				case "lost-ack":
					f.lostAck = true
				}
				f.mu.Unlock()
				success := mode == "unchanged" || mode == "unrelated" || mode == "caller-mutated" || mode == "concurrent"
				if mode == "concurrent" {
					var wg sync.WaitGroup
					results := make(chan error, 2)
					for range 2 {
						wg.Add(1)
						go func() { defer wg.Done(); _, err := write.submit(ctx); results <- err }()
					}
					wg.Wait()
					close(results)
					accepted := 0
					for err := range results {
						if err == nil {
							accepted++
						} else if !errors.Is(err, errConfigurationTransition) {
							t.Fatal(err)
						}
					}
					if accepted != 1 {
						t.Fatal("concurrent reuse was accepted", accepted)
					}
				} else {
					_, err = write.submit(ctx)
					if (err == nil) != success {
						t.Fatal("wrong submit outcome", err)
					}
					if !success && mode != "lost-ack" && write.noDispatch(err) == nil {
						t.Fatal("preflight refusal lacks original guard proof")
					}
				}
				if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
					t.Fatal("write reused", err)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				expectedWrites := 0
				if success || mode == "lost-ack" {
					expectedWrites = 1
				}
				if f.writes != expectedWrites {
					t.Fatal("wrong native mutation count", f.writes)
				}
				if expectedWrites == 1 && !reflect.DeepEqual(f.body, original) {
					t.Fatal("caller changed frozen payload")
				}
			})
		}
	}
}
