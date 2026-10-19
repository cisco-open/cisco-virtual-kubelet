// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type wiredExecutorFixture struct {
	nativeID   string
	present    bool
	calls      int
	duringRead func()
}

func newWiredExecutorFixture(t *testing.T) (*wiredProvisionExecutor, *wiredExecutorFixture) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Source.Inline.Raw = []byte(fabricEdgeIntentModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	intents, err := parseFabricDeviceIntents([]byte(fabricEdgeIntentModel))
	if err != nil {
		t.Fatal(err)
	}
	f := &wiredExecutorFixture{nativeID: "provisioned", present: true}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			f.calls++
			t.Errorf("unexpected mutation %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if f.duringRead != nil {
			callback := f.duringRead
			f.duringRead = nil
			callback()
		}
		if r.URL.Path == taskPath+"task" {
			now := time.Now().UnixMilli()
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task", "isError": false, "startTime": now, "endTime": now}})
			return
		}
		var rows any
		switch r.URL.Path {
		case devicesPath:
			rows = []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}}
		case sitesPath:
			rows = sampleSites()
		case assignedDevicesPath:
			rows = []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
		case wiredProvisionPath:
			provisioned := []wiredProvisionedDevice{}
			if f.present {
				provisioned = append(provisioned, wiredProvisionedDevice{ID: f.nativeID, DeviceID: "edge-id", SiteID: "building"})
			}
			rows = provisioned
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	return &wiredProvisionExecutor{adapter: a, config: config, desired: intents[0].inventory, state: "REPROVISION"}, f
}
func TestWiredProvisionExecutorPrepared(t *testing.T) {
	for _, mode := range []string{"same", "replaced-native", "accepted", "source", "parent", "action", "now-existing", "late-source", "late-journal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			e, f := newWiredExecutorFixture(t)
			if mode == "now-existing" {
				e.state = "PROVISION"
				f.present = false
			}
			first, err := e.next(ctx)
			if err != nil || first == nil {
				t.Fatal(first, err)
			}
			op, err := e.adapter.prepareConfiguration(ctx, e.config, nil, "inventory", first.resource, first.action, first.identity)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := e.adapter.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "replaced-native":
				f.nativeID = "replacement"
			case "now-existing":
				f.present = true
			case "accepted":
				nc.Status.Configuration.Operation.Phase = "Accepted"
			case "source":
				nc.Status.Configuration.Operation.SourceVersion = "other"
			case "parent":
				nc.Status.Configuration.Operation.ParentID = "other"
			case "action":
				nc.Status.Configuration.Operation.Action = "Create"
			case "late-source":
				f.duringRead = func() {
					changed := e.config.DeepCopy()
					changed.Generation++
					if err := e.adapter.statusWriter.Update(ctx, changed); err != nil {
						t.Error(err)
					}
				}
			case "late-journal":
				f.duringRead = func() {
					current, err := e.adapter.currentController(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					current.Status.Configuration.Operation.Phase = "Unknown"
					if err := e.adapter.statusWriter.Status().Update(ctx, current); err != nil {
						t.Error(err)
					}
				}
			}
			if err := e.adapter.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			restarted := *e
			next, err := restarted.next(ctx)
			if mode == "same" {
				if err != nil || next == nil || next.resource != op.Resource {
					t.Fatal(next, err)
				}
			} else if err == nil || next != nil {
				t.Fatal("invalid prepared evidence replaced", next, err)
			}
			if f.calls != 0 {
				t.Fatal("planning submitted mutation")
			}
		})
	}
}
func TestWiredProvisionExecutorVerification(t *testing.T) {
	for _, mode := range []string{"verified", "foreign-operation", "wrong-revision", "replaced-native", "late-source", "late-journal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			e, f := newWiredExecutorFixture(t)
			first, err := e.next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op, err := e.adapter.prepareConfiguration(ctx, e.config, nil, "inventory", first.resource, first.action, first.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.adapter.startConfiguration(ctx, e.config, nil, op.ID, first.identity); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{"Accepted", "Verifying"} {
				task := ""
				if phase == "Accepted" {
					task = "task"
				}
				if err := e.adapter.advanceConfiguration(ctx, op.ID, phase, task); err != nil {
					t.Fatal(err)
				}
			}
			current := journalState(t, e.adapter).Operation.DeepCopy()
			switch mode {
			case "foreign-operation":
				current.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			case "wrong-revision":
				current.ConfigGeneration++
			case "replaced-native":
				f.nativeID = "replacement"
			case "late-source":
				f.duringRead = func() {
					changed := e.config.DeepCopy()
					changed.Generation++
					if err := e.adapter.statusWriter.Update(ctx, changed); err != nil {
						t.Error(err)
					}
				}
			case "late-journal":
				f.duringRead = func() {
					nc, err := e.adapter.currentController(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					nc.Status.Configuration.Operation.TaskID = "other-task"
					if err := e.adapter.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				}
			}
			restarted := *e
			verified, err := restarted.verify(ctx, current)
			if mode == "verified" {
				if err != nil || !verified {
					t.Fatal(verified, err)
				}
			} else if verified || err == nil {
				t.Fatal("changed operation verified", verified, err)
			}
			if f.calls != 0 {
				t.Fatal("verification replayed mutation")
			}
			if len(journalState(t, e.adapter).ActionApplications) != 0 {
				t.Fatal("semantic verifier fabricated retained receipt")
			}
		})
	}
}
