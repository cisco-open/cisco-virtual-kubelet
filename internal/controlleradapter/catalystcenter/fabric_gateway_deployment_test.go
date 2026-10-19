// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestFabricGatewayManifest(t *testing.T) {
	for _, mode := range []string{"apply", "report", "lost-gateway-receipt", "replaced-parent-gateway", "missing-pool", "incomplete-payload", "secondary-apply", "secondary-old-release", "secondary-release-changed", "secondary-missing-readback", "secondary-pool-replaced", "ipam-apply", "ipam-report", "ipam-reverse-order", "ipam-lost-pool-receipt", "ipam-lost-reservation-receipt", "ipam-incomplete-payload", "ipam-wrong-owner", "ipam-owner-conflict", "ipam-secondary-apply", "ipam-secondary-report", "ipam-secondary-old-release", "ipam-secondary-release-changed", "ipam-secondary-missing-readback", "ipam-secondary-pool-replaced", "ipam-secondary-lost-receipt", "ipam-dualstack-apply", "ipam-dualstack-report", "ipam-dualstack-missing-v6", "ipam-dualstack-parent-replaced", "bulk-apply", "bulk-lost-gateway-receipt", "bulk-replaced-parent-gateway", "bulk-ipam-apply", "bulk-ipam-secondary-apply", "bulk-option-removed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			bulk := strings.HasPrefix(mode, "bulk-")
			mode = strings.TrimPrefix(mode, "bulk-")
			removeBulkOption := mode == "option-removed"
			if removeBulkOption {
				mode = "apply"
			}
			combined := strings.HasPrefix(mode, "ipam-")
			mode = strings.TrimPrefix(mode, "ipam-")
			reverse := mode == "reverse-order"
			if reverse {
				mode = "apply"
			}
			prefix := 0
			if combined {
				prefix = 2
			}
			secondary := strings.HasPrefix(mode, "secondary-")
			dual := strings.HasPrefix(mode, "dualstack-")
			if combined && dual {
				prefix = 3
			}
			if combined && secondary {
				prefix = 4
			}
			reportOnly := mode == "report" || mode == "secondary-report" || mode == "dualstack-report"
			release := "3.2.3-75346.100"
			if mode == "secondary-old-release" {
				release = "2.3.7.9"
			}
			a, config := journalFixture(t)
			if bulk {
				config.Annotations = map[string]string{anycastBulkAnnotation: "true"}
			}
			var root map[string]any
			json.Unmarshal([]byte(fabricL2Fixture), &root)
			fabric := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)
			fabric["l3_virtual_networks"] = []any{map[string]any{"name": "Corp"}}
			site := fabric["fabric_sites"].([]any)[0].(map[string]any)
			site["l3_virtual_networks"] = []any{"Corp"}
			delete(site, "l2_virtual_networks")
			gateway := map[string]any{"ip_pool_name": "access", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "layer2_flooding": false, "wireless_pool": false, "ip_directed_broadcast": false, "intra_subnet_routing_enabled": false, "multiple_ip_to_mac_addresses": false}
			if secondary {
				gateway["additional_ip_pools"] = []any{"ExtraA", "ExtraB"}
			}
			site["anycast_gateways"] = []any{gateway}
			zone := site["fabric_zones"].([]any)[0].(map[string]any)
			zone["l3_virtual_networks"] = []any{"Corp"}
			delete(zone, "l2_virtual_networks")
			zone["anycast_gateways"] = []any{"access"}
			if mode == "incomplete-payload" {
				delete(gateway, "wireless_pool")
			}
			config.Spec.ManagedSections = []string{"fabric"}
			if combined {
				var ipamRoot map[string]any
				if err := json.Unmarshal([]byte(poolModel), &ipamRoot); err != nil {
					t.Fatal(err)
				}
				ipam := ipamRoot["catalyst_center"].(map[string]any)
				destination := root["catalyst_center"].(map[string]any)
				destination["network_settings"], destination["sites"] = ipam["network_settings"], ipam["sites"]
				if secondary {
					parent := ipam["network_settings"].(map[string]any)["ip_pools"].([]any)[0].(map[string]any)
					parent["ip_pools_reservations"] = append(parent["ip_pools_reservations"].([]any),
						map[string]any{"name": "ExtraA", "type": "LAN", "subnet": "192.0.2.0", "prefix_length": 26, "gateway": "192.0.2.1"},
						map[string]any{"name": "ExtraB", "type": "LAN", "subnet": "192.0.2.64", "prefix_length": 26, "gateway": "192.0.2.65"})
					ipam["sites"].(map[string]any)["areas"].([]any)[0].(map[string]any)["ip_pools_reservations"] = []any{"access", "ExtraA", "ExtraB"}
				}
				if dual {
					settings := ipam["network_settings"].(map[string]any)
					settings["ip_pools"] = append(settings["ip_pools"].([]any), map[string]any{"name": "parent-v6", "type": "Generic", "ip_address_space": "IPv6", "ip_pool_cidr": "2001:db8::/48", "ip_pools_reservations": []any{map[string]any{"name": "access", "type": "LAN", "subnet": "2001:db8:0:1::", "prefix_length": 64, "gateway": "2001:db8:0:1::1"}}})
				}
				if mode == "wrong-owner" {
					destination["sites"] = map[string]any{"buildings": []any{map[string]any{"name": "Building", "parent_name": "Global/Lab", "ip_pools_reservations": []any{"access"}}}}
				}
				config.Spec.ManagedSections = []string{"network_settings", "fabric"}
				if reverse {
					config.Spec.ManagedSections = []string{"fabric", "network_settings"}
				}
			}
			raw, _ := json.Marshal(root)
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
			if reportOnly {
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			}

			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if mode == "owner-conflict" {
				nc, err := a.configurationRecoveryController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "network_settings", ConfigName: "other", ConfigUID: "other-uid"}}}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			sites := []fabricSite{}
			zones := []fabricZone{}
			l3 := []fabricL3Network{}
			gateways := []map[string]any{}
			writes := []string{}
			expected := []string{"site", "zone", "definition", "site-association", "zone-association", "site-gateway", "zone-gateway"}
			pools, allocated := fixturePools(), fixtureReservations()
			if combined {
				pools = []ipPool{}
				allocated = []ipReservation{}
				prerequisites := []string{"pool", "reservation"}
				if secondary {
					prerequisites = []string{"pool", "extra-a", "extra-b", "reservation"}
				}
				if dual {
					prerequisites = []string{"pool", "pool-v6", "reservation"}
				}
				expected = append(prerequisites, expected...)
			}
			// Public IPAM creates now correlate the canonical NCIP result UUID with
			// native readback. Keep logical write-order labels separate from native IDs.
			ipamIDs := map[string]string{"pool": ipamTestPoolID, "pool-v6": "44444444-4444-4444-8444-444444444444", "reservation": ipamTestReservationID, "extra-a": "55555555-5555-4555-8555-555555555555", "extra-b": "66666666-6666-4666-8666-666666666666"}
			nativeIPAMID := func(kind string) string {
				if combined {
					return ipamIDs[kind]
				}
				return kind
			}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				receipt := func(kind string) {
					if len(writes) >= len(expected) || expected[len(writes)] != kind {
						t.Error("wrong dependency order", writes, kind)
					}
					writes = append(writes, kind)
					if mode == "lost-gateway-receipt" && kind == "site-gateway" || mode == "lost-pool-receipt" && kind == "pool" || mode == "lost-reservation-receipt" && kind == "reservation" || mode == "secondary-lost-receipt" && kind == "extra-a" {
						reply(map[string]string{})
						return
					}
					reply(map[string]string{"taskId": kind + "-task"})
				}
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == applianceReleasePath:
					reply(map[string]string{"installedVersion": release})
				case r.URL.Path == sitesPath:
					reply(sampleSites())
				case strings.HasPrefix(r.URL.Path, taskPath):
					now := time.Now().UnixMilli()
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
					task := map[string]any{"id": id, "startTime": now, "endTime": now, "isError": false}
					if result := ipamIDs[strings.TrimSuffix(id, "-task")]; combined && result != "" {
						task["serviceType"] = "NCIP"
						task["progress"] = result
					}
					writeDefinitionTaskFixture(w, r, task)
				case r.URL.Path == fabricSitesPath && r.Method == "GET":
					reply(sites)
				case r.URL.Path == fabricZonesPath && r.Method == "GET":
					reply(zones)
				case r.URL.Path == fabricL3NetworksPath && r.Method == "GET":
					reply(l3)
				case r.URL.Path == fabricL2NetworksPath && r.Method == "GET":
					reply([]fabricL2Network{})
				case r.URL.Path == anycastGatewaysPath && r.Method == "GET":
					reply(gateways)
				case combined && r.URL.Path == globalPoolsPath && r.Method == "POST":
					var pool ipPool
					if err := json.NewDecoder(r.Body).Decode(&pool); err != nil {
						t.Fatal(err)
					}
					kind := "pool"
					if pool.Name == "parent-v6" {
						kind = "pool-v6"
					}
					pool.ID = nativeIPAMID(kind)
					pools = append(pools, pool)
					receipt(kind)
				case combined && r.URL.Path == sitePoolsPath && r.Method == "POST":
					var reservation ipReservation
					if err := json.NewDecoder(r.Body).Decode(&reservation); err != nil {
						t.Fatal(err)
					}
					if reservation.SiteID != "area" || reservation.V4 == nil || reservation.V4.GlobalPoolID != nativeIPAMID("pool") {
						t.Error("unresolved reservation dependency")
					}
					kind := "reservation"
					if reservation.Name == "ExtraA" {
						kind = "extra-a"
					}
					if reservation.Name == "ExtraB" {
						kind = "extra-b"
					}
					if dual && (reservation.V6 == nil || reservation.V6.GlobalPoolID != nativeIPAMID("pool-v6") || reservation.V6.SLAAC == nil || !*reservation.V6.SLAAC) {
						t.Error("dual-stack reservation lost IPv6 parent or SLAAC")
					}
					if mode == "dualstack-missing-v6" {
						reservation.V6 = nil
					}
					reservation.ID = nativeIPAMID(kind)
					allocated = append(allocated, reservation)
					receipt(kind)
				case r.URL.Path == globalPoolsPath:
					observed := append([]ipPool{}, pools...)
					if mode == "dualstack-parent-replaced" && len(writes) >= 6+prefix {
						for i := range observed {
							if observed[i].Name == "parent-v6" {
								observed[i].ID = "replacement-v6"
							}
						}
					}
					reply(observed)
				case r.URL.Path == sitePoolsPath:
					rows := allocated
					if secondary && !combined {
						bits := 26
						ga, gb := "192.0.2.1", "192.0.2.65"
						rows = append(rows, ipReservation{ID: "extra-a", Name: "ExtraA", Type: "LAN", SiteID: "area", V4: &poolAddressSpace{Subnet: "192.0.2.0", PrefixLength: &bits, Gateway: &ga, GlobalPoolID: "pool"}}, ipReservation{ID: "extra-b", Name: "ExtraB", Type: "LAN", SiteID: "area", V4: &poolAddressSpace{Subnet: "192.0.2.64", PrefixLength: &bits, Gateway: &gb, GlobalPoolID: "pool"}})

					}
					if mode == "secondary-pool-replaced" && len(writes) >= 6+prefix {
						rows = append([]ipReservation{}, rows...)
						for i := range rows {
							if rows[i].Name == "ExtraA" {
								rows[i].ID = "replacement"
							}
						}
					}
					if mode == "dualstack-parent-replaced" && len(writes) >= 6+prefix {
						rows = append([]ipReservation{}, rows...)
						for i := range rows {
							if rows[i].V6 != nil {
								copy := *rows[i].V6
								copy.GlobalPoolID = "replacement-v6"
								rows[i].V6 = &copy
							}
						}
					}
					if mode == "missing-pool" {
						rows = []ipReservation{}
					}
					reply(rows)
				case r.URL.Path == fabricSitesPath && r.Method == "POST":
					if json.NewDecoder(r.Body).Decode(&sites) != nil || len(sites) != 1 {
						t.Error("bad site")
						return
					}
					sites[0].ID = "fabric"
					receipt("site")
				case r.URL.Path == fabricZonesPath && r.Method == "POST":
					if json.NewDecoder(r.Body).Decode(&zones) != nil || len(zones) != 1 {
						t.Error("bad zone")
						return
					}
					zones[0].ID = "zone"
					receipt("zone")
				case r.URL.Path == fabricL3NetworksPath && r.Method == "POST":
					if json.NewDecoder(r.Body).Decode(&l3) != nil || len(l3) != 1 {
						t.Error("bad definition")
						return
					}
					l3[0].ID = "network"
					l3[0].Fabrics = []string{}
					receipt("definition")
				case r.URL.Path == fabricL3NetworksPath && r.Method == "PUT":
					var rows []fabricL3Network
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("bad association")
						return
					}
					kind := "site-association"
					want := []string{"fabric"}
					if len(writes) == 4+prefix {
						kind = "zone-association"
						want = append(want, "zone")
					}
					if rows[0].ID != "network" || !reflect.DeepEqual(rows[0].Fabrics, want) {
						t.Error("incorrect association", rows)
					}
					l3 = rows
					receipt(kind)
				case r.URL.Path == anycastGatewaysPath && r.Method == "POST":
					var rows []map[string]any
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("bad gateway body")
						return
					}
					n := rows[0]
					kind := "site-gateway"
					if len(l3) != 1 || !reflect.DeepEqual(l3[0].Fabrics, []string{"fabric", "zone"}) {
						t.Error("gateway before memberships")
					}
					if n["fabricId"] == "fabric" {
						if n["vlanId"] != nil {
							t.Error("invented parent VLAN")
						}
					} else {
						kind = "zone-gateway"
						if n["fabricId"] != "zone" || n["vlanId"] != float64(150) {
							t.Error("wrong inherited gateway VLAN")
						}
					}
					if secondary {
						ordered := []any{map[string]any{"name": "ExtraA", "order": float64(2)}, map[string]any{"name": "ExtraB", "order": float64(3)}}
						if !reflect.DeepEqual(n["additionalIpPools"], ordered) {
							t.Error("secondary order lost", n["additionalIpPools"])
						}
						n["additionalIpPools"] = []any{ordered[1], ordered[0]}
						if mode == "secondary-missing-readback" {
							delete(n, "additionalIpPools")
						}
						if mode == "secondary-release-changed" && kind == "site-gateway" {
							release = "3.2.4-80000.100"
						}
					}
					n["id"] = kind
					n["vlanId"] = 150
					n["vlanName"] = "192.0.2.128/25-Corp"
					delete(n, "autoGenerateVlanName")
					gateways = append(gateways, n)
					if mode == "replaced-parent-gateway" && kind == "zone-gateway" {
						gateways[0]["id"] = "replacement"
					}
					receipt(kind)
				default:
					t.Error("unexpected API", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			key := ctrlclient.ObjectKeyFromObject(config)
			for i := 0; i < 90; i++ {
				if removeBulkOption {
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Accepted" && strings.Contains(current.Status.Configuration.Operation.Resource, anycastBulkPrefix) {
						var fresh configapi.NetworkControllerConfig
						if err := a.statusReader.Get(ctx, key, &fresh); err != nil {
							t.Fatal(err)
						}
						fresh.Annotations = nil
						if err := a.statusWriter.Update(ctx, &fresh); err != nil {
							t.Fatal(err)
						}
						removeBulkOption = false
					}
				}

				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if reportOnly {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(config.Status.SectionStatus) != len(config.Spec.ManagedSections) {
					t.Fatal("bad report", config.Status)
				}
				for _, section := range config.Status.SectionStatus {
					count := int64(7)
					if section.Name == "network_settings" {
						count = int64(prefix)
					}
					if section.ResourceCount != count {
						t.Fatal("wrong section resource count", section)
					}
				}
				return
			}
			if mode == "missing-pool" || mode == "incomplete-payload" || mode == "secondary-old-release" || mode == "wrong-owner" || mode == "owner-conflict" {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("dependency preflight allowed writes", writes)
				}
				return
			}
			state := journalState(t, a)
			if mode == "apply" || mode == "secondary-apply" || mode == "dualstack-apply" {
				if !reflect.DeepEqual(writes, expected) || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("manifest failed", writes, config.Status.Conditions)
				}
				if combined {
					for _, section := range []string{"network_settings", "fabric"} {
						owned := false
						for _, owner := range state.Owners {
							if owner.Section == section && owner.ConfigUID == string(config.UID) {
								owned = true
							}
						}
						if !owned {
							t.Fatal("combined section ownership missing", section)
						}
					}
				}
			} else {
				expectedWrites := 7 + prefix
				if mode == "lost-gateway-receipt" || secondary {
					expectedWrites = 6 + prefix
				}
				if mode == "lost-pool-receipt" {
					expectedWrites = 1
				}
				if mode == "lost-reservation-receipt" {
					expectedWrites = 2
				}
				if mode == "secondary-lost-receipt" {
					expectedWrites = 2
				}
				if mode == "dualstack-missing-v6" {
					expectedWrites = 3
				}
				if mode == "dualstack-parent-replaced" {
					expectedWrites = 6 + prefix
				}
				if len(writes) != expectedWrites || state.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("fence lost or replay", writes, config.Status.Conditions)
				}
				if (strings.HasPrefix(mode, "lost-") || mode == "secondary-lost-receipt") && state.Operation.Phase != "Unknown" {
					t.Fatal("receipt loss did not fence")
				}
			}
		})
	}
}
