// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

const fabricL2Fixture = `{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","authentication_template":{"name":"Closed Authentication"},"pub_sub_enabled":false,"l2_virtual_networks":[{"name":"clients","vlan_name":"Clients","traffic_type":"DATA","fabric_enabled_wireless":false}],"fabric_zones":[{"name":"Global/Lab/Building","authentication_template":{"name":"No Authentication"},"l2_virtual_networks":["clients"]}]}]}}}`

func TestFabricL2Manifest(t *testing.T) {
	for _, mode := range []string{"apply", "report", "lost-parent-receipt", "lost-zone-receipt", "replaced-parent-network", "replaced-fabric", "wrong-zone-vlan", "conflicting-existing", "gateway-zone-conflict"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(fabricL2Fixture)}
			if mode == "report" {
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			sites := []fabricSite{}
			zones := []fabricZone{}
			networks := []fabricL2Network{}
			writes := []string{}
			wireless := false
			if mode == "conflicting-existing" {
				sites = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &wireless}}
				networks = []fabricL2Network{{ID: "old-network", FabricID: "fabric", VLANName: "Clients", VLAN: 150, Traffic: "VOICE", Wireless: &wireless}}
			}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				response := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					response(sampleSites())
				case strings.HasPrefix(r.URL.Path, taskPath):
					now := time.Now().UnixMilli()
					fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, strings.TrimPrefix(r.URL.Path, taskPath), now, now)
				case r.URL.Path == fabricSitesPath && r.Method == "GET":
					response(sites)
				case r.URL.Path == fabricZonesPath && r.Method == "GET":
					response(zones)
				case r.URL.Path == anycastGatewaysPath && r.Method == "GET":
					gateways := []any{}
					if mode == "gateway-zone-conflict" && len(writes) >= 3 {
						gateway := anycastObservationFixture()
						gateway["fabricId"] = "zone"
						gateway["vlanName"] = "Unrelated"
						gateway["vlanId"] = 150
						gateways = append(gateways, gateway)
					}
					response(gateways)
				case r.URL.Path == fabricL2NetworksPath && r.Method == "GET":
					response(networks)
				case r.URL.Path == fabricSitesPath && r.Method == "POST":
					if len(writes) != 0 {
						t.Error("replayed site")
					}
					if json.NewDecoder(r.Body).Decode(&sites) != nil || len(sites) != 1 {
						t.Error("invalid site")
						w.WriteHeader(400)
						return
					}
					sites[0].ID = "fabric"
					writes = append(writes, "site")
					response(map[string]string{"taskId": "site-task"})
				case r.URL.Path == fabricZonesPath && r.Method == "POST":
					if !reflect.DeepEqual(writes, []string{"site"}) {
						t.Error("zone out of order")
					}
					if json.NewDecoder(r.Body).Decode(&zones) != nil || len(zones) != 1 {
						t.Error("invalid zone")
						w.WriteHeader(400)
						return
					}
					zones[0].ID = "zone"
					writes = append(writes, "zone")
					response(map[string]string{"taskId": "zone-task"})
				case r.URL.Path == fabricL2NetworksPath && r.Method == "POST":
					var rows []fabricL2Network
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("invalid network")
						w.WriteHeader(400)
						return
					}
					n := rows[0]
					if n.ID != "" || n.VLANName != "Clients" || n.Traffic != "DATA" || n.Wireless == nil || *n.Wireless {
						t.Error("incorrect payload")
					}
					kind := "parent-network"
					if n.FabricID == "fabric" {
						if !reflect.DeepEqual(writes, []string{"site", "zone"}) || n.VLAN != 0 {
							t.Error("parent creation out of order or invented VLAN")
						}
						n.ID = "parent-network"
						n.VLAN = 150
					} else if n.FabricID == "zone" {
						if !reflect.DeepEqual(writes, []string{"site", "zone", "parent-network"}) || n.VLAN != 150 {
							t.Error("zone did not inherit allocated VLAN")
						}
						n.ID = "zone-network"
						kind = "zone-network"
						if mode == "replaced-parent-network" {
							networks[0].ID = "replacement"
						}
						if mode == "wrong-zone-vlan" {
							n.VLAN = 151
						}
					} else {
						t.Error("wrong fabric target")
					}
					networks = append(networks, n)
					writes = append(writes, kind)
					if mode == "replaced-fabric" {
						sites[0].ID = "replacement"
					}
					if mode == "lost-parent-receipt" && kind == "parent-network" || mode == "lost-zone-receipt" && kind == "zone-network" {
						response(map[string]string{})
					} else {
						response(map[string]string{"taskId": kind + "-task"})
					}
				default:
					t.Error("unexpected API", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			key := ctrlclient.ObjectKeyFromObject(config)
			for i := 0; i < 50; i++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if mode == "report" {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 4 {
					t.Fatal("incorrect report", config.Status)
				}
				return
			}
			if mode == "conflicting-existing" {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("child conflict did not block zone creation")
				}
				return
			}
			state := journalState(t, a)
			if mode == "apply" {
				if !reflect.DeepEqual(writes, []string{"site", "zone", "parent-network", "zone-network"}) || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil {
					t.Fatal("did not converge or replayed", writes, config.Status.Conditions)
				}
			} else {
				expected := 4
				if mode == "lost-parent-receipt" || mode == "replaced-fabric" || mode == "gateway-zone-conflict" {
					expected = 3
				}
				if len(writes) != expected || state.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("lost fence/replayed", writes, config.Status.Conditions)
				}
				if strings.HasPrefix(mode, "lost-") && state.Operation.Phase != "Unknown" {
					t.Fatal("inferred completion without task receipt")
				}
			}
		})
	}
}
