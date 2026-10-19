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
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

const fabricSiteZoneFixture = `{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","authentication_template":{"name":"Closed Authentication"},"pub_sub_enabled":false,"fabric_zones":[{"name":"Global/Lab/Building","authentication_template":{"name":"No Authentication"}}]}]}}}`

func TestFabricSiteZoneManifest(t *testing.T) {
	for _, mode := range []string{"apply", "report", "lost-site-receipt", "lost-zone-receipt", "replaced-parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(fabricSiteZoneFixture)}
			if mode == "report" {
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			sites := []fabricSite{}
			zones := []fabricZone{}
			writes := []string{}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case strings.HasPrefix(r.URL.Path, taskPath):
					now := time.Now().UnixMilli()
					fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, strings.TrimPrefix(r.URL.Path, taskPath), now, now)
				case r.URL.Path == fabricSitesPath && r.Method == "GET":
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case r.URL.Path == fabricZonesPath && r.Method == "GET":
					json.NewEncoder(w).Encode(map[string]any{"response": zones})
				case r.URL.Path == fabricSitesPath && r.Method == "POST":
					var rows []fabricSite
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("bad site request")
						return
					}
					rows[0].ID = "fabric"
					sites = rows
					writes = append(writes, "site")
					if mode == "lost-site-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"site-task"}}`)
					}
				case r.URL.Path == fabricZonesPath && r.Method == "POST":
					if len(sites) != 1 || len(writes) != 1 || writes[0] != "site" {
						t.Error("zone written before parent completed")
					}
					var rows []fabricZone
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 || rows[0].SiteID != "building" || rows[0].Authentication != "No Authentication" {
						t.Error("bad zone request")
						return
					}
					rows[0].ID = "zone"
					zones = rows
					writes = append(writes, "zone")
					if mode == "replaced-parent" {
						sites[0].ID = "replacement"
					}
					if mode == "lost-zone-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"zone-task"}}`)
					}
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			key := ctrlclient.ObjectKeyFromObject(config)
			for i := 0; i < 30; i++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if mode == "report" {
				if len(writes) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 2 {
					t.Fatal("incorrect report", config.Status)
				}
				return
			}
			state := journalState(t, a)
			if mode == "apply" {
				if len(writes) != 2 || writes[0] != "site" || writes[1] != "zone" || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil {
					t.Fatal("manifest failed or replayed", config.Status.Conditions)
				}
			} else {
				expected := 2
				if mode == "lost-site-receipt" {
					expected = 1
				}
				if len(writes) != expected || state.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("lost fence or replayed write")
				}
				if strings.HasPrefix(mode, "lost-") && state.Operation.Phase != "Unknown" {
					t.Fatal("inferred completion without receipt")
				}
			}
		})
	}
}
