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

func TestOwnedExtranetExpansion(t *testing.T) {
	for _, mode := range []string{"update", "update-v2", "scope-add", "noop", "unowned", "provider-change", "remove-subscriber", "remove-scope", "unscoped-existing", "late-native", "late-owner", "late-source", "lost-receipt", "unknown", "task-running", "task-failed", "replaced-readback", "incomplete-readback"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			raw := extranetModel
			if mode == "scope-add" {
				raw = strings.Replace(raw, `"fabric_sites":["Global/Lab"]`, `"fabric_sites":["Global/Lab","Global/Lab/Building"]`, 1)
			}
			config.Spec.Source.Inline.Raw = []byte(raw)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			create := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "fabric", Action: "Create", Phase: "Prepared", Resource: "extranet-create:" + strings.Repeat("b", 64), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation}
			create, err = contract.WithGroupIdentity(create, []contract.GroupTargetIdentity{{InventoryID: "edge", PhysicalIdentity: "foc12345678", Device: ops.ConfigurationReservationReference{Name: "edge", UID: "edge-uid"}, Node: ops.ConfigurationReservationReference{Name: "edge", UID: "node-uid"}}})
			if err != nil {
				t.Fatal(err)
			}
			createdAt := metav1.NewTime(now.Add(-time.Hour))
			verifiedAt := metav1.NewTime(now.Add(-time.Minute))
			create.Phase = "Verified"
			create.TaskID = "old-task"
			create.SubmittedAt = &createdAt
			prefix, _ := extranetReceiptPrefix("Shared")
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: prefix + "policy", Operation: *create, VerifiedAt: &verifiedAt}}}
			if mode == "unowned" {
				nc.Status.Configuration.ActionApplications = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(raw), &root); err != nil {
				t.Fatal(err)
			}
			intents, err := parseFabricExtranets(root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)["extranet_policies"])
			if err != nil {
				t.Fatal(err)
			}
			want := intents[0]
			late, updated, recovery := false, false, false
			writes := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "PUT" {
					writes++
					if r.URL.Path != fabricExtranetPath {
						t.Error("unexpected mutation path")
						w.WriteHeader(500)
						return
					}
					var body []fabricExtranetPolicy
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].ID != "policy" || body[0].Provider != "Services" || len(body[0].Subscribers) != 2 {
						t.Error("invalid update payload")
					}
					if mode == "scope-add" && len(body[0].Fabrics) != 2 {
						t.Error("missing added scope")
					}
					updated = true
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"update-task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case sitesPath:
					rows = sampleSites()
				case fabricSitesPath:
					yes := true
					rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &yes}}
				case fabricZonesPath:
					rows = []fabricZone{{ID: "zone", SiteID: "building", Authentication: "Closed Authentication"}}
				case fabricL3NetworksPath:
					rows = []fabricL3Network{{ID: "services", Name: "Services", Fabrics: []string{"fabric"}}, {ID: "corp", Name: "Corp", Fabrics: []string{"fabric"}}, {ID: "guest", Name: "Guest", Fabrics: []string{"fabric"}}}
				case devicesPath:
					rows = []Device{{ID: "edge", Hostname: "edge", Serial: "FOC12345678"}}
				case fabricDevicesPath:
					fid := r.URL.Query().Get("fabricId")
					rows = []fabricDeviceMembership{{ID: fid + "-row", NetworkDeviceID: "edge", FabricID: fid, Roles: []string{"EDGE_NODE"}}}
				case taskPath + "update-task":
					task := map[string]any{"id": "update-task", "isError": false, "startTime": now.Add(-20 * time.Second).UnixMilli(), "endTime": now.UnixMilli()}
					if mode == "task-running" {
						delete(task, "endTime")
					}
					if mode == "task-failed" {
						task["isError"] = true
					}
					rows = task
				case fabricExtranetPath:
					p := fabricExtranetPolicy{ID: "policy", Name: "Shared", Provider: "Services", Subscribers: []string{"Corp"}, Fabrics: []string{"fabric"}}
					if updated || mode == "noop" {
						p.Subscribers = []string{"Corp", "Guest"}
					}
					if updated && mode == "scope-add" {
						p.Fabrics = []string{"fabric", "zone"}
					}
					switch mode {
					case "provider-change":
						p.Provider = "Other"
					case "remove-subscriber":
						p.Subscribers = []string{"Corp", "Other"}
					case "remove-scope":
						p.Fabrics = []string{"fabric", "other"}
					case "unscoped-existing":
						p.Fabrics = nil
					}
					if late && mode == "late-native" || recovery && mode == "replaced-readback" {
						p.ID = "replacement"
					}
					if recovery && mode == "incomplete-readback" {
						p.Subscribers = []string{"Corp"}
					}
					if late && mode == "late-owner" {
						current, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							return
						}
						current.Status.Configuration.Owners[0].ConfigUID = "other"
						if err := a.statusWriter.Status().Update(ctx, current); err != nil {
							t.Error(err)
						}
					}
					if late && mode == "late-source" {
						copy := config.DeepCopy()
						copy.Generation++
						if err := a.statusWriter.Update(ctx, copy); err != nil {
							t.Error(err)
						}
					}
					rows = []fabricExtranetPolicy{p}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			write, err := a.planOwnedExtranetExpansion(ctx, config, source, want)
			if mode == "noop" {
				if err != nil || write != nil {
					t.Fatal(write, err)
				}
				return
			}
			if mode == "unowned" || mode == "provider-change" || mode == "remove-subscriber" || mode == "remove-scope" || mode == "unscoped-existing" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal(write, err, writes)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(write, err)
			}
			if mode == "update-v2" {
				before, err := a.client.extranetSnapshot(ctx, want)
				if err != nil {
					t.Fatal(err)
				}
				baseline, err := wirelessTagDigest(before.policies[want.policy.Name])
				if err != nil {
					t.Fatal(err)
				}
				write.resource = "extranet-update-v2:policy:" + baseline + ":" + before.digest
			}
			late = true
			task, err := write.submit(ctx)
			if strings.HasPrefix(mode, "late-") {
				if err == nil || writes != 0 {
					t.Fatal("unsafe late submission", err, writes)
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("missing receipt accepted", err, writes)
				}
				return
			}
			if err != nil || writes != 1 {
				t.Fatal(task, err, writes)
			}
			recovery = true
			submitted := metav1.NewTime(now.Add(-20 * time.Second))
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Update", Resource: write.resource, TargetID: write.identity.target, TaskID: task.ID, SubmittedAt: &submitted}
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			ok, err := a.client.verifyExtranetExpansion(ctx, want, op, now)
			valid := mode == "update" || mode == "update-v2" || mode == "scope-add"
			if valid {
				if err != nil || !ok {
					t.Fatal(ok, err)
				}
			} else if ok {
				t.Fatal("invalid update verified", err)
			}
			if writes != 1 {
				t.Fatal("recovery replayed update")
			}
			if valid {
				prepared := op.DeepCopy()
				prepared.Phase = "Prepared"
				prepared.TaskID = ""
				prepared.SubmittedAt = nil
				prepared.ID = strings.Repeat("c", 32)
				prepared.Section = "fabric"
				prepared.ConfigName = config.Name
				prepared.ConfigUID = string(config.UID)
				prepared.ConfigGeneration = config.Generation
				prepared.ControllerGeneration = nc.Generation
				group, err := contract.WithGroupIdentity(prepared, create.PhysicalGroup.Targets)
				if err != nil {
					t.Fatal(err)
				}
				group.Phase = "Verifying"
				group.TaskID = op.TaskID
				group.SubmittedAt = op.SubmittedAt
				testExtranetApplicationPersistence(t, a.client, raw, group, now, nc.Status.Configuration.ActionApplications[0])
				noop, err := a.planOwnedExtranetExpansion(ctx, config, source, want)
				if err != nil || noop != nil {
					t.Fatal("update did not converge", noop, err)
				}
			}
		})
	}
}
