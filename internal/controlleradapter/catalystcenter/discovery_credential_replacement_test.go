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
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type discoveryRetirementFaultClient struct {
	ctrlclient.Client
	mode string
	hits *int
}
type discoveryRetirementFaultWriter struct {
	ctrlclient.SubResourceWriter
	client discoveryRetirementFaultClient
}

func (c discoveryRetirementFaultClient) Status() ctrlclient.SubResourceWriter {
	return discoveryRetirementFaultWriter{c.Client.Status(), c}
}
func (w discoveryRetirementFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || !discoveryCredentialRetirementSelected(nc.Status.Configuration.Operation.Resource) {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	phase := "Verified"
	if w.client.mode == "lost-permit-ack" {
		phase = "Submitting"
	}
	if w.client.mode == "lost-accepted-ack" {
		phase = "Accepted"
	}
	if nc.Status.Configuration.Operation.Phase != phase || *w.client.hits > 0 {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.client.hits++
	if w.client.mode == "conflict" {
		return errors.New("injected rejected receipt write")
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.client.mode == "pruned-retirement" {
		var stored cvk.NetworkController
		if err := w.client.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &stored); err != nil {
			return err
		}
		out := stored.Status.Configuration.ActionApplications[:0]
		for _, r := range stored.Status.Configuration.ActionApplications {
			if !discoveryCredentialRetiredSelected(r.Resource) {
				out = append(out, r)
			}
		}
		stored.Status.Configuration.ActionApplications = out
		return w.client.Client.Status().Update(ctx, &stored)
	}
	return errors.New("injected lost retirement acknowledgement")
}

func TestDiscoveryCredentialReplacement(t *testing.T) {
	for _, mode := range []string{"complete", "configmap", "active", "known-unknown", "lost-accepted-ack", "lost-receipt-ack", "conflict", "pruned-retirement", "ambiguous-delete", "lost-permit-ack", "retained-native", "foreign-native", "failed-task", "native-drift", "capacity", "source-change", "source-loss-successor", "delete-source-complete", "delete-source-lost-receipt-ack", "delete-source-conflict", "delete-source-ambiguous-delete", "delete-source-retained-native", "delete-source-foreign-native", "delete-source-failed-task"} {
		t.Run(mode, func(t *testing.T) { discoveryCredentialReplacementScenario(t, mode, nil) })
	}
}

func discoveryCredentialReplacementScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	retainedDelete := strings.HasPrefix(mode, "delete-source-")
	faultMode := strings.TrimPrefix(mode, "delete-source-")
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1, "enable_password_list_version": 1}, false)
	a, _, c, _, paths := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`, "enablePasswordList": `["CVK_SENSITIVE_TEST_VALUE_ENABLE"]`})
	if install != nil {
		install(a, c)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	refs := append([]configapi.NetworkControllerSecretRef(nil), c.Spec.SecretRefs...)
	var cm *core.ConfigMap
	if mode == "configmap" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "replacement-model"}, Data: map[string]string{"model": string(raw)}}
		if install == nil {
			cm.UID = "replacement-model-uid"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	var job *discoveryMetadata
	creates, deletes, otherWrites := 0, 0, 0
	type taskFixture struct {
		start  int64
		id     string
		delete bool
	}
	tasks := map[string]taskFixture{}
	replacement := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			rows := []discoveryMetadata{}
			if job != nil {
				row := *job
				if replacement && mode == "native-drift" {
					row.Usernames = []string{"foreign"}
				}
				rows = append(rows, row)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			if job != nil {
				t.Error("creation while predecessor still exists")
			}
			creates++
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid creation body")
			}
			passwords, ok := body["passwordList"].([]any)
			if !ok || len(passwords) != 1 || passwords[0] != discoveryCredentialMarker {
				t.Error("credential projection lost")
			}
			users := []string{}
			for _, u := range body["userNameList"].([]any) {
				users = append(users, u.(string))
			}
			job = &discoveryMetadata{ID: fmt.Sprintf("job-%d", creates), Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: users, Condition: "Complete", Status: "Inactive"}
			id := fmt.Sprintf("create-task-%d", creates)
			tasks[id] = taskFixture{time.Now().UnixMilli(), job.ID, false}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		case r.Method == "DELETE" && job != nil && r.URL.Path == discoveryPath+"/"+job.ID:
			deletes++
			id := fmt.Sprintf("delete-task-%d", deletes)
			tasks[id] = taskFixture{time.Now().UnixMilli(), job.ID, true}
			if faultMode != "retained-native" {
				job = nil
			}
			if faultMode == "foreign-native" {
				job = &discoveryMetadata{ID: "foreign-job", Name: "fixture"}
			}
			if faultMode == "ambiguous-delete" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
			}
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimPrefix(r.URL.Path, taskPath)
			task, ok := tasks[id]
			if !ok || len(r.URL.Path) < len(taskPath) {
				t.Error("unknown task path", r.URL.Path)
				w.WriteHeader(500)
				return
			}
			data := ""
			if !task.delete {
				data = fmt.Sprintf(`{"discoveryId":%q}`, task.id)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": task.start, "endTime": task.start, "isError": task.delete && faultMode == "failed-task", "data": data}})
		default:
			otherWrites++
			t.Error("unexpected mutation", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	reconcile := func() {
		t.Helper()
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	converge := func() {
		t.Helper()
		for i := 0; i < 20; i++ {
			reconcile()
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
				return
			}
		}
		t.Fatal("did not converge", c.Status.Conditions, journalState(t, a))
	}
	converge()
	if creates != 1 || deletes != 0 {
		t.Fatal("initial dispatch count")
	}
	update := func(password, enable int64, user string) {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		fields := map[string]any{"user_name_list": []any{user}, "password_list_version": password}
		if enable > 0 {
			fields["enable_password_list_version"] = enable
		}
		raw := discoveryCredentialSource(t, fields, false)
		if cm == nil {
			c.Spec.Source.Inline.Raw = raw
		} else {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
				t.Fatal(err)
			}
			cm.Data["model"] = string(raw)
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
		c.Spec.SecretRefs = nil
		for _, ref := range refs {
			if enable == 0 && ref.Path == "/discovery/0/enable_password_list" {
				continue
			}
			c.Spec.SecretRefs = append(c.Spec.SecretRefs, ref)
		}
		if mode == "known-unknown" {
			c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
		}
		c.Generation++
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, password, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
			t.Fatal(err)
		}
		if enable > 0 {
			if err := os.WriteFile(paths["enablePasswordList"], versionedSecretFixture(t, enable, `["CVK_SENSITIVE_TEST_VALUE_ENABLE"]`), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	initial := journalState(t, a).DeepCopy()
	replacement = true
	if mode == "active" {
		job.Status = "Active"
		job.Condition = "In Progress"
	}
	update(2, 0, "operator")
	base := a.statusWriter
	hits := 0
	switch faultMode {
	case "lost-permit-ack", "lost-accepted-ack", "lost-receipt-ack", "conflict", "pruned-retirement":
		a.statusWriter = discoveryRetirementFaultClient{base, faultMode, &hits}
	}
	defer func() { a.statusWriter = base }()
	if mode == "capacity" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// Reuse admitted unrelated receipts rather than empty operations: the
		// real CRD must accept the synthetic occupancy before capacity is tested.
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(254, false)...)
		if err := base.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	blocked := mode == "pruned-retirement" || faultMode == "ambiguous-delete" || mode == "lost-permit-ack" || faultMode == "retained-native" || faultMode == "foreign-native" || faultMode == "failed-task" || mode == "native-drift" || mode == "capacity" || mode == "source-change"
	lostDeleteSource, restoredDeleteSource := false, false
	interruptDelete := func() {
		t.Helper()
		if retainedDelete && !lostDeleteSource && deletes == 1 {
			if err := os.Remove(paths["passwordList"]); err != nil {
				t.Fatal(err)
			}
			lostDeleteSource = true
		}
	}
	if blocked {
		changed := false
		for i := 0; i < 12; i++ {
			reconcile()
			interruptDelete()
			if mode == "source-change" && !changed {
				op := journalState(t, a).Operation
				if op != nil && op.Action == "Delete" && op.TaskID != "" {
					update(3, 0, "operator")
					changed = true
				}
			}
		}
		if mode == "source-change" && !changed {
			t.Fatal("source interruption not exercised")
		}
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || creates != 1 || deletes > 1 {
			t.Fatal("uncertain replacement converged or replayed", creates, deletes, c.Status.Conditions)
		}
		if (mode == "lost-permit-ack" || mode == "native-drift" || mode == "capacity") && deletes != 0 {
			t.Fatal("pre-dispatch fence failed")
		}
		if mode == "pruned-retirement" && hits != 1 {
			t.Fatal("pruning fault not exercised")
		}
		if retainedDelete && !lostDeleteSource {
			t.Fatal("delete source interruption not exercised")
		}
		return
	}
	lostSuccessorSource, restoredSuccessorSource := false, false
	sawUnknown := false
	for i := 0; i < 30; i++ {
		reconcile()
		interruptDelete()
		if retainedDelete && lostDeleteSource && !restoredDeleteSource && journalState(t, a).Operation == nil {
			if creates != 1 || deletes != 1 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("retained delete consumed successor intent")
			}
			state := journalState(t, a).DeepCopy()
			if len(state.ActionApplications) != 2 || len(state.CredentialApplications) != 1 {
				t.Fatal("retirement evidence missing or credential delivery invented")
			}
			for j := 0; j < 3; j++ {
				reconcile()
			}
			if creates != 1 || deletes != 1 || !reflect.DeepEqual(state, journalState(t, a)) {
				t.Fatal("missing projection authorized successor")
			}
			if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 2, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
				t.Fatal(err)
			}
			restoredDeleteSource = true
		}
		if mode == "source-loss-successor" && creates == 2 {
			state := journalState(t, a)
			if !lostSuccessorSource && state.Operation != nil && state.Operation.Action == "Create" && state.Operation.TaskID != "" {
				if err := os.Remove(paths["passwordList"]); err != nil {
					t.Fatal(err)
				}
				lostSuccessorSource = true
			}
			if lostSuccessorSource && !restoredSuccessorSource && state.Operation == nil {
				if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("retained successor claimed current readiness")
				}
				if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 2, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
					t.Fatal(err)
				}
				restoredSuccessorSource = true
			}
		}
		if op := journalState(t, a).Operation; op != nil && op.Action == "Delete" && op.Phase == "Unknown" {
			sawUnknown = true
		}
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
			break
		}
	}
	if creates != 2 || deletes != 1 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("replacement did not converge", creates, deletes, c.Status.Conditions, journalState(t, a).Operation)
	}
	if mode == "known-unknown" && !sawUnknown {
		t.Fatal("delete timeout recovery not exercised")
	}
	if (mode == "lost-accepted-ack" || mode == "lost-receipt-ack" || mode == "conflict") && hits != 1 {
		t.Fatal("journal fault not exercised")
	}
	if mode == "source-loss-successor" && (!lostSuccessorSource || !restoredSuccessorSource) {
		t.Fatal("successor source recovery was not exercised")
	}
	if retainedDelete && (!lostDeleteSource || !restoredDeleteSource) {
		t.Fatal("retained delete recovery not exercised")
	}
	if retainedDelete && (faultMode == "lost-receipt-ack" || faultMode == "conflict") && hits != 1 {
		t.Fatal("retained delete receipt fault not exercised")
	}
	a.statusWriter = base
	if mode == "complete" || mode == "configmap" {
		// Return to earlier public input under the same applied credential version.
		update(2, 0, "other")
		converge()
		update(2, 0, "operator")
		converge()
		before := journalState(t, a).DeepCopy()
		beforeCreates, beforeDeletes := creates, deletes
		update(2, 1, "operator")
		for i := 0; i < 3; i++ {
			reconcile()
		}
		if creates != beforeCreates || deletes != beforeDeletes || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("removed credential floor reset")
		}
		update(3, 2, "operator")
		converge()
	}
	state := journalState(t, a)
	if len(state.ActionApplications) != creates+deletes || len(state.CredentialApplications) != creates || !reflect.DeepEqual(initial.ActionApplications[0], state.ActionApplications[0]) || !reflect.DeepEqual(initial.CredentialApplications[0], state.CredentialApplications[0]) {
		t.Fatal("predecessor evidence overwritten")
	}
	if _, err := discoveryCredentialLineages(state, string(c.UID)); err != nil {
		t.Fatal("invalid final audit", err)
	}
	retained := state.DeepCopy()
	for i := 0; i < 3; i++ {
		copy := *a
		a = &copy
		reconcile()
	}
	if !reflect.DeepEqual(retained, journalState(t, a)) || creates != len(retained.CredentialApplications) {
		t.Fatal("restart replayed replacement")
	}
	encoded, _ := json.Marshal(state)
	if bytes.Contains(encoded, []byte(discoveryCredentialMarker)) || otherWrites != 0 {
		t.Fatal("credential leaked or unrelated mutation")
	}
}
