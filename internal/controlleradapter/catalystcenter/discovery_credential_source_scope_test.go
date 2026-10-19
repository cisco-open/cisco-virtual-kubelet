// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialSourceRecoveryModes = []string{"cm-deleted", "cm-changed", "cm-replaced", "projection-lost", "counter-lag", "known-unknown", "lost-receipt-ack", "conflict", "pruned-application", "scope-missing", "scope-tampered", "spec-changed", "controller-changed", "owner-revoked", "taskless", "legacy", "source-returned", "failed-task", "pending-task", "native-drift", "foreign-uuid", "cleanup-conflict"}

func TestDiscoveryCredentialSourceRecovery(t *testing.T) {
	for _, mode := range credentialSourceRecoveryModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialSourceRecoveryScenario(t, mode, nil) })
	}
}
func discoveryCredentialSourceRecoveryScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	a, _, c, _, paths := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if mode == "known-unknown" {
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
	}
	inline := mode == "projection-lost" || mode == "counter-lag"
	var cm *core.ConfigMap
	if !inline {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "recovery-model"}, Data: map[string]string{"model": string(raw)}}
		if install == nil {
			cm.UID = "original-source"
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
	present := false
	posts, writes := 0, 0
	var started int64
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			rows := []discoveryMetadata{}
			if present {
				id, user := "job-id", "operator"
				if mode == "foreign-uuid" {
					id = "foreign"
				}
				if mode == "native-drift" {
					user = "other"
				}
				rows = append(rows, discoveryMetadata{ID: id, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{user}, Condition: "Complete", Status: "Inactive"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			posts++
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			var current configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &current); err != nil {
				t.Error(err)
				return
			}
			if nc.Status.Configuration == nil || discoveryCredentialScopeForOperation(nc, &current, nc.Status.Configuration.Operation) == nil {
				t.Error("native dispatch preceded source attestation")
			}
			present = true
			started = time.Now().UnixMilli()
			if mode == "taskless" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				fmt.Fprint(w, `{"response":{"taskId":"original-task"}}`)
			}
		case r.Method == "GET" && r.URL.Path == taskPath+"original-task":
			end := started
			if mode == "pending-task" {
				end = 0
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "original-task", "startTime": started, "endTime": end, "isError": mode == "failed-task", "data": `{"discoveryId":"job-id"}`}})
		default:
			if r.Method != "GET" {
				writes++
			}
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	reconcile := func() {
		t.Helper()
		copy := *a
		if _, err := (&configReconciler{a: &copy}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 12 && posts == 0; i++ {
		reconcile()
	}
	if posts != 1 {
		t.Fatal("missing acknowledged creation", c.Status.Conditions)
	}
	initial := journalState(t, a).Operation.DeepCopy()
	if initial == nil {
		t.Fatal("missing original operation")
	}
	base := a.statusWriter
	hits := 0
	switch mode {
	case "lost-receipt-ack":
		a.statusWriter = discoveryCredentialCompletionClient{base, "lost-ack", &hits}
	case "conflict":
		a.statusWriter = discoveryCredentialCompletionClient{base, "conflict", &hits}
	case "pruned-application":
		a.statusWriter = discoveryCredentialCompletionClient{base, "pruned-credentials", &hits}
	}
	defer func() { a.statusWriter = base }()
	if inline {
		if mode == "counter-lag" {
			if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 2, `["different"]`), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(paths["passwordList"]); err != nil {
			t.Fatal(err)
		}
	} else if mode == "cm-changed" {
		if err := base.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
			t.Fatal(err)
		}
		cm.Data["model"] = `{"catalyst_center":{"inventory":{"tags":[]}}}`
		if err := base.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := base.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
		if mode == "cm-replaced" {
			cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "recovery-model"}, Data: map[string]string{"model": string(raw)}}
			if install == nil {
				cm.UID = "replacement-source"
			}
			if err := base.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "scope-missing" || mode == "scope-tampered" {
		if mode == "scope-missing" {
			meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
		} else {
			cond := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition)
			cond.Message = strings.Replace(cond.Message, string(c.UID), "foreign-config", 1)
		}
		if err := base.Status().Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "cleanup-conflict" {
		c.Finalizers = append(c.Finalizers, discoveryConfigFinalizer, featureConfigFinalizer)
		if err := base.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "spec-changed" {
		c.Spec.Source.ConfigMapRef.Key = "different"
		c.Generation++
		if err := base.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "controller-changed" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: "unused", Name: "unrelated", Key: "unused"})
		nc.Generation++
		if err := base.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if err := base.Get(ctx, a.key, nc); err != nil {
			t.Fatal(err)
		}
		a.generation = nc.Generation
	}
	if mode == "owner-revoked" || mode == "legacy" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "owner-revoked" {
			nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
		} else {
			resource, _, _ := discoveryCredentialInputParts(nc.Status.Configuration.Operation.Resource)
			nc.Status.Configuration.Operation.Resource = resource
		}
		if err := base.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	blocked := mode == "cleanup-conflict" || mode == "failed-task" || mode == "pending-task" || mode == "native-drift" || mode == "foreign-uuid" || mode == "conflict" || mode == "pruned-application" || mode == "scope-missing" || mode == "scope-tampered" || mode == "spec-changed" || mode == "controller-changed" || mode == "owner-revoked" || mode == "taskless" || mode == "legacy"
	sawUnknown := false
	for i := 0; i < 12; i++ {
		reconcile()
		state := journalState(t, a)
		if state.Operation != nil && state.Operation.Phase == "Unknown" {
			sawUnknown = true
		}
		if !blocked && state.Operation == nil {
			break
		}
	}
	state := journalState(t, a)
	if posts != 1 || writes != 0 {
		t.Fatal("retained completion replayed or submitted a new action")
	}
	if blocked {
		if mode == "cleanup-conflict" {
			found := false
			for _, cond := range c.Status.Conditions {
				if cond.Type == configapi.NetworkControllerConfigConditionReady && cond.Reason == "CleanupFamilyConflict" {
					found = true
				}
			}
			if !found {
				t.Fatal("cleanup conflict guard was bypassed")
			}
		}
		if state.Operation == nil || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("invalid retained source released operation")
		}
		if (mode == "conflict" || mode == "pruned-application") && hits == 0 {
			t.Fatal("receipt fault not exercised")
		}
		return
	}
	if state.Operation != nil || len(state.ActionApplications) != 1 || len(state.CredentialApplications) != 1 || !discoveryCredentialReceiptPairPresent(state, &state.ActionApplications[0].Operation) {
		t.Fatal("original work did not produce paired receipt", c.Status.Conditions, state.Operation)
	}
	receipt := state.ActionApplications[0].Operation
	if receipt.ID != initial.ID || receipt.TaskID != initial.TaskID || receipt.SourceUID != initial.SourceUID || receipt.SourceVersion != initial.SourceVersion || receipt.Resource != initial.Resource {
		t.Fatal("retained completion relabelled original input")
	}
	if mode == "known-unknown" && !sawUnknown {
		t.Fatal("timeout recovery not exercised")
	}
	if mode == "lost-receipt-ack" && hits != 1 {
		t.Fatal("lost acknowledgement not exercised")
	}
	if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("retained work reported current InSync")
	}
	a.statusWriter = base
	before := state.DeepCopy()
	if mode == "source-returned" {
		restored := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "recovery-model"}, Data: map[string]string{"model": string(raw)}}
		if install == nil {
			restored.UID = "returned-source"
		}
		if err := base.Create(ctx, restored); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		reconcile()
	}
	if !reflect.DeepEqual(before, journalState(t, a)) || posts != 1 || writes != 0 {
		t.Fatal("restart or restored source replayed original operation")
	}
	if mode == "source-returned" && c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("restored valid source did not resume ordinary no-op", c.Status.Conditions)
	}
}

func TestDiscoveryCredentialScopePublicationGuard(t *testing.T) {
	credentialScopePublicationGuard(t, nil)
}
func credentialScopePublicationGuard(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, nc, c, _, _ := discoveryProjectionFixture(t, discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false), map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
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
	status := *c.Status.DeepCopy()
	source, handled, err := (&configReconciler{a: a}).recordDiscoveryCredentialSourceScope(ctx, nc, c, &status)
	if err != nil || !handled || source.discoveryScopeControllerVersion == "" {
		t.Fatal("scope not planned", err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: pnpCapacityHistory(1, false)[0].Operation.DeepCopy()}
	nc.Status.Configuration.Operation.Phase = "Unknown"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.checkConfigurationSnapshot(ctx, c, source); err == nil {
		t.Fatal("scope publication ignored journal race")
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition) != nil {
		t.Fatal("unpublished scope became recovery authority")
	}
}

// Inject a competing journal entry after scope planning, before the reconciler's
// actual publication guard. This tests the public status path, not only the helper.
type credentialScopeRaceReader struct {
	ctrlclient.Client
	key      ctrlclient.ObjectKey
	reads    *int
	injected *bool
}

func (r credentialScopeRaceReader) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if _, ok := obj.(*cvk.NetworkController); ok && key == r.key {
		*r.reads++
		if *r.reads == 3 && !*r.injected {
			var nc cvk.NetworkController
			if err := r.Client.Get(ctx, key, &nc); err != nil {
				return err
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: pnpCapacityHistory(1, false)[0].Operation.DeepCopy()}
			nc.Status.Configuration.Operation.Phase = "Unknown"
			if err := r.Client.Status().Update(ctx, &nc); err != nil {
				return err
			}
			*r.injected = true
		}
	}
	return r.Client.Get(ctx, key, obj, opts...)
}
func TestDiscoveryCredentialScopePublicPublicationRace(t *testing.T) {
	credentialScopePublicPublicationRace(t, nil)
}
func credentialScopePublicPublicationRace(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, _, c, _, _ := discoveryProjectionFixture(t, discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false), map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if install != nil {
		install(a, c)
	}
	reads, calls := 0, 0
	injected := false
	a.statusReader = credentialScopeRaceReader{a.statusWriter, a.key, &reads, &injected}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if !injected || calls != 0 || meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition) != nil {
		t.Fatal("scope race reached publication or native I/O", injected, calls)
	}
}

type credentialSourceChangeReader struct {
	ctrlclient.Client
	key             ctrlclient.ObjectKey
	reads           *int
	armed, injected *bool
}

func (r credentialSourceChangeReader) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if _, ok := obj.(*core.ConfigMap); ok && key == r.key && *r.armed {
		*r.reads++
		if *r.reads == 2 && !*r.injected {
			var cm core.ConfigMap
			if err := r.Client.Get(ctx, key, &cm); err != nil {
				return err
			}
			cm.Data["model"] = strings.Replace(cm.Data["model"], `"SSH"`, `"SSH","retry":2`, 1)
			if err := r.Client.Update(ctx, &cm); err != nil {
				return err
			}
			*r.injected = true
		}
	}
	return r.Client.Get(ctx, key, obj, opts...)
}
func TestDiscoveryCredentialSourceChangeBeforePlanning(t *testing.T) {
	credentialSourceChangeBeforePlanning(t, nil)
}
func credentialSourceChangeBeforePlanning(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	a, _, c, _, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if install != nil {
		install(a, c)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "changing-source"}, Data: map[string]string{"model": string(raw)}}
	if install == nil {
		cm.UID = "changing-source"
	}
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	c.Spec.Source.Inline = nil
	c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	calls, reads := 0, 0
	armed, injected := false, false
	a.statusReader = credentialSourceChangeReader{a.statusWriter, ctrlclient.ObjectKeyFromObject(cm), &reads, &armed, &injected}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	run := func() {
		t.Helper()
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	run()
	cond := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition)
	if cond == nil {
		t.Fatal("initial scope not published")
	}
	old := cond.Message
	armed = true
	run()
	if !injected || calls != 0 || journalState(t, a) != nil || c.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting || meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition).Message != old {
		t.Fatal("changed source reached planning or native I/O", injected, calls, c.Status.Conditions)
	}
}
