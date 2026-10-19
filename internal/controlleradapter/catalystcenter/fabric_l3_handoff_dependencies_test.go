// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFabricL3IPHandoffDependencies(t *testing.T) { testFabricL3IPHandoffFlow(t, false) }
func TestFabricL3IPHandoffExecution(t *testing.T)    { testFabricL3IPHandoffFlow(t, true) }
func testFabricL3IPHandoffFlow(t *testing.T, execute bool) {
	for _, mode := range []string{"valid", "custom", "missing-pool", "wrong-site", "outside-parent", "changed-pool", "changed-transit", "changed-provision", "exec-existing", "exec-conflict", "exec-denied", "exec-missing-task", "exec-error", "exec-async", "exec-readback-missing", "exec-readback-conflict", "dispatch-valid", "dispatch-denied", "dispatch-lost", "dispatch-revoked", "dispatch-recovery-public", "dispatch-recovery-public-unknown", "dispatch-recovery-public-replaced", "dispatch-recovery-public-missing", "dispatch-recovery-public-denied", "dispatch-recovery-public-source", "dispatch-recovery-valid", "dispatch-recovery-unknown", "dispatch-recovery-running", "dispatch-recovery-failed", "dispatch-recovery-missing", "dispatch-recovery-denied", "dispatch-recovery-no-task", "dispatch-recovery-node", "dispatch-recovery-publish-denied", "dispatch-recovery-publish-lost", "dispatch-recovery-publish-incomplete"} {
		t.Run(mode, func(t *testing.T) {
			if !execute && (strings.HasPrefix(mode, "exec-") || strings.HasPrefix(mode, "dispatch-")) {
				t.Skip("execution fixture")
			}
			flag := true
			border := &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}
			device := fabricDeviceIntent{inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "border"}, site: "Global/Lab/Building"}, state: "PROVISION", fabricSite: "Global/Lab", roles: []string{"BORDER_NODE"}, border: border}
			want := fabricL3IPHandoffIntent{device: "border", transit: "Transit", port: "GigabitEthernet1/0/1", network: "Users", pool: "access", vlan: 300}
			if mode == "custom" {
				want.pool, want.local4, want.peer4 = "", "192.0.2.1/30", "192.0.2.2/30"
			}
			changed, submitted, reading := false, false, false
			posts := 0
			var afterPost func()
			now := time.Now().UTC().Truncate(time.Second)
			desired := fabricL3IPHandoff{DeviceID: "device", FabricID: "fabric", TransitID: "transit", Port: want.port, Network: want.network, VLAN: want.vlan, Pool: want.pool}
			if want.pool == "" {
				desired.Local4, desired.Peer4 = want.local4, want.peer4
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == fabricL3IPHandoffsPath {
					posts++
					submitted = true
					if afterPost != nil {
						afterPost()
					}
					var body []fabricL3IPHandoff
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || !reflect.DeepEqual(body[0], desired) {
						t.Error("incorrect handoff payload")
					}
					switch mode {
					case "exec-missing-task":
						fmt.Fprint(w, `{"response":{}}`)
						return
					case "exec-error", "dispatch-lost":
						w.WriteHeader(400)
					case "exec-async":
						w.WriteHeader(202)
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("dependency collector mutated controller")
					w.WriteHeader(400)
					return
				}
				var rows any
				switch r.URL.Path {
				case taskPath + "task/tree":
					rows = []map[string]any{{"id": "task", "isError": false, "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": mode == "dispatch-recovery-failed", "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}}
					if mode == "dispatch-recovery-running" {
						rows.([]map[string]any)[1]["endTime"] = 0
					}

				case sitesPath:
					rows = sampleSites()
				case fabricSitesPath:
					rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}}
				case devicesPath:
					rows = []Device{{ID: "device", Hostname: "border", Serial: "FOC12345678", ManagementIP: "198.51.100.101"}}
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "device", "siteId": "building"}}
				case wiredProvisionPath:
					id := "provision"
					if changed && mode == "changed-provision" {
						id = "replacement"
					}
					rows = []wiredProvisionedDevice{{ID: id, DeviceID: "device", SiteID: "building"}}
				case fabricDevicesPath:
					rows = []fabricDeviceConfiguration{{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: border}}
				case "/dna/intent/api/v1/interface/network-device/device":
					rows = []lanLinkInterface{{ID: "port", DeviceID: "device", PortName: want.port, Type: "Physical", Index: "1"}}
				case fabricTransitsPath:
					asn := "65001"
					if changed && mode == "changed-transit" {
						asn = "65002"
					}
					rows = []fabricTransit{{ID: "transit", Name: "Transit", Type: "IP_BASED_TRANSIT", IP: &ipTransitSettings{Protocol: "BGP", ASN: asn}}}
				case fabricL3NetworksPath:
					rows = []fabricL3Network{{ID: "network", Name: "Users", Fabrics: []string{"fabric"}}}
				case fabricL3IPHandoffsPath:
					handoffs := []fabricL3IPHandoff{}
					if mode == "exec-existing" || mode == "exec-conflict" || submitted && mode != "exec-readback-missing" && mode != "dispatch-recovery-missing" {
						row := desired
						row.ID = "handoff"
						if mode == "exec-conflict" || reading && mode == "exec-readback-conflict" {
							row.VLAN = 301
						}
						handoffs = append(handoffs, row)
					}
					rows = handoffs
				case globalPoolsPath:
					if mode == "custom" {
						t.Error("custom addressing requested IPAM inventory")
					}
					rows = fixturePools()
				case sitePoolsPath:
					reservations := fixtureReservations()
					if changed {
						switch mode {
						case "missing-pool":
							reservations = nil
						case "wrong-site":
							reservations[0].SiteID = "building"
						case "changed-pool":
							reservations[0].ID = "replacement"
						case "outside-parent":
							reservations[0].V4.Subnet = "198.51.100.0"
						}
					}
					rows = reservations
				default:
					t.Error("unexpected dependency endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			if strings.HasPrefix(mode, "dispatch-") {
				testFabricL3IPHandoffDispatch(t, c, want, mode, &afterPost)
				expected := 1
				if mode == "dispatch-denied" {
					expected = 0
				}
				if posts != expected {
					t.Fatal("dispatch mutation count", posts)
				}
				return
			}
			if execute {
				ctx := context.Background()
				write, err := c.planGuardedFabricL3IPHandoffCreate(ctx, want, device, func(context.Context) error {
					if mode == "exec-denied" {
						return errors.New("fixture denied")
					}
					return nil
				})
				if mode == "exec-conflict" {
					if err == nil || posts != 0 {
						t.Fatal("conflict accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if mode == "exec-existing" {
					if write != nil || posts != 0 {
						t.Fatal("matching handoff rewritten")
					}
					return
				}
				if write == nil {
					t.Fatal("missing plan")
				}
				changed = true
				task, err := write.submit(ctx)
				blocked := mode == "missing-pool" || mode == "wrong-site" || mode == "outside-parent" || strings.HasPrefix(mode, "changed-") || mode == "exec-denied"
				failed := blocked || mode == "exec-missing-task" || mode == "exec-error" || mode == "exec-async"
				if (err != nil) != failed {
					t.Fatal("submission result", err)
				}
				expected := 1
				if blocked {
					expected = 0
				}
				if posts != expected {
					t.Fatal("incorrect mutation count", posts)
				}
				if _, err := write.submit(ctx); err == nil || posts != expected {
					t.Fatal("submission replayed")
				}
				if failed {
					return
				}
				reading = true
				op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Create", TaskID: task.ID, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
				id, err := c.verifyFabricL3IPHandoffCreate(ctx, want, device, op)
				if mode == "exec-readback-conflict" {
					if err == nil {
						t.Fatal("conflicting readback accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if (id == "handoff") != (mode != "exec-readback-missing") {
					t.Fatal("incorrect readback", id)
				}
				return
			}
			before, err := c.fabricL3IPHandoffDependencies(context.Background(), want, device)
			if err != nil || before == nil || len(before.physical) != 1 || before.binding.desired.TransitID != "transit" {
				t.Fatal("missing complete dependency evidence", err)
			}
			changed = true
			after, err := c.fabricL3IPHandoffDependencies(context.Background(), want, device)
			invalid := mode == "missing-pool" || mode == "wrong-site" || mode == "outside-parent"
			if invalid {
				if err == nil || after != nil {
					t.Fatal("invalid pool evidence accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			stable := mode == "valid" || mode == "custom"
			if (before.digest == after.digest) != stable {
				t.Fatal("incorrect dependency change detection")
			}
		})
	}
}
