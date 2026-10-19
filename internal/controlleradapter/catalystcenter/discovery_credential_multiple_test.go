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
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryCredentialPublicMultipleJobs(t *testing.T) {
	for _, mode := range []string{"complete", "late-unowned", "composition", "composition-configmap", "composition-unknown", "composition-late-unowned", "composition-reclassify", "composition-replace"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialMultipleScenario(t, mode, nil) })
	}
}

func discoveryCredentialMultipleScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	ctx := context.Background()
	var model map[string]any
	if json.Unmarshal(discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false), &model) != nil {
		t.Fatal("invalid fixture")
	}
	inventory := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
	first := inventory["discovery"].([]any)[0].(map[string]any)
	second := map[string]any{}
	for k, v := range first {
		second[k] = v
	}
	second["name"] = "another"
	composition := strings.HasPrefix(mode, "composition") || strings.HasPrefix(mode, "prune-composition") || mode == "public-delete-mixed"
	lateUnowned := mode == "late-unowned" || mode == "composition-late-unowned"
	if composition {
		delete(second, "password_list_version")
		delete(second, "user_name_list")
		second["global_credential_list"] = []any{"lab-cli"}
		second["retry"] = 3
	}
	if mode == "composition-reclassify" {
		first["global_credential_list"] = []any{"lab-cli"}
	}
	inventory["discovery"] = []any{first, second}
	raw, _ := json.Marshal(model)
	a, _, c, _, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if mode == "composition-unknown" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if install != nil {
		install(a, c)
	}
	if mode == "composition-configmap" || mode == "prune-configmap" || mode == "prune-source-loss" || mode == "prune-empty-source-loss" {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "composed-source", UID: "fixture-composed-model"}, Data: map[string]string{"model": string(raw)}}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	jobs := map[string]discoveryMetadata{}
	if lateUnowned {
		jobs["fixture"] = discoveryMetadata{ID: "foreign", Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"}
	}
	tasks := map[string]map[string]any{}
	creates := map[string]int{}
	posts, deletes, otherWrites := 0, 0, 0
	newTask := func(taskID, id string) {
		now := time.Now().UnixMilli()
		tasks[taskID] = map[string]any{"id": taskID, "startTime": now, "endTime": now, "isError": false, "data": fmt.Sprintf(`{"discoveryId":"%s"}`, id)}
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == globalCredentialsPath:
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []map[string]any{{"id": "global-id", "description": "lab-cli"}}}})
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			rows := []discoveryMetadata{}
			for _, job := range jobs {
				rows = append(rows, job)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid body")
				w.WriteHeader(500)
				return
			}
			name, _ := body["name"].(string)
			if name != "fixture" && name != "another" {
				t.Error("unknown job")
				w.WriteHeader(500)
				return
			}
			if _, present := jobs[name]; present {
				t.Error("native request replayed")
			}
			posts++
			creates[name]++
			id, taskID := "job-"+name, "task-"+name
			if creates[name] > 1 {
				id += fmt.Sprint("-", creates[name])
				taskID += fmt.Sprint("-", creates[name])
			}
			job := discoveryMetadata{ID: id, Name: name, Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"}
			if composition && name == "another" {
				if body["passwordList"] != nil || !reflect.DeepEqual(body["globalCredentialIdList"], []any{"global-id"}) || body["retry"] != float64(3) && body["retry"] != float64(4) {
					t.Error("ordinary global discovery payload changed")
				}
				job.Usernames = nil
				job.CredentialIDs = []string{"global-id"}
				retry := int(body["retry"].(float64))
				job.Retry = &retry
			} else if !reflect.DeepEqual(body["passwordList"], []any{discoveryCredentialMarker}) {
				t.Error("credential discovery payload changed")
			}
			if mode == "composition-reclassify" && name == "fixture" {
				job.CredentialIDs = []string{"global-id"}
			}
			jobs[name] = job
			newTask(taskID, id)
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": taskID}})
		case r.Method == "DELETE" && (mode == "composition-replace" || strings.HasPrefix(mode, "prune") || strings.HasPrefix(mode, "public-delete")):
			name := ""
			for key, row := range jobs {
				if r.URL.Path == discoveryPath+"/"+row.ID {
					name = key
				}
			}
			if name == "" {
				t.Error("unknown exact cleanup UUID")
				w.WriteHeader(500)
				return
			}
			id := jobs[name].ID
			delete(jobs, name)
			deletes++
			taskID := "delete-" + id
			newTask(taskID, id)
			if mode == "prune-failed" {
				tasks[taskID]["isError"] = true
			}
			if mode == "prune-ambiguous" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": taskID}})
			}
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath):
			task, found := tasks[strings.TrimPrefix(r.URL.Path, taskPath)]
			if !found {
				t.Error("unknown retained task")
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": task})
		default:
			if r.Method != "GET" {
				otherWrites++
			}
			t.Error("unexpected API request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	if strings.HasPrefix(mode, "public-delete") {
		protectCredentialFixtureController(t, a)
		enrollPublicCredentialFixture(t, a, c, mode, &posts)
	}
	unknown := map[string]bool{}
	for step := 0; step < 32; step++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
		state := journalState(t, a)
		if state != nil && state.Operation != nil && state.Operation.Phase == "Unknown" {
			if ownedDiscoverySelected(state.Operation.Resource) {
				unknown["ordinary"] = true
			} else if discoveryCredentialInputSelected(state.Operation.Resource) {
				unknown["credentials"] = true
			}
		}
		if lateUnowned && step >= 3 {
			break
		}
	}
	state := journalState(t, a)
	if otherWrites != 0 {
		t.Fatal("unrelated mutation")
	}
	if lateUnowned {
		if posts != 0 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || state != nil && state.Operation != nil {
			t.Fatal("later conflict allowed partial job creation")
		}
		return
	}
	if mode == "public-delete-mixed" {
		if posts != 0 || deletes != 0 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || state != nil && state.Operation != nil {
			t.Fatal("unsupported mixed Delete model partially executed")
		}
		return
	}
	versions := 2
	if composition {
		versions = 1
	}
	if posts != 2 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || state.Operation != nil || len(state.ActionApplications) != 2 || len(state.CredentialApplications) != versions {
		t.Fatal("two jobs did not converge exactly once", posts, c.Status.Phase)
	}
	for _, receipt := range state.ActionApplications {
		if composition && ownedDiscoverySelected(receipt.Operation.Resource) {
			if !strings.HasPrefix(receipt.Operation.Resource, ownedDiscoveryPrefix) || !validOwnedDiscoveryReceipt(receipt) || len(receipt.Operation.CredentialVersions) != 0 {
				t.Fatal("ordinary receipt identity or semantics changed")
			}
		} else if !discoveryCredentialReceiptPairPresent(state, &receipt.Operation) {
			t.Fatal("job receipt pair is incomplete")
		}
	}
	if mode == "composition-unknown" && (!unknown["ordinary"] || !unknown["credentials"]) {
		t.Fatal("both original-task recovery families were not exercised")
	}
	for i := 0; i < 3; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 2 {
		t.Fatal("multiple-job no-op replayed")
	}
	if strings.HasPrefix(mode, "public-delete") {
		exercisePublicCredentialCohortDeletion(t, a, c, mode, jobs, &posts, &deletes)
		return
	}
	if strings.HasPrefix(mode, "prune") {
		discoveryCredentialPruneCycle(t, a, c, model, first, raw, jobs, &posts, &deletes, mode)
		return
	}
	if mode == "composition-replace" {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		before := journalState(t, a).DeepCopy()
		originalID := jobs["another"].ID
		second["retry"] = 4
		c.Spec.Source.Inline.Raw, _ = json.Marshal(model)
		c.Generation++
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		for step := 0; step < 32; step++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				break
			}
		}
		after := journalState(t, a)
		if posts != 3 || deletes != 1 || jobs["another"].ID == originalID || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || after.Operation != nil || !reflect.DeepEqual(before.CredentialApplications, after.CredentialApplications) {
			t.Fatal("ordinary replacement changed credential application or failed", posts, deletes, c.Status.Phase)
		}
		for _, r := range before.ActionApplications {
			if discoveryCredentialInputSelected(r.Operation.Resource) {
				found := false
				for _, next := range after.ActionApplications {
					if reflect.DeepEqual(r, next) {
						found = true
					}
				}
				if !found {
					t.Fatal("ordinary replacement rewrote credential creator proof")
				}
			}
		}
	}
	if mode == "composition-reclassify" {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		delete(first, "password_list_version")
		delete(first, "user_name_list")
		c.Spec.SecretRefs = nil
		c.Spec.Source.Inline.Raw, _ = json.Marshal(model)
		c.Generation++
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		before := journalState(t, a).DeepCopy()
		for i := 0; i < 3; i++ {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || posts != 2 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("removing credential leaves reclassified a credential-owned job as global-only import")
		}
	}
}
