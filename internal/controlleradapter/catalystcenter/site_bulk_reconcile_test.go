// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func bulkSiteManifest(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, desired []desiredSite) {
	t.Helper()
	raw, err := siteManifestModel(desired)
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	config.Annotations = map[string]string{siteBulkAnnotation: "true"}
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
}

func siteManifestModel(desired []desiredSite) ([]byte, error) {
	groups := map[string][]map[string]any{}
	for _, site := range desired {
		item := map[string]any{"name": lastSiteName(site.path), "parent_name": parentSitePath(site.path)}
		for key, value := range site.fields {
			item[key] = value
		}
		groups[site.kind+"s"] = append(groups[site.kind+"s"], item)
	}
	return json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": groups}})
}

func TestSiteBulkManifestRestartAndUncertainFencing(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "task-failed", "lost-receipt", "source-changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			desired, err := parseSites([]byte(executionSiteModel))
			if err != nil {
				t.Fatal(err)
			}
			bulkSiteManifest(t, a, config, desired)
			var mu sync.Mutex
			actual := sampleSites()[:1]
			writes, treeReads := 0, 0
			stamp := int64(0)
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.Method == http.MethodGet && r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": actual})
				case r.Method == http.MethodGet && r.URL.Path == taskPath+"bulk-task/tree":
					treeReads++
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": "bulk-task", "rootId": "bulk-task", "startTime": stamp, "endTime": stamp, "isError": mode == "task-failed"}}})
				case r.Method == http.MethodGet && r.URL.Path == taskPath+"bulk-task":
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "bulk-task", "startTime": stamp, "endTime": stamp, "isError": false}})
				case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/dna/intent/api/v2/floors/"):
					writes++
					stamp = time.Now().UnixMilli()
					var updated Site
					if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
						t.Error(err)
					}
					id := strings.TrimPrefix(r.URL.Path, "/dna/intent/api/v2/floors/")
					found := false
					for i, site := range actual {
						if site.ID == id && site.ParentID == updated.ParentID {
							updated.ID, updated.Type, updated.NameHierarchy = site.ID, site.Type, site.NameHierarchy
							actual[i] = updated
							found = true
						}
					}
					if !found {
						t.Error("update lost floor identity")
					}
					fmt.Fprint(w, `{"response":{"taskId":"bulk-task"}}`)
				case r.Method == http.MethodPost && r.URL.Path == siteBulkPath:
					writes++
					stamp = time.Now().UnixMilli()
					var rows []map[string]any
					if err := json.NewDecoder(r.Body).Decode(&rows); err != nil || len(rows) != 3 {
						t.Error("wrong bulk payload", err)
					}
					for i, row := range rows {
						if i > 0 && (mode == "partial" || mode == "task-failed" || mode == "lost-receipt") {
							break
						}
						encoded, _ := json.Marshal(row)
						var site Site
						json.Unmarshal(encoded, &site)
						parent, _ := row["parentNameHierarchy"].(string)
						for _, existing := range actual {
							if existing.NameHierarchy == parent {
								site.ParentID = existing.ID
							}
						}
						if site.ParentID == "" {
							t.Error("batch parent missing")
						}
						site.ID, site.NameHierarchy = fmt.Sprintf("bulk-native-%d", i), parent+"/"+site.Name
						actual = append(actual, site)
					}
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"bulk-task"}}`)
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
				}
			})
			req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
			var current configapi.NetworkControllerConfig
			for step := 0; step < 12; step++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
				if mode == "source-changed" && step == 0 {
					if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
						t.Fatal(err)
					}
					config.Generation++
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if writes != 1 || len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(config.UID) {
				t.Fatalf("write count/ownership changed: writes=%d state=%+v", writes, state)
			}
			if mode == "complete" {
				if current.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil || treeReads == 0 {
					t.Fatalf("manifest failed convergence: status=%+v state=%+v", current.Status, state)
				}
				// Existing divergent members use the ordinary UUID-bound update
				// executor even while bulk create remains selected.
				if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
					t.Fatal(err)
				}
				config.Generation++
				for i := range desired {
					if desired[i].kind == "floor" {
						desired[i].fields["width"] = 12.654321
					}
				}
				bulkSiteManifest(t, a, config, desired)
				for i := 0; i < 10; i++ {
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
					t.Fatal(err)
				}
				if writes != 2 || current.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation != nil {
					t.Fatalf("ordinary update failed or bulk replayed: writes=%d status=%+v", writes, current.Status)
				}
				return
			}
			if state.Operation == nil || !strings.HasPrefix(state.Operation.Resource, siteBulkResourcePrefix) || current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("uncertain/partial operation released")
			}
			expected := map[string]string{"partial": "Verifying", "task-failed": "Accepted", "lost-receipt": "Unknown", "source-changed": "Accepted"}[mode]
			if state.Operation.Phase != expected {
				t.Fatalf("phase=%s expected=%s", state.Operation.Phase, expected)
			}
			if mode == "partial" {
				// Simulate eventual completion without granting a new mutation.
				retained := state.Operation.Resource
				if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
					t.Fatal(err)
				}
				config.Annotations[siteBulkAnnotation] = "false"
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				for i := 0; i < 2; i++ {
					mutation, err := nextSiteMutation(desired, actual)
					if err != nil || mutation == nil {
						t.Fatal("late native convergence", err)
					}
					var site Site
					json.Unmarshal(mutation.body, &site)
					site.ID, site.NameHierarchy, site.Type = fmt.Sprintf("late-%d", i), mutation.desired.path, mutation.desired.kind
					actual = append(actual, site)
				}
				mu.Unlock()
				binding, err := recoverSiteBulkBinding(desired, retained)
				if err != nil || !binding.selected(0) || !binding.selected(1) || !binding.selected(2) {
					t.Fatal("partial result lost original selection")
				}
				for i := 0; i < 6; i++ {
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
						t.Fatal(err)
					}
				}
				if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
					t.Fatal(err)
				}
				if current.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 1 || journalState(t, a).Operation != nil {
					t.Fatal("late completion failed or batch replayed")
				}
			}
		})
	}
}

func TestSiteBulkSubmissionRequiresCurrentOwnedJournal(t *testing.T) {
	for _, mode := range []string{"unprepared", "prepared", "owner-lost", "source-changed", "wrong-parent", "controller-paused"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			desired, _ := parseSites([]byte(executionSiteModel))
			bulkSiteManifest(t, a, config, desired)
			writes := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
				} else if r.Method == http.MethodGet && r.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()[:1]})
				} else {
					writes++
					t.Error("unauthorized native submission")
					fmt.Fprint(w, `{"response":{"taskId":"unexpected"}}`)
				}
			})
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := a.compileSiteImageApply(nc, config, source)
			if err != nil {
				t.Fatal(err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if mode != "unprepared" {
				op, err := a.prepareConfiguration(ctx, config, source, "sites", write.resource, write.action, write.identity)
				if err != nil {
					t.Fatal(err)
				}
				if mode != "prepared" {
					if err := a.startConfiguration(ctx, config, source, op.ID, write.identity); err != nil {
						t.Fatal(err)
					}
				}
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "owner-lost":
				nc.Status.Configuration.Owners[0].ConfigUID = "other-owner"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "wrong-parent":
				nc.Status.Configuration.Operation.ParentID = "other-root"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "source-changed":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "controller-paused":
				nc.Spec.Paused = true
				if err := a.statusWriter.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := write.submit(ctx); err == nil || writes != 0 {
				t.Fatal("bulk submission escaped journal/source/owner fence")
			}
		})
	}
}
