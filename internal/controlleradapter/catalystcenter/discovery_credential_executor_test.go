// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryCredentialExecutorCycle(t *testing.T) {
	for _, mode := range []string{"complete", "known-unknown", "lost-accepted-ack", "lost-permit-ack", "ambiguous-create", "unowned-existing", "job-appeared", "projection-changed", "owner-revoked", "global-replaced"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialExecutorScenario(t, mode, nil) })
	}
}

func TestDiscoveryCredentialPublicCycle(t *testing.T) {
	for _, mode := range []string{"complete", "configmap", "mixed", "known-unknown", "lost-accepted-ack", "lost-permit-ack", "ambiguous-create", "unowned-existing", "pruned-pair", "wrong-native-id", "metadata-drift", "rotation", "controller-alias-addition", "preflight-replan"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialExecutorScenario(t, mode, nil, true) })
	}
}

func discoveryCredentialExecutorScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig), public ...bool) {
	t.Helper()
	ctx := context.Background()
	mixed := mode == "mixed" || mode == "global-replaced"
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, mixed)
	a, nc, c, source, paths := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if len(public) > 0 && public[0] && mode == "known-unknown" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if install != nil {
		install(a, c)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		var err error
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "configmap" {
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "credential-discovery-model", UID: "fixture-model-uid"}, Data: map[string]string{"model": string(raw)}}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		var err error
		source, err = loadControllerSource(ctx, a.statusReader, c)
		if err != nil {
			t.Fatal(err)
		}
	}
	present := mode == "unowned-existing"
	posts, otherWrites := 0, 0
	globalReads := 0
	refusedID := ""
	jobID, user := "created-job", "operator"
	var taskStart int64
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == globalCredentialsPath:
			globalReads++
			id := "global-id"
			if mode == "global-replaced" && globalReads > 1 {
				id = "replacement-id"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []map[string]any{{"id": id, "description": "lab-cli"}}}})
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			if mode == "preflight-replan" && refusedID == "" {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
				} else if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Submitting" {
					refusedID = current.Status.Configuration.Operation.ID
					w.WriteHeader(503)
					return
				}
			}
			rows := []discoveryMetadata{}
			if present {
				row := discoveryMetadata{ID: jobID, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{user}, Condition: "Complete", Status: "Inactive"}
				if mixed {
					row.CredentialIDs = []string{"global-id"}
				}
				rows = append(rows, row)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			posts++
			var body map[string]any
			var expectedIDs any
			if mixed {
				expectedIDs = []any{"global-id"}
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "fixture" || body["ipAddressList"] != "192.0.2.1" || !reflect.DeepEqual(body["globalCredentialIdList"], expectedIDs) || !reflect.DeepEqual(body["passwordList"], []any{discoveryCredentialMarker}) {
				t.Error("unexpected credential request")
			}
			present = true
			taskStart = time.Now().UnixMilli()
			if mode == "ambiguous-create" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				fmt.Fprint(w, `{"response":{"taskId":"original-task"}}`)
			}
		case r.Method == "GET" && r.URL.Path == taskPath+"original-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "original-task", "startTime": taskStart, "endTime": taskStart, "isError": false, "data": `{"discoveryId":"created-job"}`}})
		default:
			if r.Method != "GET" {
				otherWrites++
			}
			t.Error("unexpected API request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	if len(public) > 0 && public[0] {
		discoveryCredentialPublicReconcileCycle(t, a, c, mode, &posts, paths, func() {
			if mode == "wrong-native-id" {
				jobID = "replacement-job"
			}
			if mode == "metadata-drift" {
				user = "different-user"
			}
		})
		if otherWrites != 0 {
			t.Fatal("public lifecycle issued unrelated mutation")
		}
		if mode == "preflight-replan" {
			state := journalState(t, a)
			if refusedID == "" || state.ActionApplications[0].Operation.ID == refusedID || state.CredentialApplications[0].OperationID == refusedID {
				t.Fatal("public replan reused the retired operation or did not exercise refusal")
			}
		}
		return
	}
	plan, err := a.compileDiscoveryCredentialApply(nc, c, source)
	if err != nil {
		t.Fatal(err)
	}
	write, err := plan.executor.next(ctx)
	if mode == "unowned-existing" {
		if !errors.Is(err, errConfigurationOwned) || write != nil || posts != 0 {
			t.Fatal("unowned job was adoptable", err)
		}
		return
	}
	if err != nil || write == nil {
		t.Fatal("next", err)
	}
	if mode == "job-appeared" {
		present = true
	}
	if mode == "projection-changed" {
		if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 1, `["changed"]`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := a.statusWriter
	if mode == "lost-permit-ack" || mode == "lost-accepted-ack" || mode == "owner-revoked" {
		phase := "Submitting"
		if mode == "lost-accepted-ack" {
			phase = "Accepted"
		}
		a.statusWriter = rotatingSecretJournalClient{Client: base, phase: phase, rotate: func() error {
			if mode == "owner-revoked" {
				nc, err := a.currentController(ctx)
				if err != nil {
					return err
				}
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				return base.Status().Update(ctx, nc)
			}
			return errors.New("injected lost acknowledgement")
		}}
	}
	err = a.submitConfigurationWrite(ctx, c, source, "inventory", write)
	a.statusWriter = base
	if mode == "job-appeared" || mode == "global-replaced" {
		state := journalState(t, a)
		if !errors.Is(err, errConfigurationNotDispatched) || state == nil || state.Operation != nil || len(state.ActionApplications) != 0 || len(state.CredentialApplications) != 0 || posts != 0 || otherWrites != 0 {
			t.Fatal("proven credential preflight refusal was not retired", err, state)
		}
		if _, err := write.submit(ctx); err == nil {
			t.Fatal("consumed preflight closure replayed")
		}
		return
	}
	success := mode == "complete" || mode == "known-unknown"
	if (err == nil) != success {
		t.Fatal("unexpected shared dispatch result", mode, err)
	}
	if err := a.submitConfigurationWrite(ctx, c, source, "inventory", write); err == nil {
		t.Fatal("shared dispatcher replayed an existing or stale operation")
	}
	if mode != "lost-permit-ack" && mode != "projection-changed" {
		if _, err := write.submit(ctx); err == nil {
			t.Fatal("consumed executor write replayed")
		}
	}
	if otherWrites != 0 {
		t.Fatal("unrelated mutation issued")
	}
	expectedPosts := 0
	if mode == "complete" || mode == "known-unknown" || mode == "lost-accepted-ack" || mode == "ambiguous-create" {
		expectedPosts = 1
	}
	if posts != expectedPosts {
		t.Fatal("unexpected native submission count", posts, expectedPosts)
	}
	if expectedPosts == 0 {
		state := journalState(t, a)
		if mode == "projection-changed" {
			if state != nil && state.Operation != nil && state.Operation.Phase != "Prepared" {
				t.Fatal("changed source advanced journal")
			}
		} else if state == nil || state.Operation == nil || state.Operation.Phase != "Submitting" && state.Operation.Phase != "Unknown" {
			t.Fatal("unsubmitted uncertainty lost its fence")
		}
		return
	}
	// Reconstruct the worker/projection from persisted objects. The executor has
	// no private retry or task state to preserve across this boundary.
	restarted := *a
	nc, err = restarted.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	freshSource := &configSourceSnapshot{raw: append([]byte(nil), raw...)}
	plan, err = restarted.compileDiscoveryCredentialApply(nc, c, freshSource)
	if err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if mode == "ambiguous-create" {
		if state.Operation.Phase != "Unknown" || state.Operation.TaskID != "" || plan.canObserveUnknownTask(state.Operation) {
			t.Fatal("taskless submission became recoverable")
		}
		if err := restarted.submitConfigurationWrite(ctx, c, freshSource, "inventory", write); err == nil {
			t.Fatal("uncertain submission replayed")
		}
		if posts != 1 {
			t.Fatal("taskless request replayed")
		}
		return
	}
	if mode == "known-unknown" {
		if err := restarted.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
		state = journalState(t, a)
		if !plan.canObserveUnknownTask(state.Operation) {
			t.Fatal("original retained task could not recover")
		}
	}
	done, err := plan.taskComplete(ctx, restarted.client, state.Operation, time.Now())
	if err != nil || !done {
		t.Fatal("original task completion", err)
	}
	if err := restarted.advanceConfiguration(ctx, state.Operation.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	state = journalState(t, a)
	if err := plan.completeWithReceipt(ctx, &restarted, state.Operation); err != nil {
		t.Fatal("atomic completion", err)
	}
	state = journalState(t, a)
	if !discoveryCredentialReceiptPairPresent(state, state.Operation) {
		t.Fatal("executor lost paired evidence")
	}
	if err := restarted.finishConfiguration(ctx, state.Operation.ID); err != nil {
		t.Fatal(err)
	}
	before := journalState(t, a).DeepCopy()
	nc, err = restarted.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = restarted.compileDiscoveryCredentialApply(nc, c, freshSource)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := plan.executor.next(ctx); err != nil || next != nil {
		t.Fatal("unchanged desired input did not converge without a write", err)
	}
	if posts != 1 || !reflect.DeepEqual(before, journalState(t, a)) {
		t.Fatal("no-op rewrote/replayed the receipt")
	}
}

func discoveryCredentialPublicReconcileCycle(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, posts *int, paths map[string]string, changeNative func()) {
	t.Helper()
	ctx := context.Background()
	base := a.statusWriter
	injected := false
	if mode == "lost-permit-ack" || mode == "lost-accepted-ack" {
		phase := "Submitting"
		if mode == "lost-accepted-ack" {
			phase = "Accepted"
		}
		a.statusWriter = rotatingSecretJournalClient{Client: base, phase: phase, rotate: func() error { injected = true; return errors.New("injected publication acknowledgement loss") }}
	}
	defer func() { a.statusWriter = base }()
	sawUnknown := false
	postChange := mode == "pruned-pair" || mode == "wrong-native-id" || mode == "metadata-drift" || mode == "rotation" || mode == "controller-alias-addition"
	converges := mode == "preflight-replan" || postChange || mode == "complete" || mode == "configmap" || mode == "mixed" || mode == "known-unknown" || mode == "lost-accepted-ack"
	r := &configReconciler{a: a}
	for step := 0; step < 16; step++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
			sawUnknown = sawUnknown || nc.Status.Configuration.Operation.Phase == "Unknown"
		}
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
		if !converges && step >= 3 {
			break
		}
	}
	state := journalState(t, a)
	if converges {
		if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || *posts != 1 || state.Operation != nil || len(state.ActionApplications) != 1 || len(state.CredentialApplications) != 1 {
			t.Fatal("public credential discovery did not converge exactly once", c.Status.Phase, *posts, c.Status.Conditions)
		}
		if mode == "known-unknown" && !sawUnknown {
			t.Fatal("public timeout did not exercise original-task Unknown recovery")
		}
		if mode == "lost-accepted-ack" && !injected {
			t.Fatal("acknowledgement fault was not exercised")
		}
		before := state.DeepCopy()
		restarted := *a
		restarted.statusWriter = base
		r = &configReconciler{a: &restarted}
		for i := 0; i < 3; i++ {
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
		}
		if *posts != 1 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("public restarted no-op replayed or changed paired evidence")
		}
		encoded, _ := json.Marshal(journalState(t, a))
		if bytes.Contains(encoded, []byte(discoveryCredentialMarker)) {
			t.Fatal("public journal contains credential material")
		}
		if postChange {
			changeNative()
			if mode == "pruned-pair" || mode == "controller-alias-addition" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "pruned-pair" {
					nc.Status.Configuration.CredentialApplications = nil
					err = base.Status().Update(ctx, nc)
				} else {
					nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: "unused-extra-alias", Name: "unrelated-credentials", Key: "unused"})
					nc.Generation++
					err = base.Update(ctx, nc)
					a.generation = nc.Generation
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "rotation" {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
					t.Fatal(err)
				}
				var model map[string]any
				if json.Unmarshal(c.Spec.Source.Inline.Raw, &model) != nil {
					t.Fatal("invalid source")
				}
				model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)[0].(map[string]any)["password_list_version"] = 2
				c.Spec.Source.Inline.Raw, _ = json.Marshal(model)
				c.Generation++
				if err := base.Update(ctx, c); err != nil {
					t.Fatal(err)
				}
				// Deliberately leave the projection at version 1: source version 2
				// must remain fenced until its authorized projection arrives.
			}
			retained := journalState(t, a).DeepCopy()
			r = &configReconciler{a: a}
			for i := 0; i < 3; i++ {
				if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			if (c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (mode == "controller-alias-addition") || *posts != 1 || !reflect.DeepEqual(retained, journalState(t, a)) {
				t.Fatal("changed input/native/evidence was replayed, adopted or reported converged", c.Status.Phase)
			}
		}
	} else {
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("uncertain or imported job was reported converged")
		}
		if mode == "unowned-existing" {
			if *posts != 0 || state != nil && state.Operation != nil {
				t.Fatal("unowned discovery was mutated")
			}
			return
		}
		phase, expectedPosts := "Unknown", 1
		if mode == "lost-permit-ack" {
			phase, expectedPosts = "Submitting", 0
		}
		if state == nil || state.Operation == nil || state.Operation.Phase != phase || state.Operation.TaskID != "" || *posts != expectedPosts || len(state.ActionApplications) != 0 || len(state.CredentialApplications) != 0 {
			t.Fatal("public uncertainty lost fence or replayed", *posts)
		}
	}
}
