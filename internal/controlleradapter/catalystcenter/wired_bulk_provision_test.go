// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type wiredBulkFixture struct {
	present                   map[string]string
	serialB                   string
	calls                     int
	inventoryReads, siteReads int
	payload                   []wiredProvisionedDevice
	partial, lost             bool
	taskMode                  string
	taskEnd                   time.Time
	method                    string
	afterMutation             func()
	beforeInventory           func()
	networkReply              func(http.ResponseWriter, *http.Request) bool
}

func newWiredBulkFixture(t *testing.T) (*client, []wiredBulkMember, *wiredBulkFixture) {
	t.Helper()
	f := &wiredBulkFixture{present: map[string]string{}, serialB: "SERIAL-B", taskEnd: time.Now().UTC().Truncate(time.Second)}
	members := []wiredBulkMember{{desired: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "b"}, site: "Global/Lab/Building"}, state: "PROVISION"}, {desired: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "a"}, site: "Global/Lab/Building"}, state: "PROVISION"}}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if f.networkReply != nil && f.networkReply(w, r) {
			return
		}
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == wiredProvisionPath && r.Method != "GET" {
			f.calls++
			f.taskEnd = time.Now().UTC().Truncate(time.Second)
			f.method = r.Method
			if err := json.NewDecoder(r.Body).Decode(&f.payload); err != nil {
				t.Error(err)
			}
			for i, row := range f.payload {
				if !f.partial || i == 0 {
					if row.ID != "" {
						f.present[row.DeviceID] = row.ID
					} else {
						f.present[row.DeviceID] = "record-" + row.DeviceID
					}
				}
			}
			if f.afterMutation != nil {
				f.afterMutation()
			}
			if f.lost {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"batch-task","url":"https://untrusted.invalid"}}`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected native mutation")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == taskPath+"batch-task/tree" {
			now := f.taskEnd.UnixMilli()
			end := any(now)
			if f.taskMode == "running" {
				end = nil
			}
			rows := []any{map[string]any{"id": "batch-task", "isError": false, "startTime": now, "endTime": now}, map[string]any{"id": "child", "parentId": "batch-task", "rootId": "batch-task", "isError": f.taskMode == "failed", "errorCode": "FAILED", "startTime": now, "endTime": end}}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
			return
		}
		var rows any
		switch r.URL.Path {
		case devicesPath:
			if f.beforeInventory != nil {
				f.beforeInventory()
			}
			f.inventoryReads++
			rows = []Device{{ID: "a", Hostname: "a", Serial: "SERIAL-A", Family: "Switches and Hubs", Reachability: "Reachable", ManagementIP: "198.51.100.100"}, {ID: "b", Hostname: "b", Serial: f.serialB, Family: "Switches and Hubs", Reachability: "Reachable", ManagementIP: "198.51.100.101"}}
		case sitesPath:
			f.siteReads++
			rows = sampleSites()
		case assignedDevicesPath:
			rows = []map[string]string{{"deviceId": "a", "siteId": "building"}, {"deviceId": "b", "siteId": "building"}}
		case wiredProvisionPath:
			list := []wiredProvisionedDevice{}
			device := r.URL.Query().Get("networkDeviceId")
			if id := f.present[device]; id != "" {
				list = append(list, wiredProvisionedDevice{ID: id, DeviceID: device, SiteID: "building"})
			}
			rows = list
		default:
			t.Errorf("unexpected native path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	return c, members, f
}

func TestWiredBulkNativeProvisionRecovery(t *testing.T) {
	for _, mode := range []string{"create", "reprovision", "noop", "existing-member", "denied", "late-serial", "late-record", "partial", "lost-receipt", "failed-child", "running-child", "replaced-existing", "replaced-reprovision", "missing-guard", "mixed", "duplicate", "outside-root", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, members, f := newWiredBulkFixture(t)
			if mode == "reprovision" || mode == "replaced-reprovision" {
				for i := range members {
					members[i].state = "REPROVISION"
				}
				f.present["a"], f.present["b"] = "old-a", "old-b"
			}
			if mode == "noop" {
				f.present["a"], f.present["b"] = "old-a", "old-b"
			}
			if mode == "existing-member" || mode == "replaced-existing" {
				f.present["a"] = "old-a"
			}
			if mode == "partial" {
				f.partial = true
			}
			if mode == "lost-receipt" {
				f.lost = true
			}
			if mode == "failed-child" {
				f.taskMode = "failed"
			}
			if mode == "running-child" {
				f.taskMode = "running"
			}
			switch mode {
			case "mixed":
				members[0].state = "REPROVISION"
				f.present["b"] = "old-b"
			case "duplicate":
				members[0] = members[1]
			case "outside-root":
				members[0].desired.site = "Global/Lab2/Building"
			case "oversized":
				for len(members) <= wiredBulkProvisionLimit {
					members = append(members, members[0])
				}
			}
			guards := 0
			guard := wiredBulkGuard(func(_ context.Context, targets []Device) error {
				guards++
				if len(targets) != 2 || targets[0].ID != "a" || targets[1].ID != "b" {
					t.Error("guard received noncanonical footprint")
				}
				switch mode {
				case "denied":
					return errors.New("denied")
				case "late-serial":
					f.serialB = "replacement"
				case "late-record":
					f.present["b"] = "external"
				}
				return nil
			})
			if mode == "missing-guard" {
				guard = nil
			}
			write, err := c.planWiredBulkProvision(ctx, "Global/Lab", members, guard)
			if mode == "missing-guard" || mode == "mixed" || mode == "duplicate" || mode == "outside-root" || mode == "oversized" {
				if err == nil || write != nil || f.calls != 0 {
					t.Fatal("invalid batch planned")
				}
				return
			}
			if mode == "noop" {
				if err != nil || write != nil || guards != 0 {
					t.Fatal("converged batch acquired authority", err)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if !isReservedConfigurationGroupResource("inventory", write.resource) || !validConfigurationGroupAction("inventory", write.resource, write.action) {
				t.Fatal("bulk escaped group prefix reservation")
			}
			// Input order cannot change native payload ordering or retained batch identity.
			reversed := []wiredBulkMember{members[1], members[0]}
			second, err := c.planWiredBulkProvision(ctx, "Global/Lab", reversed, guard)
			if err != nil || second.resource != write.resource {
				t.Fatal("reordering changed batch identity", err)
			}
			submitted := metav1.NewTime(time.Now().Add(-time.Second))
			task, err := write.submit(ctx)
			if mode == "denied" || mode == "late-serial" || mode == "late-record" {
				if err == nil || f.calls != 0 {
					t.Fatal("unsafe batch dispatched")
				}
				if _, err := write.submit(ctx); err == nil {
					t.Fatal("refused closure replayed")
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || f.calls != 1 {
					t.Fatal("lost receipt accepted")
				}
				if _, err := write.submit(ctx); err == nil || f.calls != 1 {
					t.Fatal("ambiguous batch replayed")
				}
				return
			}
			if err != nil || task.ID != "batch-task" || f.calls != 1 || guards != 1 {
				t.Fatal("batch not submitted exactly once", err)
			}
			if _, err := write.submit(ctx); err == nil || f.calls != 1 {
				t.Fatal("accepted closure replayed")
			}
			if len(f.payload) == 0 || f.payload[0].DeviceID != (map[bool]string{true: "b", false: "a"}[mode == "existing-member" || mode == "replaced-existing"]) {
				t.Fatal("payload membership/order wrong")
			}
			op := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent, Phase: "Verifying", TaskID: task.ID, SubmittedAt: &submitted}
			if mode == "replaced-existing" {
				f.present["a"] = "replacement"
			}
			if mode == "replaced-reprovision" {
				f.present["a"] = "replacement"
			}
			done, err := c.verifyWiredBulkProvision(ctx, "Global/Lab", members, op, time.Now())
			if mode == "failed-child" || mode == "replaced-existing" || mode == "replaced-reprovision" {
				if err == nil || done {
					t.Fatal("unsafe completion accepted")
				}
				return
			}
			if mode == "partial" || mode == "running-child" {
				if err != nil || done {
					t.Fatal("partial batch settled", err)
				}
				if mode == "partial" {
					f.present["b"] = "record-b"
					done, err = c.verifyWiredBulkProvision(ctx, "Global/Lab", members, op, time.Now())
					if err != nil || !done {
						t.Fatal("original receipt failed after partial convergence", err)
					}
				}
				return
			}
			if err != nil || !done {
				t.Fatal("complete batch not independently verified", err)
			}
		})
	}
}

func TestWiredBulkResourceRejectsMalformedMasks(t *testing.T) {
	valid := wiredBulkProvisionPrefix + "v1:create:2:003:" + strings.Repeat("a", 64)
	partialUpdate := strings.Replace(strings.Replace(valid, ":create:", ":update:", 1), ":003:", ":001:", 1)
	if _, _, _, err := parseWiredBulkResource(partialUpdate, "Update"); err == nil {
		t.Fatal("reprovision receipt dropped a declared member")
	}
	for _, value := range []string{strings.Replace(valid, ":003:", ":000:", 1), strings.Replace(valid, ":003:", ":004:", 1), strings.Replace(valid, ":2:", ":02:", 1), strings.Replace(valid, ":003:", ":3:", 1), strings.ToUpper(valid), strings.Replace(valid, "v1:", "v3:", 1), valid + ":extra"} {
		if _, _, _, err := parseWiredBulkResource(value, "Create"); err == nil {
			t.Fatal("malformed batch resource accepted")
		}
		if !isReservedConfigurationGroupResource("inventory", value) && strings.HasPrefix(value, wiredBulkProvisionPrefix) {
			t.Fatal("malformed group escaped prefix fence")
		}
	}
}

func TestWiredBulkObservationUsesOneFreshCatalog(t *testing.T) {
	c, members, f := newWiredBulkFixture(t)
	if _, err := c.wiredBulkSnapshot(context.Background(), "Global/Lab", members); err != nil {
		t.Fatal(err)
	}
	if f.inventoryReads != 1 || f.siteReads != 1 {
		t.Fatal("batch repeats inventory/site catalog per member", f.inventoryReads, f.siteReads)
	}
	if _, err := c.wiredBulkSnapshot(context.Background(), "Global/Lab", members); err != nil {
		t.Fatal(err)
	}
	if f.inventoryReads != 2 || f.siteReads != 2 {
		t.Fatal("catalog reused across fresh observations")
	}
}
