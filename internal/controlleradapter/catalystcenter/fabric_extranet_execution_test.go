// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExtranetNativeCreateRecovery(t *testing.T) {
	for _, mode := range []string{"create", "zone", "remote-vn", "receipt-native-replacement", "existing", "conflict", "unscoped", "missing-membership", "missing-device", "aliased-serial", "late-vn", "late-member", "late-device", "late-policy", "late-peer-policy", "recovery-peer-policy", "lost-receipt", "running-task", "failed-task", "stale-task", "unknown", "missing-task", "changed-readback", "changed-recovery-member", "missing-readback", "caller-change"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			submitted := metav1.NewTime(now.Add(-time.Minute))
			late, recovery, created := false, false, false
			writes := 0
			receiptReads := 0
			buildingReceipt := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" {
					if r.URL.Path != fabricExtranetPath {
						t.Errorf("unexpected POST %s", r.URL.Path)
						w.WriteHeader(500)
						return
					}
					writes++
					var body []fabricExtranetPolicy
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
						t.Error("invalid payload")
						w.WriteHeader(500)
						return
					}
					fid := "fabric"
					if mode == "zone" {
						fid = "zone"
					}
					if body[0].Name != "Shared" || body[0].Provider != "Services" || len(body[0].Subscribers) != 2 || body[0].Subscribers[0] != "Corp" || len(body[0].Fabrics) != 1 || body[0].Fabrics[0] != fid || body[0].ID != "" {
						t.Errorf("wrong native intent: %+v", body)
					}
					created = true
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Errorf("unexpected mutation %s", r.Method)
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case taskPath + "task":
					start := submitted.Time.UnixMilli()
					task := map[string]any{"id": "task", "startTime": start, "endTime": now.UnixMilli(), "isError": false}
					switch mode {
					case "running-task":
						delete(task, "endTime")
					case "failed-task":
						task["isError"] = true
						task["errorCode"] = "TEST_FAILURE"
					case "stale-task":
						task["startTime"] = submitted.Add(-time.Hour).UnixMilli()
					}
					rows = task
				case fabricL3NetworksPath:
					vn := []fabricL3Network{{ID: "services", Name: "Services", Fabrics: []string{"fabric"}}, {ID: "corp", Name: "Corp", Fabrics: []string{"fabric"}}, {ID: "guest", Name: "Guest", Fabrics: []string{"fabric"}}}
					if mode == "remote-vn" {
						vn[2].Fabrics = []string{"remote"}
					}
					if late && mode == "late-vn" {
						vn[0].ID = "replaced"
					}
					if mode == "missing-membership" {
						vn[0].Fabrics = nil
					}
					rows = vn
				case sitesPath:
					rows = sampleSites()
				case fabricSitesPath:
					yes := true
					fabrics := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &yes}}
					if mode == "remote-vn" {
						fabrics = append(fabrics, fabricSite{ID: "remote", SiteID: "building", Authentication: "Closed Authentication", PubSub: &yes})
					}
					rows = fabrics
				case fabricZonesPath:
					rows = []fabricZone{{ID: "zone", SiteID: "building", Authentication: "Closed Authentication"}}
					if mode == "remote-vn" {
						rows = []fabricZone{}
					}
				case fabricDevicesPath:
					fid := r.URL.Query().Get("fabricId")
					members := []fabricDeviceMembership{{ID: fid + "-edge", NetworkDeviceID: "edge", FabricID: fid, Roles: []string{"EDGE_NODE"}}}
					if fid == "remote" {
						members[0].NetworkDeviceID = "remote-edge"
					}
					if fid == "fabric" {
						members = append(members, fabricDeviceMembership{ID: "border", NetworkDeviceID: "border", FabricID: fid, Roles: []string{"BORDER_NODE"}})
					}
					if late && mode == "late-member" || recovery && mode == "changed-recovery-member" {
						members[0].ID = "replaced-member"
					}
					rows = members
				case devicesPath:
					devices := []Device{{ID: "edge", Hostname: "edge", Serial: "FOC12345678"}, {ID: "border", Hostname: "border", Serial: "FOC87654321"}}
					if late && mode == "late-device" {
						devices[0].Serial = "FOC99999999"
					}
					if mode == "remote-vn" {
						devices = append(devices, Device{ID: "remote-edge", Hostname: "remote-edge", Serial: "FOC77777777"})
					}
					if mode == "missing-device" {
						devices = devices[:1]
					}
					if mode == "aliased-serial" {
						devices[1].Serial = devices[0].Serial
					}
					rows = devices
				case fabricExtranetPath:
					policies := []fabricExtranetPolicy{}
					if created && mode != "missing-readback" || mode == "existing" || mode == "conflict" || late && mode == "late-policy" {
						fid := "fabric"
						if mode == "zone" {
							fid = "zone"
						}
						policy := fabricExtranetPolicy{ID: "policy", Name: "Shared", Provider: "Services", Subscribers: []string{"Corp", "Guest"}, Fabrics: []string{fid}}
						if mode == "conflict" || recovery && mode == "changed-readback" {
							policy.Subscribers = []string{"Corp"}
						}
						if buildingReceipt {
							receiptReads++
							if mode == "receipt-native-replacement" && receiptReads > 1 {
								policy.ID = "replaced-policy"
							}
						}
						policies = append(policies, policy)
					}
					if late && mode == "late-peer-policy" || recovery && mode == "recovery-peer-policy" {
						policies = append(policies, fabricExtranetPolicy{ID: "peer-policy", Name: "Other", Provider: "Services", Subscribers: []string{"Corp"}})
					}
					rows = policies
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			raw := extranetModel
			if mode == "zone" {
				raw = strings.Replace(raw, "Global/Lab", "Global/Lab/Building", 1)
			}
			if mode == "unscoped" {
				raw = strings.Replace(raw, `,"fabric_sites":["Global/Lab"]`, "", 1)
			}
			parse := func() fabricExtranetIntent {
				var root map[string]any
				if err := json.Unmarshal([]byte(raw), &root); err != nil {
					t.Fatal(err)
				}
				fabric := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)
				intents, err := parseFabricExtranets(fabric["extranet_policies"])
				if err != nil {
					t.Fatal(err)
				}
				return intents[0]
			}
			want := parse()
			write, err := c.planExtranetCreate(ctx, want)
			switch mode {
			case "existing":
				if err != nil || write != nil || writes != 0 {
					t.Fatal(write, err, writes)
				}
				return
			case "conflict", "unscoped", "missing-membership", "missing-device", "aliased-serial":
				if err == nil || write != nil || writes != 0 {
					t.Fatal(write, err, writes)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(write, err)
			}
			snapshot, err := c.extranetSnapshot(ctx, want)
			if err != nil {
				t.Fatal(err)
			}
			expected := 2
			if mode == "remote-vn" {
				expected = 3
			}
			if len(snapshot.physical) != expected {
				t.Fatal("incomplete physical footprint", snapshot.physical)
			}
			late = true
			if mode == "caller-change" {
				want.policy.Subscribers[0] = "Mutated"
				want.paths[0] = "Global/Changed"
			}
			task, err := write.submit(ctx)
			if strings.HasPrefix(mode, "late-") {
				if err == nil || writes != 0 {
					t.Fatal("stale submission", err, writes)
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("lost receipt accepted", err, writes)
				}
				return
			}
			if err != nil || writes != 1 || task.ID != "task" {
				t.Fatal(task, err, writes)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Create", Resource: write.resource, TaskID: task.ID, SubmittedAt: &submitted}
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			if mode == "missing-task" {
				op.TaskID = ""
			}
			recovery = true
			ok, err := c.verifyExtranetCreate(ctx, parse(), op, now)
			valid := mode == "receipt-native-replacement" || mode == "create" || mode == "zone" || mode == "remote-vn" || mode == "caller-change"
			if valid {
				if !ok || err != nil {
					t.Fatal(ok, err)
				}
				prepared := op.DeepCopy()
				prepared.ID = strings.Repeat("a", 32)
				prepared.ConfigName = "config"
				prepared.ConfigUID = "config-uid"
				prepared.ConfigGeneration = 1
				prepared.ControllerGeneration = 1
				prepared.Section = "fabric"
				prepared.Phase = "Prepared"
				prepared.TaskID = ""
				prepared.SubmittedAt = nil
				targets := []contract.GroupTargetIdentity{}
				for _, device := range snapshot.physical {
					targets = append(targets, contract.GroupTargetIdentity{InventoryID: device.ID, PhysicalIdentity: device.Serial, Device: ops.ConfigurationReservationReference{Name: device.ID, UID: device.ID + "-uid"}, Node: ops.ConfigurationReservationReference{Name: device.ID, UID: device.ID + "-node-uid"}})
				}
				group, err := contract.WithGroupIdentity(prepared, targets)
				if err != nil {
					t.Fatal(err)
				}
				group.Phase = "Verifying"
				group.TaskID = op.TaskID
				group.SubmittedAt = op.SubmittedAt
				buildingReceipt = true
				receipt, err := c.verifiedExtranetReceipt(ctx, parse(), group, now)
				if mode == "receipt-native-replacement" {
					if err == nil || receipt != nil {
						t.Fatal("replaced native policy acquired ownership", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: "config", ConfigUID: "config-uid"}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{*receipt}}
				// Persisted JSON is the input after restart; no in-memory plan is retained.
				encoded, _ := json.Marshal(state)
				var restored cvk.NetworkControllerConfigurationStatus
				if err := json.Unmarshal(encoded, &restored); err != nil {
					t.Fatal(err)
				}
				if owned, err := ownedExtranetPolicy(&restored, "config", "config-uid", "Shared", "policy", now); !owned || err != nil {
					t.Fatal(owned, err)
				}
				if mode == "create" {
					testExtranetApplicationPersistence(t, c, raw, group, now)
				}
				if group.Phase != "Verifying" || group.TargetID != "" {
					t.Fatal("receipt builder mutated immutable operation")
				}
				incomplete := prepared.DeepCopy()
				truncated, err := contract.WithGroupIdentity(incomplete, targets[:1])
				if err != nil {
					t.Fatal(err)
				}
				truncated.Phase = "Verifying"
				truncated.TaskID = op.TaskID
				truncated.SubmittedAt = op.SubmittedAt
				if receipt, err := c.verifiedExtranetReceipt(ctx, parse(), truncated, now); err == nil || receipt != nil {
					t.Fatal("receipt accepted incomplete physical group", err)
				}

			} else if ok {
				t.Fatal("invalid result accepted", mode, err)
			}
			if writes != 1 {
				t.Fatal("verification replayed write", writes)
			}
		})
	}
}
