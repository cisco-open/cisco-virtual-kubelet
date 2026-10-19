// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var siteAppliedModes = []string{"complete", "recover", "pending-child", "failed-child", "foreign-child", "missing-result", "foreign-result", "lost-ack", "source-change", "owner-change", "preflight-native", "post-applied-drift"}

func TestSitePrivateAppliedPublic(t *testing.T) {
	for _, stage := range []string{"building-create", "floor-create"} {
		for _, mode := range siteAppliedModes {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseSiteAppliedPublic(t, stage, mode, nil) })
		}
	}
}

func exerciseSiteAppliedPublic(t *testing.T, stage, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	desired, actual := siteDispatchFixture(stage)
	config.Spec.ManagedSections = []string{"sites"}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "private-applied-source", Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	configureSiteNativeComparisonFixture(t, a, config)
	if install != nil {
		install(a, config)
	}
	model := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "private-applied-source", Namespace: config.Namespace}}
	if install == nil {
		model.UID = "applied-source"
	}
	writeModel := func(create bool) {
		item := map[string]any{"name": lastSiteName(desired[0].path), "parent_name": parentSitePath(desired[0].path)}
		for key, value := range desired[0].fields {
			item[key] = value
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{desired[0].kind + "s": []any{item}}}})
		if !create {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
				t.Fatal(err)
			}
		}
		model.Data = map[string]string{"model.yaml": string(raw)}
		var err error
		if create {
			err = a.statusWriter.Create(ctx, model)
		} else {
			err = a.statusWriter.Update(ctx, model)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	writeModel(true)
	var mu sync.Mutex
	writes, updateReads := 0, 0
	stamps := map[string]int64{}
	opaqueChanged, injected, sawRefusal := false, false, false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == sitesPath {
			if mode == "preflight-native" && !injected {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Action == "Update" && nc.Status.Configuration.Operation.Phase == "Submitting" {
					opaqueChanged, injected = true, true
				}
			}
			raw, _ := json.Marshal(actual)
			var rows []map[string]any
			json.Unmarshal(raw, &rows)
			for _, row := range rows {
				if row["nameHierarchy"] == desired[0].path {
					value := "private-original"
					if opaqueChanged {
						value = "private-foreign"
					}
					row["opaqueNative"] = value
					row["counter"] = json.Number("9007199254740993")
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath) {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := stamps[id]
			if !ok {
				t.Error("foreign task requested")
				w.WriteHeader(404)
				return
			}
			if id != "task-1" {
				updateReads++
			}
			root := map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false, "data": siteOwnedTestUUID}
			child := map[string]any{"id": id + "-child", "parentId": id, "rootId": id, "startTime": stamp, "endTime": stamp, "isError": false}
			if id == "task-2" {
				switch mode {
				case "pending-child":
					child["endTime"] = 0
				case "failed-child":
					child["isError"] = true
				case "foreign-child":
					child["rootId"] = "foreign"
				case "missing-result":
					root["data"] = ""
				case "foreign-result":
					root["data"] = "22222222-2222-4222-8222-222222222222"
				}
			}
			writeDefinitionTaskFixture(w, r, root, child)
			return
		}
		if r.Method == "POST" || r.Method == "PUT" {
			writes++
			var site Site
			if json.NewDecoder(r.Body).Decode(&site) != nil {
				t.Error("bad native body")
			}
			site.ID, site.NameHierarchy, site.Type = siteOwnedTestUUID, desired[0].path, desired[0].kind
			found := false
			for index, old := range actual {
				if old.NameHierarchy == site.NameHierarchy {
					actual[index] = site
					found = true
				}
			}
			if !found {
				actual = append(actual, site)
			}
			id := fmt.Sprint("task-", writes)
			stamps[id] = time.Now().UnixMilli()
			if writes > 1 && mode == "lost-ack" {
				fmt.Fprint(w, `{}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
			}
			return
		}
		t.Error("unexpected native request")
		w.WriteHeader(500)
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	tick := func() {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 30; pass++ {
		tick()
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	state := journalState(t, a)
	creators, err := siteCreatedRecords(state)
	if err != nil || len(creators) != 1 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("private creator prerequisite failed", err, config.Status.Phase)
	}
	original := state.DeepCopy().ActionApplications[0]
	if desired[0].kind == "building" {
		desired[0].fields["address"] = "private next address"
	} else {
		desired[0].fields["width"] = float64(14)
	}
	writeModel(false)
	uncertain := map[string]bool{}
	for pass := 0; pass < 35; pass++ {
		state = journalState(t, a)
		if state.Operation != nil && state.Operation.Action == "Update" && state.Operation.Phase == "Accepted" && !uncertain[state.Operation.ID] {
			if mode != "complete" && mode != "preflight-native" && mode != "post-applied-drift" {
				if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				uncertain[state.Operation.ID] = true
			}
			if mode == "source-change" {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), model); err != nil {
					t.Fatal(err)
				}
				model.Data["revision"] = "after-ack"
				if err := a.statusWriter.Update(ctx, model); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "owner-change" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
		}
		tick()
		if mode == "preflight-native" && !sawRefusal {
			for _, condition := range config.Status.Conditions {
				if condition.Reason == "NativePreflightRefused" {
					mu.Lock()
					if writes != 1 {
						t.Fatal("preflight refusal had native effects")
					}
					opaqueChanged = false
					mu.Unlock()
					sawRefusal = true
				}
			}
		}
	}
	state = journalState(t, a)
	rows, err := siteAppliedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count, reads := writes, updateReads
	mu.Unlock()
	positive := mode == "complete" || mode == "recover" || mode == "preflight-native" || mode == "post-applied-drift"
	if !positive {
		if count != 2 || state.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(rows) != 0 || len(state.ActionApplications) != 1 || !reflect.DeepEqual(state.ActionApplications[0], original) {
			t.Fatal("unverified private Update lost its fence or changed creator lineage")
		}
		if (mode == "source-change" || mode == "owner-change") && reads != 0 {
			t.Fatal("changed authority observed a task")
		}
		if mode == "lost-ack" && state.Operation.TaskID != "" {
			t.Fatal("lost acknowledgement inferred a task")
		}
		return
	}
	if count != 2 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(rows) != 1 || sawRefusal != (mode == "preflight-native") {
		t.Fatal("private Update failed to converge once", config.Status.Phase, count)
	}
	first := rows[sitePathDigest(desired[0].path)]
	if !siteAppliedReceiptPresent(state, &first.Operation) || !reflect.DeepEqual(state.ActionApplications[0], original) {
		t.Fatal("private latest row or immutable creator proof invalid")
	}
	if desired[0].kind == "building" {
		desired[0].fields["address"] = "private restored address"
	} else {
		desired[0].fields["width"] = float64(12)
	}
	writeModel(false)
	for pass := 0; pass < 30; pass++ {
		state = journalState(t, a)
		if mode == "recover" && state.Operation != nil && state.Operation.Action == "Update" && state.Operation.Phase == "Accepted" && !uncertain[state.Operation.ID] {
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			uncertain[state.Operation.ID] = true
		}
		tick()
	}
	state = journalState(t, a)
	rows, err = siteAppliedRecords(state)
	mu.Lock()
	count = writes
	mu.Unlock()
	latest := rows[sitePathDigest(desired[0].path)]
	if err != nil || count != 3 || len(rows) != 1 || len(state.ActionApplications) != 2 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || latest.Operation.ID == first.Operation.ID || !reflect.DeepEqual(state.ActionApplications[0], original) {
		t.Fatal("latest replacement grew history, replayed or overwrote creator lineage", err, count, config.Status.Phase)
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), "private restored") || strings.Contains(string(encoded), "private-original") || strings.Contains(string(encoded), "9007199254740993") {
		t.Fatal("native private values entered public status")
	}
	if mode == "post-applied-drift" {
		before := state.DeepCopy()
		mu.Lock()
		opaqueChanged = true
		mu.Unlock()
		for pass := 0; pass < 4; pass++ {
			tick()
		}
		after := journalState(t, a)
		mu.Lock()
		count = writes
		mu.Unlock()
		if count != 3 || after.Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || !reflect.DeepEqual(before.ActionApplications, after.ActionApplications) {
			t.Fatal("post-Update drift was silently rebased or overwritten")
		}
	}
}
