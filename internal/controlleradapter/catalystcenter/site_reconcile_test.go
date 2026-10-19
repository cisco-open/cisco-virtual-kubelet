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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSitesApplyManifestCompletesHierarchyAcrossWorkerRestarts(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	// Inline CR source is JSON, as stored by the Kubernetes API.
	desired, _ := parseSites([]byte(executionSiteModel))
	groups := map[string][]map[string]any{}
	for _, d := range desired {
		item := map[string]any{"name": lastSiteName(d.path), "parent_name": parentSitePath(d.path)}
		for k, v := range d.fields {
			item[k] = v
		}
		groups[d.kind+"s"] = append(groups[d.kind+"s"], item)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": groups}})
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	actual := sampleSites()[:1]
	tasks := map[string]int64{}
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == sitesPath {
			json.NewEncoder(w).Encode(map[string]any{"response": actual})
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath) {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := tasks[id]
			if !ok {
				t.Errorf("unknown task requested")
				w.WriteHeader(404)
				return
			}
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false, "data": strings.Replace(id, "task-", "00000000-0000-4000-8000-00000000000", 1)})
			return
		}
		if r.Method != "POST" {
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var site Site
		if err := json.NewDecoder(r.Body).Decode(&site); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		for _, s := range actual {
			if s.ID == site.ParentID {
				site.NameHierarchy = s.NameHierarchy + "/" + site.Name
			}
		}
		if site.NameHierarchy == "" {
			t.Error("child submitted before its parent was observed")
			w.WriteHeader(400)
			return
		}
		kinds := map[string]string{"/dna/intent/api/v1/areas": "area", "/dna/intent/api/v2/buildings": "building", "/dna/intent/api/v2/floors": "floor"}
		site.Type = kinds[r.URL.Path]
		if site.Type == "" {
			t.Error("unknown site endpoint")
			w.WriteHeader(400)
			return
		}
		writes++
		site.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", writes)
		actual = append(actual, site)
		id := fmt.Sprintf("task-%d", writes)
		tasks[id] = time.Now().UnixMilli()
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
	})
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	var current configapi.NetworkControllerConfig
	for step := 0; step < 30; step++ {
		restarted := *a
		r := &configReconciler{a: &restarted}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
			t.Fatal(err)
		}
		if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if current.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || current.Status.LastAppliedTime == nil {
		t.Fatalf("manifest did not converge: %+v journal=%+v", current.Status, journalState(t, a))
	}
	mu.Lock()
	count := writes
	mu.Unlock()
	if count != 3 {
		t.Fatalf("writes=%d want exactly three", count)
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.Owners) != 1 {
		t.Fatalf("unexpected final journal %+v", state)
	}
	// An additional reconciliation verifies without issuing another write.
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 3 {
		t.Fatal("converged intent replayed mutation")
	}
}

func TestSitesApplyUncertainReceiptCannotReplay(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()[:1]})
			return
		}
		writes++
		w.WriteHeader(202) // Accepted with no receipt: outcome is uncertain.
	})
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	for range 5 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 || journalState(t, a).Operation.Phase != "Unknown" {
		t.Fatalf("writes=%d journal=%+v", writes, journalState(t, a))
	}
	var current configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || current.Status.LastAppliedTime != nil {
		t.Fatal("uncertain mutation claimed success")
	}
}

func TestSitesApplyTaskTimeoutRetainsJournal(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.TaskTimeout = &metav1.Duration{Duration: time.Minute}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "Global/Lab", "Create", configurationResourceIdentity{parent: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "task-1"); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	nc.Status.Configuration.Operation.SubmittedAt = &old
	if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	// No API client is installed: timeout must be handled before any request.
	if _, err = (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
		t.Fatal(err)
	}
	if journalState(t, a).Operation.Phase != "Unknown" {
		t.Fatal("timed-out operation was not fenced")
	}
	var current configapi.NetworkControllerConfig
	if err = a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || current.Status.SectionStatus[0].State != configapi.NetworkControllerSectionStateApplyError {
		t.Fatalf("timeout not surfaced: %+v", current.Status)
	}
}
