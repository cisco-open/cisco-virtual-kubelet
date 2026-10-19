// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const fabricSiteFixture = `{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","authentication_template":{"name":"Closed Authentication"},"pub_sub_enabled":false}]}}}`

func TestFabricSiteManifestLifecycle(t *testing.T) {
	for _, mode := range []string{"create", "ambiguous", "drift", "replaced-hierarchy", "partial"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(fabricSiteFixture)}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			rows := []fabricSite{}
			writes := 0
			pubsub := false
			if mode == "drift" {
				pubsub = true
				rows = append(rows, fabricSite{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &pubsub})
			}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					sites := sampleSites()
					if mode == "replaced-hierarchy" && writes > 0 {
						sites[1].ID = "new-area"
						sites[2].ParentID = "new-area"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case strings.HasPrefix(r.URL.Path, taskPath):
					now := time.Now().UnixMilli()
					fmt.Fprintf(w, `{"response":{"id":%q,"startTime":%d,"endTime":%d,"isError":false}}`, strings.TrimPrefix(r.URL.Path, taskPath), now, now)
				case r.URL.Path == fabricSitesPath && r.Method == "GET":
					if mode == "partial" {
						w.WriteHeader(206)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case r.URL.Path == fabricSitesPath && r.Method == "POST":
					writes++
					var batch []fabricSite
					if json.NewDecoder(r.Body).Decode(&batch) != nil || len(batch) != 1 || batch[0].SiteID != "area" || batch[0].ID != "" || batch[0].PubSub == nil || *batch[0].PubSub || batch[0].Authentication != "Closed Authentication" {
						t.Error("incorrect creation payload")
						w.WriteHeader(400)
						return
					}
					batch[0].ID = "fabric"
					rows = batch
					if mode == "ambiguous" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"fabric-task"}}`)
					}
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			key := ctrlclient.ObjectKeyFromObject(config)
			for i := 0; i < 20; i++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if mode == "create" {
				if writes != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil {
					t.Fatal("fabric creation failed or replayed", config.Status.Conditions)
				}
			} else {
				if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("unsafe convergence")
				}
				if mode == "drift" || mode == "partial" {
					if writes != 0 {
						t.Fatal("failed preflight mutated")
					}
				} else if writes != 1 || state.Operation == nil {
					t.Fatal("uncertain/replaced operation lost fence")
				}
				if mode == "ambiguous" && state.Operation.Phase != "Unknown" {
					t.Fatal("ambiguous receipt inferred success")
				}
			}
		})
	}
}
func TestFabricSiteModelValidation(t *testing.T) {
	for _, replacement := range [][2]string{{`"pub_sub_enabled":false`, `"pub_sub_enabled":null`}, {`"Closed Authentication"`, `"unknown"`}, {`"Global/Lab"`, `"Global"`}, {`"pub_sub_enabled":false`, `"pub_sub_enabled":false,"reconfigure":true`}, {`"pub_sub_enabled":false`, `"pub_sub_enabled":false,"fabric_zones":null`}, {`"name":"Closed Authentication"`, `"name":"Closed Authentication","unknown":true`}} {
		if _, err := (&client{}).compileFabricConfiguration([]byte(strings.Replace(fabricSiteFixture, replacement[0], replacement[1], 1))); err == nil {
			t.Fatalf("unsupported model accepted: %s", replacement[1])
		}
	}
}
func TestFabricSiteObservationRejectsIncomplete(t *testing.T) {
	for _, raw := range []string{`{"response":[{"id":"fabric","siteId":"area","authenticationProfileName":"Closed Authentication"}]}`, `{"response":null}`, `{"response":[{"id":"one","siteId":"area","authenticationProfileName":"Closed Authentication","isPubSubEnabled":false},{"id":"two","siteId":"area","authenticationProfileName":"Closed Authentication","isPubSubEnabled":false}]}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			fmt.Fprint(w, raw)
		})
		if got, err := c.fabricSites(context.Background()); err == nil || got != nil {
			t.Fatal("incomplete or duplicate inventory accepted")
		}
	}
}
