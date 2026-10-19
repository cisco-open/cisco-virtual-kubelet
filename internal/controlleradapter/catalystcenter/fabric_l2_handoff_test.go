// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricL2HandoffModel(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		count     int
	}{
		{"hybrid", `{"l2_with_anycast_gateway":[{"l3_virtual_network":"Campus","ip_pool_name":"Users","external_vlan":400,"interfaces":["GigabitEthernet1/0/1","GigabitEthernet1/0/2"]}],"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":500}],"interfaces":["GigabitEthernet1/0/1"]}}`, 3},
		{"unknown", `{"future":true}`, -1},
		{"nested-unknown", `{"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":500,"future":true}],"interfaces":["Gi1"]}}`, -1},
		{"duplicate", `{"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":500}],"interfaces":["Gi1","gi1"]}}`, -1},
		{"fraction", `{"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":1.2}],"interfaces":["Gi1"]}}`, -1},
		{"range", `{"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":4095}],"interfaces":["Gi1"]}}`, -1},
		{"missing-dependency", `{"l2_with_anycast_gateway":[{"external_vlan":400,"interfaces":["Gi1"]}]}`, -1},
		{"conflicting", `{"l2_without_anycast_gateway":{"vlans":[{"name":"IoT","external_vlan":500},{"name":"Other","external_vlan":500}],"interfaces":["Gi1"]}}`, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			rows, err := parseFabricL2Handoffs("border", value)
			if (err == nil) != (tc.count >= 0) || err == nil && len(rows) != tc.count {
				t.Fatalf("rows=%v error=%v", rows, err)
			}
		})
	}
}

func TestFabricL2HandoffBinding(t *testing.T) {
	for _, mode := range []string{"anycast", "layer2", "missing", "duplicate", "wrong-vn", "wrong-fabric", "wrong-device", "not-border", "virtual-port", "has-anycast", "unknown-gateway"} {
		t.Run(mode, func(t *testing.T) {
			want := fabricL2HandoffIntent{device: "border", port: "GigabitEthernet1/0/1", network: "Campus", pool: "Users", externalVLAN: 400}
			device := fabricDeviceConfiguration{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_2"}}}
			ports := []lanLinkInterface{{ID: "port", DeviceID: "device", PortName: want.port, Type: "Physical"}}
			gateways := []anycastGateway{{ID: "gateway", FabricID: "fabric", Network: "Campus", Pool: "Users", VLAN: 100}}
			networks := []fabricL2Network{{ID: "network", FabricID: "fabric", VLANName: "IoT", VLAN: 200}}
			switch mode {
			case "layer2", "has-anycast":
				want.network, want.pool, want.vlanName = "", "", "IoT"
				if mode == "has-anycast" {
					gateways[0].VLAN = 200
				}
			case "unknown-gateway":
				gateways[0].unknownFields = true
			case "missing":
				gateways = nil
			case "duplicate":
				gateways = append(gateways, gateways[0])
			case "wrong-vn":
				gateways[0].Network = "Other"
			case "wrong-fabric":
				gateways[0].FabricID = "other"
			case "wrong-device":
				want.device = "other"
			case "not-border":
				device.Roles = []string{"EDGE_NODE"}
			case "virtual-port":
				ports[0].MappedID = "other"
			}
			got, err := bindFabricL2Handoff(want, "border", device, ports, gateways, networks)
			valid := mode == "anycast" || mode == "layer2"
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if valid && (got.desired.ID != "" || got.dependencyID == "" || got.interfaceID != "port" || got.desired.ExternalVLAN != 400) {
				t.Fatal("lost dependency binding")
			}
			if valid {
				if len(got.digest) != 64 {
					t.Fatal("dependency digest missing")
				}
				ports[0].Mode = "trunk"
				changed, err := bindFabricL2Handoff(want, "border", device, ports, gateways, networks)
				if err != nil || changed.digest == got.digest {
					t.Fatal("interface change lost from dependency evidence", err)
				}
				ports[0].Mode = ""
				if mode == "anycast" {
					gateways[0].VLAN = 101
				} else {
					networks[0].VLAN = 201
				}
				changed, err = bindFabricL2Handoff(want, "border", device, ports, gateways, networks)
				if err != nil || changed.digest == got.digest {
					t.Fatal("allocated VLAN change lost from dependency evidence", err)
				}
			}

		})
	}
}

func TestFabricL2HandoffInventory(t *testing.T) {
	for _, mode := range []string{"valid", "foreign", "duplicate", "conflict", "malformed", "async"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != fabricL2HandoffsPath || r.URL.Query().Get("fabricId") != "fabric" || r.URL.Query().Get("networkDeviceId") != "device" {
					t.Errorf("unexpected handoff request")
					w.WriteHeader(400)
					return
				}
				row := fabricL2Handoff{ID: "handoff", DeviceID: "device", FabricID: "fabric", Port: "GigabitEthernet1/0/1", InternalVLAN: 100, ExternalVLAN: 400}
				if mode == "foreign" {
					row.FabricID = "other"
				}
				if mode == "malformed" {
					row.InternalVLAN = 0
				}
				rows := []fabricL2Handoff{row}
				if mode == "duplicate" || mode == "conflict" {
					if mode == "conflict" {
						row.ID = "other"
					}
					rows = append(rows, row)
				}
				if mode == "async" {
					w.WriteHeader(202)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.fabricL2Handoffs(context.Background(), "fabric", "device")
			if (err == nil) != (mode == "valid") || err == nil && len(rows) != 1 {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
		})
	}
}

func TestFabricL2HandoffPagination(t *testing.T) {
	for _, mode := range []string{"complete", "duplicate-id", "duplicate-mapping", "missing-response", "failed-page"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				offset := 1
				if calls == 2 {
					offset = 501
				}
				q := r.URL.Query()
				if calls > 2 || r.Method != "GET" || r.URL.Path != fabricL2HandoffsPath || q.Get("offset") != fmt.Sprint(offset) || q.Get("limit") != "500" || q.Get("fabricId") != "fabric" || q.Get("networkDeviceId") != "device" {
					t.Error("pagination lost scope or offset")
					w.WriteHeader(400)
					return
				}
				rows := []fabricL2Handoff{}
				count := 500
				if calls == 2 {
					count = 1
				}
				for i := offset; i < offset+count; i++ {
					rows = append(rows, fabricL2Handoff{ID: fmt.Sprintf("handoff-%d", i), DeviceID: "device", FabricID: "fabric", Port: "GigabitEthernet1/0/1", InternalVLAN: i + 1, ExternalVLAN: i + 1})
				}
				if calls == 2 {
					switch mode {
					case "duplicate-id":
						rows[0].ID = "handoff-1"
					case "duplicate-mapping":
						rows[0].InternalVLAN = 2
					case "missing-response":
						fmt.Fprint(w, `{"version":"1"}`)
						return
					case "failed-page":
						w.WriteHeader(400)
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.fabricL2Handoffs(context.Background(), "fabric", "device")
			if (err == nil) != (mode == "complete") || calls != 2 {
				t.Fatalf("calls=%d rows=%d error=%v", calls, len(rows), err)
			}
			if err == nil && len(rows) != 501 {
				t.Fatal("partial inventory accepted")
			}
			if err != nil && rows != nil {
				t.Fatal("failed pagination leaked partial snapshot")
			}
		})
	}
}

func TestFabricL2HandoffDependencySnapshot(t *testing.T) {
	for _, mode := range []string{"stable", "vlan-change", "port-change", "serial-change", "missing-border"} {
		t.Run(mode, func(t *testing.T) {
			changed := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("dependency read attempted mutation")
					w.WriteHeader(400)
					return
				}
				var rows any
				switch r.URL.Path {
				case sitesPath:
					rows = sampleSites()
				case fabricSitesPath:
					enabled := false
					rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &enabled}}
				case devicesPath:
					serial := "FOC12345678"
					if changed && mode == "serial-change" {
						serial = "FOC87654321"
					}
					rows = []Device{{ID: "device", Hostname: "border", Serial: serial}}
				case fabricDevicesPath:
					members := []fabricDeviceConfiguration{{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_2"}}}}
					if mode == "missing-border" {
						members = nil
						members = []fabricDeviceConfiguration{}
					}
					rows = members
				case "/dna/intent/api/v1/interface/network-device/device":
					port := lanLinkInterface{ID: "port", DeviceID: "device", PortName: "GigabitEthernet1/0/1", Type: "Physical", Index: "1"}
					if changed && mode == "port-change" {
						port.Mode = "trunk"
					}
					rows = []lanLinkInterface{port}
				case anycastGatewaysPath:
					vlan := 100
					if changed && mode == "vlan-change" {
						vlan = 101
					}
					rows = []map[string]any{{"id": "gateway", "fabricId": "fabric", "virtualNetworkName": "Campus", "ipPoolName": "Users", "vlanName": "Users", "vlanId": vlan, "trafficType": "DATA"}}
				case fabricL2NetworksPath:
					rows = []fabricL2Network{}
				case fabricL2HandoffsPath:
					rows = []fabricL2Handoff{}
				default:
					t.Errorf("unexpected dependency endpoint %s", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			want := fabricL2HandoffIntent{device: "border", port: "GigabitEthernet1/0/1", network: "Campus", pool: "Users", externalVLAN: 400}
			first, err := c.fabricL2HandoffDependencies(context.Background(), want, configurationDeviceReference{Name: "border"}, "Global/Lab")
			if mode == "missing-border" {
				if err == nil {
					t.Fatal("missing fabric membership accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			changed = true
			second, err := c.fabricL2HandoffDependencies(context.Background(), want, configurationDeviceReference{Name: "border"}, "Global/Lab")
			if err != nil {
				t.Fatal(err)
			}
			if (first.digest == second.digest) != (mode == "stable") {
				t.Fatal("dependency change not reflected in snapshot")
			}
			if len(first.physical) != 1 || len(first.digest) != 64 || first.binding.desired.InternalVLAN != 100 {
				t.Fatal("incomplete original snapshot")
			}
		})
	}
}
