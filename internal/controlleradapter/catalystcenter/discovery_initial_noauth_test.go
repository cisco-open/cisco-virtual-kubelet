// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var initialNoAuthModes = []string{"inline", "configmap", "unknown", "prepared", "prepared-configmap", "prepared-source-loss", "prepared-committed-source-loss", "prepared-native-conflict", "prepared-scope-loss", "handoff", "handoff-configmap", "prepared-handoff", "prepared-committed-source-loss-handoff", "projected", "projected-configmap", "projected-return-to-metadata"}

func TestDiscoveryInitialNoAuthLifecycle(t *testing.T) {
	for _, mode := range initialNoAuthModes {
		t.Run(mode, func(t *testing.T) { initialNoAuthScenario(t, mode, nil) })
	}
}

var noAuthRetainModes = []string{"inline", "default", "configmap", "unknown", "unknown-source-loss", "projected", "projected-configmap", "projected-return-to-metadata", "projected-reintroduce", "projected-reuse", "versioned-origin", "prepared", "blocked-discovery-finalizer", "blocked-feature-finalizer"}

func TestDiscoveryNoAuthRetainLifecycle(t *testing.T) {
	for _, mode := range noAuthRetainModes {
		t.Run(mode, func(t *testing.T) { initialNoAuthScenario(t, "retain-"+mode, nil) })
	}
}

func initialNoAuthScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	retain := strings.HasPrefix(mode, "retain-")
	mode = strings.TrimPrefix(mode, "retain-")
	metadataRaw := discoveryCredentialSource(t, map[string]any{"snmp_version": "v3", "snmp_mode": "NoAuthNoPriv", "snmp_user_name": "operator"}, false)
	fromVersioned := retain && mode == "versioned-origin"
	raw := metadataRaw
	var values map[string]string
	if fromVersioned {
		raw = discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
		values = map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`}
	}
	a, _, c, _, _ := discoveryProjectionFixture(t, raw, values)
	if install != nil {
		install(a, c)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	admin := a.statusWriter
	var cm *core.ConfigMap
	if strings.Contains(mode, "configmap") || strings.Contains(mode, "source-loss") {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "initial-noauth-model"}, Data: map[string]string{"model": string(raw)}}
		if install == nil {
			cm.UID = "initial-noauth-source"
		}
		if err := admin.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	posts, deletes, other := 0, 0, 0
	present := false
	var row discoveryMetadata
	projectedPayload := fromVersioned
	projectedValue := discoveryCredentialMarker
	tasks := map[string]map[string]any{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"fixture"}`)
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			rows := []discoveryMetadata{}
			if present {
				rows = append(rows, row)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			posts++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			encoded, _ := json.Marshal(body)
			row = discoveryMetadata{}
			if err := json.Unmarshal(encoded, &row); err != nil {
				t.Error(err)
			}
			if projectedPayload {
				if !reflect.DeepEqual(body["passwordList"], []any{projectedValue}) || !reflect.DeepEqual(body["userNameList"], []any{"operator"}) {
					t.Error("projected credential request changed")
				}
			} else if row.SNMPVersion != "v3" || row.SNMPMode != "NoAuthNoPriv" || row.SNMPUser != "operator" {
				t.Error("NoAuth metadata changed")
			}
			row.ID = fmt.Sprintf("initial-noauth-job-%d", posts)
			row.Condition = "Complete"
			row.Status = "Inactive"
			present = true
			id := fmt.Sprintf("initial-noauth-create-%d", posts)
			stamp := time.Now().UnixMilli()
			tasks[id] = map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false, "data": fmt.Sprintf(`{"discoveryId":%q}`, row.ID)}
			if mode == "unknown-source-loss" {
				if err := admin.Delete(ctx, cm); err != nil {
					t.Error(err)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		case r.Method == "DELETE" && present && r.URL.Path == discoveryPath+"/"+row.ID:
			deletes++
			present = false
			id := fmt.Sprintf("initial-noauth-delete-%d", deletes)
			stamp := time.Now().UnixMilli()
			tasks[id] = map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false, "data": fmt.Sprintf(`{"discoveryId":%q}`, row.ID)}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimPrefix(r.URL.Path, taskPath)
			task, ok := tasks[id]
			if !ok {
				t.Error("foreign task", id)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": task})
		default:
			other++
			t.Error("unexpected native request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	if strings.Contains(mode, "handoff") || strings.HasPrefix(mode, "projected") || fromVersioned {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: "initial-password", Name: "discovery-inputs", Key: "password"})
		if err := admin.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, a.key, nc); err != nil {
			t.Fatal(err)
		}
		a.generation = nc.Generation
	}
	protectCredentialFixtureController(t, a)
	if retain {
		c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
		if mode == "default" {
			c.Spec.DeletionPolicy = ""
		}
		c.Finalizers = nil
		if mode == "blocked-discovery-finalizer" {
			c.Finalizers = []string{discoveryConfigFinalizer}
		}
		if mode == "blocked-feature-finalizer" {
			c.Finalizers = []string{featureConfigFinalizer}
		}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	} else {
		enrollPublicCredentialFixture(t, a, c, "inline", &posts)
	}
	key := ctrlclient.ObjectKeyFromObject(c)
	worker := statusOnlyFeatureClient{Client: admin}
	a.statusWriter = worker
	faults := 0
	prepared := strings.HasPrefix(mode, "prepared")
	if prepared {
		a.statusWriter = publicCredentialPreparedFaultClient{worker, &faults}
	}
	unknown := false
	complete := false
	blocked := retain && strings.HasPrefix(mode, "blocked-")
	limit := 35
	if blocked {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if state == nil {
			continue
		}
		if prepared && state.Operation != nil && state.Operation.Phase == "Prepared" {
			if posts != 0 || faults != 1 || len(state.CredentialApplications) != 0 {
				t.Fatal("preparation dispatched or invented delivery")
			}
			complete = true
			break
		}
		if strings.HasPrefix(mode, "unknown") && !unknown && state.Operation != nil && state.Operation.Phase == "Accepted" {
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			unknown = true
		}
		if state.Operation == nil && len(state.ActionApplications) == 1 && (c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || mode == "unknown-source-loss") {
			complete = true
			break
		}
	}
	if blocked {
		state := journalState(t, a)
		if complete || posts != 0 || deletes != 0 || other != 0 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(c.Finalizers) != 1 {
			t.Fatal("incompatible finalizer gained NoAuth execution authority", c.Status.Conditions)
		}
		if state != nil && (state.Operation != nil || len(state.Owners) != 0 || len(state.ActionApplications) != 0 || len(state.CredentialApplications) != 0) {
			t.Fatal("incompatible finalizer claimed ownership or outcome")
		}
		return
	}
	if !complete {
		t.Fatal("initial lifecycle did not settle", c.Status.Conditions, journalState(t, a))
	}
	if strings.HasPrefix(mode, "unknown") && !unknown {
		t.Fatal("Unknown recovery not exercised")
	}
	before := journalState(t, a).DeepCopy()
	initialDeliveries := 0
	initialPrefix := discoveryCredentialMetadataScopePrefix
	if fromVersioned {
		initialDeliveries = 1
		initialPrefix = discoveryCredentialScopePrefix
	}
	if !prepared && (posts != 1 || len(before.ActionApplications) != 1 || len(before.CredentialApplications) != initialDeliveries || discoveryCredentialInputScopePrefix(before.ActionApplications[0].Operation.Resource) != initialPrefix) {
		t.Fatal("metadata created false delivery or wrong protocol")
	}
	if mode == "unknown-source-loss" {
		if !unknown || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("source-lost completion was not independently recovered or claimed current InSync")
		}
		restored := cm.DeepCopy()
		restored.UID = ""
		restored.ResourceVersion = ""
		if install == nil {
			restored.UID = "restored-noauth-model"
		}
		if err := admin.Create(ctx, restored); err != nil {
			t.Fatal(err)
		}
		cm = restored
		settled := false
		for i := 0; i < 20; i++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
				settled = true
				break
			}
		}
		if !settled || posts != 1 || deletes != 0 || !reflect.DeepEqual(before.ActionApplications, journalState(t, a).ActionApplications) {
			t.Fatal("restored source replayed native work or did not converge", c.Status.Conditions)
		}
	}
	expectedDelivery := initialDeliveries
	transitionActions := 0
	if strings.HasPrefix(mode, "projected") || fromVersioned {
		revise := func(projected bool, version int64, expectedActions int, blocked bool) {
			t.Helper()
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			nextRaw := metadataRaw
			c.Spec.SecretRefs = nil
			if projected {
				nextRaw = discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": version}, false)
				c.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "inventory", Path: "/discovery/0/password_list", Source: "initial-password"}}
				relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: c.Name, Section: "inventory", JSONPointer: "/discovery/0/password_list", SourceAlias: "initial-password", SecretName: "discovery-inputs", SecretKey: "password"})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(a.intentSecretPath, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				projectedValue = discoveryCredentialMarker
				if version > 1 {
					projectedValue = fmt.Sprintf("%s_V%d", discoveryCredentialMarker, version)
				}
				value, _ := json.Marshal([]string{projectedValue})
				if err := os.WriteFile(path, versionedSecretFixture(t, version, string(value)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if cm == nil {
				c.Spec.Source.Inline.Raw = nextRaw
			} else {
				cm.Data["model"] = string(nextRaw)
				if err := admin.Update(ctx, cm); err != nil {
					t.Fatal(err)
				}
			}
			if install == nil {
				c.Generation++
			}
			if err := admin.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			projectedPayload = projected
			settled := false
			expectedApplications := 1
			if version > 1 {
				expectedApplications = 2
			}
			limit := 45
			if blocked {
				limit = 8
			}
			for i := 0; i < limit; i++ {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
				if err := admin.Get(ctx, key, c); err != nil {
					t.Fatal(err)
				}
				current := journalState(t, a)
				if current.Operation == nil && len(current.ActionApplications) == expectedActions && len(current.CredentialApplications) == expectedApplications && c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					settled = true
					break
				}
			}
			if blocked {
				current := journalState(t, a)
				if settled || current.Operation != nil || len(current.ActionApplications) != expectedActions || len(current.CredentialApplications) != 1 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("removed delivered credential version was reused", c.Status.Conditions)
				}
				return
			}
			if !settled {
				t.Fatal("first projected revision did not settle", c.Status.Conditions, journalState(t, a).Operation)
			}
			current := journalState(t, a)
			if !reflect.DeepEqual(current.ActionApplications[:1], before.ActionApplications) || len(current.CredentialApplications[0].Versions) != 1 || current.CredentialApplications[0].Versions[0].Version != 1 {
				t.Fatal("first projection lost metadata ancestor or invented floor")
			}
			if projected && (len(current.CredentialApplications[len(current.CredentialApplications)-1].Versions) != 1 || current.CredentialApplications[len(current.CredentialApplications)-1].Versions[0].Version != version) {
				t.Fatal("latest delivered version was not retained exactly")
			}

		}
		if fromVersioned {
			revise(false, 0, 3, false)
		} else {
			revise(true, 1, 3, false)
		}
		expectedDelivery = 1
		transitionActions = 2
		if fromVersioned {
			revise(true, 2, 5, false)
			transitionActions = 4
			expectedDelivery = 2
		}
		if mode == "projected-return-to-metadata" || mode == "projected-reintroduce" || mode == "projected-reuse" {
			revise(false, 0, 5, false)
			transitionActions = 4
		}
		if mode == "projected-reintroduce" {
			revise(true, 2, 7, false)
			transitionActions = 6
			expectedDelivery = 2
		}
		if mode == "projected-reuse" {
			priorPosts, priorDeletes := posts, deletes
			revise(true, 1, 5, true)
			if posts != priorPosts || deletes != priorDeletes {
				t.Fatal("version reuse mutated retained native job")
			}
		}
	}
	if retain {
		retained := journalState(t, a).DeepCopy()
		expectedPosts := 1 + transitionActions/2
		expectedDeletes := transitionActions / 2
		if prepared {
			expectedPosts = 0
			expectedDeletes = 0
		}
		if posts != expectedPosts || deletes != expectedDeletes || present == prepared || len(c.Finalizers) != 0 || len(retained.CredentialApplications) != expectedDelivery {
			t.Fatal("Retain lifecycle acquired cleanup authority or lost its native state")
		}
		if !prepared && (retained.Operation != nil || len(retained.ActionApplications) != 1+transitionActions) {
			t.Fatal("Retain audit missing")
		}
		spec := c.Spec.DeepCopy()
		if err := admin.Delete(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, &configapi.NetworkControllerConfig{}); !apierrors.IsNotFound(err) {
			t.Fatal("Retain config acquired a deletion finalizer", err)
		}
		for i := 0; i < 3; i++ {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
		}
		if posts != expectedPosts || deletes != expectedDeletes || !reflect.DeepEqual(retained, journalState(t, a)) {
			t.Fatal("manifest deletion changed retained native work or audit")
		}
		successor := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Spec: *spec}
		if install == nil {
			successor.UID = "unqualified-retain-successor"
			successor.Generation = 1
		}
		if err := admin.Create(ctx, successor); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
		}
		if err := admin.Get(ctx, key, successor); err != nil {
			t.Fatal(err)
		}
		if posts != expectedPosts || deletes != expectedDeletes || !reflect.DeepEqual(retained, journalState(t, a)) || len(successor.Finalizers) != 0 || successor.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("new UID inherited a retained reservation or outcome", successor.Status.Conditions)
		}
		return
	}
	if mode == "prepared-source-loss" {
		if err := admin.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "prepared-native-conflict" {
		row = discoveryMetadata{ID: "foreign-job", Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH"}
		present = true
	}
	if mode == "prepared-scope-loss" {
		meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
		if err := admin.Status().Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	a.statusWriter = worker
	committedFaults := 0
	if strings.HasPrefix(mode, "prepared-committed-source-loss") {
		a.statusWriter = credentialUnsubmittedFaultClient{Client: worker, mode: "drain-prepared-retained-source-loss", hits: &committedFaults, afterCommit: func() error { return admin.Delete(ctx, cm) }}
	}
	closed := false
	for i := 0; i < 35; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if discoveryDeletionCompleted(nc, c) {
			closed = true
			break
		}
	}
	state := journalState(t, a)
	if mode == "prepared-source-loss" || mode == "prepared-native-conflict" || mode == "prepared-scope-loss" {
		if closed || posts != 0 || deletes != 0 || state.Operation == nil || !reflect.DeepEqual(state.Operation, before.Operation) || !hasDiscoveryConfigFinalizer(c) {
			t.Fatal("source loss released unproven preparation")
		}
		return
	}
	if strings.HasPrefix(mode, "prepared-committed-source-loss") && committedFaults != 1 {
		t.Fatal("lost committed closure/source-loss was not exercised")
	}
	if !closed {
		t.Fatal("initial owner cleanup did not complete", c.Status.Conditions, state.Operation)
	}
	expectedActions, expectedWrites := 2+transitionActions, 1+transitionActions/2
	if prepared {
		expectedActions = 0
		expectedWrites = 0
	}
	if posts != expectedWrites || deletes != expectedWrites || present || other != 0 || state.Operation != nil || len(state.CredentialApplications) != expectedDelivery || len(state.ActionApplications) != expectedActions {
		t.Fatal("cleanup lost metadata audit or invented delivery", posts, deletes, state)
	}
	if prepared {
		nc, _ := a.currentController(ctx)
		cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
		if cond == nil || !strings.Contains(cond.Message, "unsubmitted") {
			t.Fatal("missing canonical cancellation witness")
		}
	}
	snapshot := state.DeepCopy()
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: a.statusReader}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || c.Finalizers[0] != "example.com/keeper" || !reflect.DeepEqual(snapshot, journalState(t, a)) {
		t.Fatal("manager release changed custody/audit", c.Finalizers)
	}
	if strings.Contains(mode, "handoff") {
		a.statusWriter = admin
		exerciseDiscoveryCredentialHandoffObservation(t, a, c, "custody-public-cleanup-inline", install != nil, []configapi.NetworkControllerSecretRef{{Section: "inventory", Path: "/discovery/0/password_list", Source: "initial-password"}})
		after := journalState(t, a)
		if len(after.ActionApplications) != expectedActions+2 || len(after.CredentialApplications) != 1 || len(after.CredentialApplications[0].Versions) != 1 || after.CredentialApplications[0].Versions[0].Version != 1 {
			t.Fatal("metadata/prepared predecessor invented a delivered version floor", after)
		}
	}

}
