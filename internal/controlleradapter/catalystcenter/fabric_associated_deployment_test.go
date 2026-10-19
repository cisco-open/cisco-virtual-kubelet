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

func TestFabricAssociatedManifest(t *testing.T) {
	for _, mode := range []string{"apply", "report", "lost-association-receipt", "replaced-vn", "undeclared-vn", "undeclared-parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			var root map[string]any
			json.Unmarshal([]byte(fabricL2Fixture), &root)
			fabric := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)
			fabric["l3_virtual_networks"] = []any{map[string]any{"name": "Corp"}}
			site := fabric["fabric_sites"].([]any)[0].(map[string]any)
			site["l3_virtual_networks"] = []any{"Corp"}
			site["l2_virtual_networks"].([]any)[0].(map[string]any)["associated_l3_virtual_network_name"] = "Corp"
			zone := site["fabric_zones"].([]any)[0].(map[string]any)
			zone["l3_virtual_networks"] = []any{"Corp"}
			if mode == "undeclared-vn" {
				delete(fabric, "l3_virtual_networks")
			}
			if mode == "undeclared-parent" {
				delete(site, "l3_virtual_networks")
			}
			raw, _ := json.Marshal(root)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
			if mode == "report" {
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			sites := []fabricSite{}
			zones := []fabricZone{}
			l3 := []fabricL3Network{}
			l2 := []fabricL2Network{}
			writes := []string{}
			expected := []string{"site", "zone", "definition", "site-association", "zone-association", "site-l2", "zone-l2"}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				receipt := func(kind string) {
					if len(writes) >= len(expected) || expected[len(writes)] != kind {
						t.Error("wrong dependency order", writes, kind)
					}
					writes = append(writes, kind)
					if mode == "lost-association-receipt" && kind == "site-association" {
						reply(map[string]string{})
						return
					}
					reply(map[string]string{"taskId": kind + "-task"})
				}
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					reply(sampleSites())
				case strings.HasPrefix(r.URL.Path, taskPath):
					now := time.Now().UnixMilli()
					fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, strings.TrimPrefix(r.URL.Path, taskPath), now, now)
				case r.URL.Path == fabricSitesPath && r.Method == "GET":
					reply(sites)
				case r.URL.Path == fabricZonesPath && r.Method == "GET":
					reply(zones)
				case r.URL.Path == fabricL3NetworksPath && r.Method == "GET":
					reply(l3)
				case r.URL.Path == anycastGatewaysPath && r.Method == "GET":
					reply([]any{})
				case r.URL.Path == fabricL2NetworksPath && r.Method == "GET":
					reply(l2)
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
					if len(writes) == 4 {
						kind = "zone-association"
						want = append(want, "zone")
					}
					if rows[0].ID != "network" || !reflect.DeepEqual(rows[0].Fabrics, want) {
						t.Error("incorrect association", rows)
					}
					l3 = rows
					if mode == "replaced-vn" {
						l3[0].ID = "replacement"
					}
					receipt(kind)
				case r.URL.Path == fabricL2NetworksPath && r.Method == "POST":
					var rows []fabricL2Network
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("bad L2")
						return
					}
					n := rows[0]
					kind := "site-l2"
					if n.L3 != "Corp" || len(l3) != 1 || !reflect.DeepEqual(l3[0].Fabrics, []string{"fabric", "zone"}) {
						t.Error("L2 written without L3 associations")
					}
					if n.FabricID == "fabric" {
						if n.VLAN != 0 {
							t.Error("invented VLAN")
						}
						n.VLAN = 150
						n.ID = "site-l2"
					} else {
						kind = "zone-l2"
						if n.FabricID != "zone" || n.VLAN != 150 {
							t.Error("bad zone allocation")
						}
						n.ID = "zone-l2"
					}
					l2 = append(l2, n)
					receipt(kind)
				default:
					t.Error("unexpected API", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			key := ctrlclient.ObjectKeyFromObject(config)
			for i := 0; i < 90; i++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if mode == "report" {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 7 {
					t.Fatal("bad report", config.Status)
				}
				return
			}
			if strings.HasPrefix(mode, "undeclared-") {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("dependency preflight allowed writes", writes)
				}
				return
			}
			state := journalState(t, a)
			if mode == "apply" {
				if !reflect.DeepEqual(writes, expected) || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("manifest failed", writes, config.Status.Conditions)
				}
			} else {
				if len(writes) != 4 || state.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("fence lost or replay", writes, config.Status.Conditions)
				}
				if mode == "lost-association-receipt" && state.Operation.Phase != "Unknown" {
					t.Fatal("receipt loss did not fence")
				}
			}
		})
	}
}
