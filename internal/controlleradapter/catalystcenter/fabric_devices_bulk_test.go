// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
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
)

type fabricDevicesBulkFixture struct {
	inventory           []Device
	created             map[string]fabricDeviceConfiguration
	parents             map[string]fabricDeviceConfiguration
	posts, payloadCount int
	partial, lost       bool
	afterMutation       func()
	taskMode            string
	taskEnd             time.Time
}

func newFabricDevicesBulkFixture(t *testing.T, mode string) (*client, []fabricDeviceIntent, *fabricDevicesBulkFixture) {
	t.Helper()
	f := &fabricDevicesBulkFixture{partial: mode == "partial", lost: mode == "lost"}
	rows := []any{}
	for i, serial := range []string{"FOC12345678", "FOC12345679"} {
		row := map[string]any{"name": fmt.Sprintf("edge%d", i), "serial_number": serial, "site": "Global/Lab/Building", "state": "PROVISION", "fabric_site": "Global/Lab", "fabric_roles": []string{"EDGE_NODE"}}
		if mode == "zone" {
			row["fabric_zone"] = "Global/Lab/Building"
		}
		rows = append(rows, row)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": rows}}})
	wants, err := parseFabricDeviceIntents(raw)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "duplicate" {
		wants[1] = wants[0]
	}
	f.inventory = []Device{{ID: "edge-0", Hostname: "edge0", Serial: "FOC12345678"}, {ID: "edge-1", Hostname: "edge1", Serial: "FOC12345679"}, {ID: "peer", Hostname: "peer", Serial: "FOC12345680"}}
	if mode == "alias" {
		for i := range wants {
			wants[i].inventory.reference.Serial = ""
		}
		f.inventory[1].Serial = f.inventory[0].Serial
	}
	f.created = map[string]fabricDeviceConfiguration{}
	if mode == "deferred-parent" {
		f.parents = map[string]fabricDeviceConfiguration{}
	}

	flag := false
	if mode == "existing" || mode == "noop" || mode == "existing-replaced" {
		f.created["edge-0"] = fabricDeviceConfiguration{ID: "existing-0", NetworkDeviceID: "edge-0", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}
	}
	if mode == "noop" {
		f.created["edge-1"] = fabricDeviceConfiguration{ID: "existing-1", NetworkDeviceID: "edge-1", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == taskPath+"task/tree" {
			now := time.Now().UTC().Truncate(time.Second).UnixMilli()
			if !f.taskEnd.IsZero() {
				now = f.taskEnd.UnixMilli()
			}
			var end any = now
			if f.taskMode == "running" {
				end = nil
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "task", "isError": false, "startTime": now, "endTime": now}, {"id": "child", "parentId": "task", "rootId": "task", "isError": f.taskMode == "failed", "errorCode": "FAILED", "startTime": now, "endTime": end}}})
			return
		}
		reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case devicesPath:
			reply(f.inventory)
		case sitesPath:
			reply(sampleSites())
		case fabricSitesPath:
			reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}})
		case fabricZonesPath:
			if mode == "missing-zone" {
				reply([]fabricZone{})
				return
			}
			reply([]fabricZone{{ID: "zone", SiteID: "building", Authentication: "Closed Authentication"}})
		case assignedDevicesPath:
			reply([]map[string]string{{"deviceId": "edge-0", "siteId": "building"}, {"deviceId": "edge-1", "siteId": "building"}})
		case wiredProvisionPath:
			id := r.URL.Query().Get("networkDeviceId")
			reply([]wiredProvisionedDevice{{ID: "provisioned-" + id, DeviceID: id, SiteID: "building"}})
		case fabricDevicesPath:
			if r.Method == "GET" {
				fabric := r.URL.Query().Get("fabricId")
				members := []fabricDeviceConfiguration{{ID: "peer-row", NetworkDeviceID: "peer", FabricID: fabric, Roles: []string{"EDGE_NODE"}}}
				if mode == "zone" && fabric == "fabric" {
					for _, id := range []string{"edge-0", "edge-1"} {
						members = append(members, fabricDeviceConfiguration{ID: "parent-" + id, NetworkDeviceID: id, FabricID: fabric, Roles: []string{"EDGE_NODE"}})
					}
				} else if f.parents != nil && fabric == "fabric" {
					for _, row := range f.parents {
						members = append(members, row)
					}
				} else {
					for _, row := range f.created {
						members = append(members, row)
					}
				}
				reply(members)
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected mutation")
				w.WriteHeader(500)
				return
			}
			f.posts++
			var body []fabricDeviceConfiguration
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("bad native payload")
			}
			f.payloadCount = len(body)
			for i, row := range body {
				if !f.partial || i == 0 {
					row.ID = "created-" + row.NetworkDeviceID
					if f.parents != nil && row.FabricID == "fabric" {
						f.parents[row.NetworkDeviceID] = row
					} else {
						f.created[row.NetworkDeviceID] = row
					}
				}
			}
			if f.afterMutation != nil {
				f.afterMutation()
			}
			if f.lost {
				reply(map[string]any{})
				return
			}
			reply(map[string]any{"taskId": "task"})
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	return c, wants, f
}

func TestFabricDevicesBulkPhysicalFootprintAndRecovery(t *testing.T) {
	for _, mode := range []string{"create", "legacy", "zone", "existing", "noop", "partial", "lost", "denied", "missing-guard", "duplicate", "alias", "late-peer", "guard-target", "recovery-peer", "existing-replaced", "wrong-role", "caller-change"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, wants, f := newFabricDevicesBulkFixture(t, mode)
			guardCalls := 0
			guard := func(_ context.Context, physical []Device) error {
				guardCalls++
				if len(physical) != 3 {
					t.Fatal("incoming members or existing peers missing from footprint", len(physical))
				}
				if mode == "denied" {
					return errors.New("denied")
				}
				if mode == "guard-target" {
					f.inventory[1].Serial = "FOC99999999"
				}
				return nil
			}
			if mode == "missing-guard" {
				guard = nil
			}
			write, err := c.planFabricDevicesBulk(ctx, wants, guard)
			if mode == "duplicate" || mode == "alias" || mode == "missing-guard" {
				if err == nil || write != nil || f.posts != 0 {
					t.Fatal("invalid physical batch planned", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "noop" {
				if write != nil || guardCalls != 0 || f.posts != 0 {
					t.Fatal("matching membership replayed")
				}
				return
			}
			if write == nil {
				t.Fatal("missing batch")
			}
			if mode == "late-peer" {
				f.inventory[2].Serial = "FOC99999999"
			}
			if mode == "caller-change" {
				wants[0].roles[0] = "CONTROL_PLANE_NODE"
			}
			task, err := write.submit(ctx)
			if mode == "denied" || mode == "late-peer" || mode == "guard-target" || mode == "lost" {
				expected := 0
				if mode == "lost" {
					expected = 1
				}
				if err == nil || f.posts != expected {
					t.Fatal("unsafe or ambiguous acceptance", err, f.posts)
				}
				if _, err := write.submit(ctx); err == nil || f.posts != expected {
					t.Fatal("refused/uncertain request replayed")
				}
				return
			}
			if err != nil || task.ID != "task" || f.posts != 1 {
				t.Fatal("native batch", err)
			}
			expected := 2
			if mode == "existing" || mode == "existing-replaced" {
				expected = 1
			}
			if f.payloadCount != expected {
				t.Fatal("matching member included in native request")
			}
			if mode == "caller-change" {
				wants[0].roles[0] = "EDGE_NODE"
				if f.created["edge-0"].Roles[0] != "EDGE_NODE" {
					t.Fatal("caller changed captured roles")
				}
			}
			if mode == "recovery-peer" {
				f.inventory[2].Serial = "FOC99999999"
			}
			if mode == "existing-replaced" {
				row := f.created["edge-0"]
				row.ID = "replaced"
				f.created["edge-0"] = row
			}
			if mode == "wrong-role" {
				row := f.created["edge-0"]
				row.Roles = []string{"CONTROL_PLANE_NODE"}
				f.created["edge-0"] = row
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", TaskID: task.ID, Action: "Create", TargetID: write.identity.target, ParentID: write.identity.parent, Resource: write.resource}
			if mode == "legacy" {
				parts := strings.Split(strings.TrimPrefix(write.resource, fabricDevicesBulkIDsPrefix), ":")
				op.Resource = fabricDevicesBulkPrefix + strings.Join(parts[:3], ":")
			}
			done, err := c.verifyFabricDevicesBulk(ctx, wants, op)
			valid := mode == "create" || mode == "legacy" || mode == "zone" || mode == "existing" || mode == "caller-change"
			if done != valid || (valid || mode == "partial") && err != nil {
				t.Fatal("original physical batch recovery", done, err)
			}
			if _, err := write.submit(ctx); err == nil || f.posts != 1 {
				t.Fatal("successful batch replayed")
			}
		})
	}
}

func TestFabricDevicesBulkCannotUseLogicalAdmission(t *testing.T) {
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"fabric"}
	resource := fabricDevicesBulkPrefix + "2:003:" + strings.Repeat("a", 64)
	if !validConfigurationGroupAction("fabric", resource, "Create") || validConfigurationGroupAction("fabric", resource, "Update") {
		t.Fatal("bulk create action classification")
	}
	if _, err := a.prepareConfiguration(context.Background(), config, nil, "fabric", resource, "Create", configurationResourceIdentity{target: "fabric", parent: "fabric"}); err == nil {
		t.Fatal("physical bulk entered ordinary logical lifecycle")
	}
	for _, bad := range []string{fabricDevicesBulkPrefix + "pruned", fabricDevicesBulkFamily + "v2:pruned", fabricDevicesBulkPrefix + "2:fff:" + strings.Repeat("a", 64), fabricDevicesBulkPrefix + "02:003:" + strings.Repeat("a", 64), fabricDevicesBulkPrefix + "2:003:bad"} {
		if !isReservedConfigurationGroupResource("fabric", bad) || validConfigurationGroupAction("fabric", bad, "Create") {
			t.Fatal("malformed bulk escaped reserved group classification")
		}
	}
}
