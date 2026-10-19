// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestFabricL2HandoffCreateLifecycle(t *testing.T)     { testFabricL2HandoffLifecycle(t, false) }
func TestFabricL2HandoffDeploymentLifecycle(t *testing.T) { testFabricL2HandoffLifecycle(t, true) }
func testFabricL2HandoffLifecycle(t *testing.T, deployment bool) {
	for _, mode := range []string{"dispatch-recovery-publish-denied", "dispatch-recovery-publish-lost", "dispatch-recovery-publish-incomplete", "dispatch-recovery-public", "dispatch-recovery-public-unknown", "dispatch-recovery-public-replaced", "dispatch-recovery-public-missing", "dispatch-recovery-public-denied", "dispatch-recovery-public-source", "dispatch-recovery-valid", "dispatch-recovery-unknown", "dispatch-recovery-running", "dispatch-recovery-failed", "dispatch-recovery-missing", "dispatch-recovery-denied", "dispatch-recovery-no-task", "dispatch-recovery-node", "dispatch-denied", "dispatch-valid", "dispatch-lost", "dispatch-revoked", "unassigned", "unprovisioned", "replaced-provision", "missing-site", "missing-fabric", "late-site", "late-fabric", "recovery-site", "create", "existing", "conflict", "late-vlan", "late-handoff", "missing-task", "async", "write-error", "readback-missing", "readback-conflict", "changed-peer"} {
		t.Run(mode, func(t *testing.T) {
			if !deployment && (strings.HasPrefix(mode, "dispatch-") || mode == "unassigned" || mode == "unprovisioned" || mode == "replaced-provision") {
				t.Skip("deployment prerequisite")
			}
			posts := 0
			var afterPost func()
			now := time.Now().UTC()
			receiptMode := ""
			receiptReads := 0
			late, submitted, verify := false, false, false
			want := fabricL2HandoffIntent{device: "border", port: "GigabitEthernet1/0/1", network: "Campus", pool: "Users", externalVLAN: 400}
			desired := fabricL2Handoff{DeviceID: "device", FabricID: "fabric", Port: want.port, InternalVLAN: 100, ExternalVLAN: 400}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == fabricL2HandoffsPath {
					posts++
					var payload []fabricL2Handoff
					if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || payload[0] != desired {
						t.Error("incorrect native handoff payload")
					}
					submitted = true
					if afterPost != nil {
						afterPost()
					}
					switch mode {
					case "missing-task":
						fmt.Fprint(w, `{"response":{}}`)
						return
					case "async":
						w.WriteHeader(202)
					case "write-error", "dispatch-lost":
						w.WriteHeader(400)
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				var rows any
				switch r.URL.Path {
				case taskPath + "task/tree":
					rows = []map[string]any{{"id": "task", "isError": false, "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": (receiptMode == "failed-child" || mode == "dispatch-recovery-failed"), "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}}
					if receiptMode == "pending-child" || mode == "dispatch-recovery-running" {
						rows.([]map[string]any)[1]["endTime"] = 0
					}
				case sitesPath:
					sites := sampleSites()
					if (late && mode == "late-site") || (verify && mode == "recovery-site") {
						sites[1].ID = "new-area"
						sites[2].ParentID = "new-area"
					}
					if mode == "missing-site" {
						sites = sites[:1]
					}
					rows = sites
				case fabricSitesPath:
					enabled := false
					fabric := fabricSite{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &enabled}
					if (late && mode == "late-site") || (verify && mode == "recovery-site") {
						fabric.SiteID = "new-area"
					}
					if late && mode == "late-fabric" {
						fabric.ID = "replacement-fabric"
					}
					fabrics := []fabricSite{fabric}
					if mode == "missing-fabric" {
						fabrics = []fabricSite{}
					}
					rows = fabrics
				case assignedDevicesPath:
					assigned := []map[string]string{{"deviceId": "device", "siteId": "building"}}
					if mode == "unassigned" {
						assigned = []map[string]string{}
					}
					rows = assigned
				case wiredProvisionPath:
					provisioned := []wiredProvisionedDevice{{ID: "provision", DeviceID: "device", SiteID: "building"}}
					if mode == "unprovisioned" {
						provisioned = []wiredProvisionedDevice{}
					}
					if late && mode == "replaced-provision" {
						provisioned[0].ID = "replacement"
					}
					rows = provisioned
				case devicesPath:
					rows = []Device{{ID: "device", Hostname: "border", Serial: "FOC12345678", ManagementIP: "198.51.100.101"}}
				case fabricDevicesPath:
					members := []fabricDeviceConfiguration{{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_2"}}}}
					if verify && mode == "changed-peer" {
						members[0].Roles = append(members[0].Roles, "CONTROL_PLANE_NODE")
					}
					rows = members
				case "/dna/intent/api/v1/interface/network-device/device":
					rows = []lanLinkInterface{{ID: "port", DeviceID: "device", PortName: want.port, Type: "Physical", Index: "1"}}
				case anycastGatewaysPath:
					vlan := 100
					if late && mode == "late-vlan" {
						vlan = 101
					}
					rows = []map[string]any{{"id": "gateway", "fabricId": "fabric", "virtualNetworkName": "Campus", "ipPoolName": "Users", "vlanName": "Users", "vlanId": vlan, "trafficType": "DATA"}}
				case fabricL2NetworksPath:
					rows = []fabricL2Network{}
				case fabricL2HandoffsPath:
					handoffs := []fabricL2Handoff{}
					if mode == "existing" || mode == "conflict" || (late && mode == "late-handoff") || (submitted && mode != "readback-missing" && mode != "dispatch-recovery-missing") {
						row := desired
						row.ID = "created-handoff"
						if receiptMode != "" {
							receiptReads++
							if receiptMode == "replacement" && receiptReads > 1 {
								row.ID = "replacement-handoff"
							}
						}
						if mode == "conflict" || (verify && mode == "readback-conflict") {
							row.InternalVLAN = 101
						}
						handoffs = append(handoffs, row)
					}
					rows = handoffs
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			ctx := context.Background()
			ref := configurationDeviceReference{Name: "border"}
			intent := fabricL2HandoffDeploymentIntent{handoff: want, device: fabricDeviceIntent{inventory: inventoryDeviceIntent{reference: ref, site: "Global/Lab/Building"}, state: "PROVISION", fabricSite: "Global/Lab", roles: []string{"BORDER_NODE"}, border: &fabricBorderSettings{Types: []string{"LAYER_2"}}}}
			if strings.HasPrefix(mode, "dispatch-") {
				testFabricL2HandoffDispatch(t, c, want, mode, &afterPost)
				expected := 1
				if mode == "dispatch-denied" {
					expected = 0
				}
				if posts != expected {
					t.Fatal("incorrect dispatch count", posts)
				}
				return
			}
			plan := func() (*configurationWrite, error) {
				if deployment {
					return c.planFabricL2HandoffDeployment(ctx, intent)
				}
				return c.planFabricL2HandoffCreate(ctx, want, ref, "Global/Lab")
			}
			readback := func(op *cvk.NetworkControllerConfigurationOperation) (string, error) {
				if deployment {
					return c.verifyFabricL2HandoffDeployment(ctx, intent, op)
				}
				return c.verifyFabricL2HandoffCreate(ctx, want, ref, "Global/Lab", op)
			}
			write, err := plan()
			if mode == "unassigned" || mode == "unprovisioned" || mode == "conflict" || mode == "missing-site" || mode == "missing-fabric" {
				if err == nil || posts != 0 {
					t.Fatal("conflict accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "existing" {
				if write != nil || posts != 0 {
					t.Fatal("matching handoff rewritten")
				}
				return
			}
			if write == nil {
				t.Fatal("missing create plan")
			}
			late = true
			task, err := write.submit(ctx)
			failed := mode == "replaced-provision" || mode == "late-site" || mode == "late-fabric" || mode == "late-vlan" || mode == "late-handoff" || mode == "missing-task" || mode == "async" || mode == "write-error"
			if (err != nil) != failed {
				t.Fatalf("submit mode=%s error=%v", mode, err)
			}
			if _, err := write.submit(ctx); err == nil {
				t.Fatal("write replayed")
			}
			expectedPosts := 1
			if mode == "replaced-provision" || mode == "late-site" || mode == "late-fabric" || mode == "late-vlan" || mode == "late-handoff" {
				expectedPosts = 0
			}
			if posts != expectedPosts {
				t.Fatal("unexpected native submission count", posts)
			}
			if failed {
				return
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Create", TaskID: task.ID, TargetID: write.identity.target, ParentID: write.identity.parent, Resource: write.resource}
			verify = true
			id, err := readback(op)
			if mode == "recovery-site" || mode == "readback-conflict" || mode == "changed-peer" {
				if err == nil {
					t.Fatal("foreign state accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (id == "created-handoff") != (mode == "create") {
				t.Fatal("incorrect native ownership identity", id)
			}
			if deployment && mode == "create" {
				for _, receiptCase := range []string{"valid", "pending-child", "failed-child", "foreign-physical", "missing-group", "bad-group", "future-submit", "missing-submit", "wrong-section", "replacement"} {
					t.Run("receipt-"+receiptCase, func(t *testing.T) {
						receiptMode, receiptReads = receiptCase, 0
						prepared := op.DeepCopy()
						prepared.ID = strings.Repeat("a", 32)
						prepared.ConfigName, prepared.ConfigUID = "config", "config-uid"
						prepared.ConfigGeneration, prepared.ControllerGeneration = 1, 1
						prepared.Section, prepared.Phase, prepared.TaskID = "fabric", "Prepared", ""
						serial := "foc12345678"
						if receiptCase == "foreign-physical" {
							serial = "foc87654321"
						}
						journal, err := contract.WithGroupIdentity(prepared, []contract.GroupTargetIdentity{{InventoryID: "device", PhysicalIdentity: serial, Device: ops.ConfigurationReservationReference{Name: "device", UID: "device-uid"}, Node: ops.ConfigurationReservationReference{Name: "node", UID: "node-uid"}}})
						if err != nil {
							t.Fatal(err)
						}
						journal.Phase, journal.TaskID = "Verifying", "task"
						at := metav1.NewTime(now.Add(-time.Minute))
						journal.SubmittedAt = &at
						switch receiptCase {
						case "missing-group":
							journal.PhysicalGroup = nil
						case "bad-group":
							journal.PhysicalGroup.Digest = strings.Repeat("b", 64)
						case "future-submit":
							journal.SubmittedAt = &metav1.Time{Time: now.Add(time.Minute)}
						case "missing-submit":
							journal.SubmittedAt = nil
						case "wrong-section":
							journal.Section = "inventory"
						}
						receipt, err := c.verifiedFabricL2HandoffReceipt(ctx, intent, journal, now)
						if receiptCase == "valid" {
							if err != nil || receipt == nil {
								t.Fatal("receipt unavailable", err)
							}
							if !strings.HasSuffix(receipt.Resource, ":created-handoff") || receipt.Operation.Phase != "Verified" || receipt.Operation.PhysicalGroup.Digest != journal.PhysicalGroup.Digest || journal.Phase != "Verifying" || receipt.VerifiedAt == nil || !receipt.VerifiedAt.Time.Equal(now) {
								t.Fatal("incorrect durable evidence")
							}
						} else if err == nil || receipt != nil {
							t.Fatal("invalid receipt accepted", receiptCase)
						}
						if receiptCase == "valid" {
							testFabricL2HandoffApplicationPersistence(t, c, journal, want, now)
						}
						if posts != 1 {
							t.Fatal("verification submitted a mutation")
						}
					})
				}
			}
			op.TaskID = ""
			if _, err := readback(op); err == nil {
				t.Fatal("readback fabricated missing receipt")
			}
		})
	}
}
