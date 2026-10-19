// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialCustodyExecutionModes = []string{"complete", "configmap", "known-unknown", "lost-accepted-ack", "lost-permit-ack", "lost-receipt-ack", "ambiguous-create", "configmap-source-lost", "archive-return", "archive-return-after-submit", "missing-custody", "missing-archive", "changed-seal", "changed-inputs", "missing-scope"}

func TestDiscoveryCredentialCustodyExecution(t *testing.T) {
	for _, mode := range credentialCustodyExecutionModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-execute-"+mode, nil)
		})
	}
}

func exerciseDiscoveryCredentialCustodyExecution(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, admin ctrlclient.Client, realAPI bool) {
	t.Helper()
	revisionMode := ""
	if strings.HasPrefix(mode, "revision-") {
		revisionMode = strings.TrimPrefix(mode, "revision-")
		mode = "complete"
		if strings.Contains(revisionMode, "configmap") {
			mode = "configmap"
		}
	}
	cancelMode := ""
	if strings.HasPrefix(mode, "cancel-") {
		cancelMode = strings.TrimPrefix(mode, "cancel-")
		mode = "complete"
		if strings.Contains(cancelMode, "source") || strings.Contains(cancelMode, "configmap") {
			mode = "configmap"
		}
	}
	cleanupMode := ""
	if strings.HasPrefix(mode, "cleanup-") {
		cleanupMode = strings.TrimPrefix(mode, "cleanup-")
		mode = "complete"
		if strings.HasPrefix(cleanupMode, "configmap") {
			mode = "configmap"
		}
	}
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	before, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldActions, oldCredentials := len(before.Status.Configuration.ActionApplications), len(before.Status.Configuration.CredentialApplications)
	nativeID, taskID := fmt.Sprintf("successor-job-%d", oldCredentials+1), fmt.Sprintf("successor-task-%d", oldCredentials+1)
	creators, expectedVersion := 0, int64(1)
	for _, row := range before.Status.Configuration.ActionApplications {
		if discoveryCredentialReceiptSelected(row.Resource) {
			creators++
		}
	}
	for _, app := range before.Status.Configuration.CredentialApplications {
		for _, v := range app.Versions {
			if v.Version >= expectedVersion {
				expectedVersion = v.Version + 1
			}
		}
	}
	if oldActions != 2*creators {
		t.Fatal("missing qualified predecessor")
	}
	if mode == "missing-custody" || mode == "missing-archive" || mode == "changed-seal" || mode == "changed-inputs" {
		copy := before.DeepCopy()
		cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
		var record discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &record)
		switch mode {
		case "missing-custody":
			record.CredentialCustody = nil
		case "missing-archive":
			copy.Status.Configuration.ActionApplications = nil
			copy.Status.Configuration.CredentialApplications = nil
		case "changed-seal":
			record.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)
		case "changed-inputs":
			record.CredentialCustody.InputsDigest = strings.Repeat("a", 64)
		}
		raw, _ := json.Marshal(record)
		cond.Message = string(raw)
		if err := admin.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	}
	posts, otherWrites := 0, 0
	present := false
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
				rows = append(rows, discoveryMetadata{ID: nativeID, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"})
			}
			if mode == "archive-return" || mode == "archive-return-after-submit" && present {
				rows = append(rows, discoveryMetadata{ID: "job-id", Name: "returned-predecessor", Type: "SINGLE"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			posts++
			present = true
			started = time.Now().UnixMilli()
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["name"] != "fixture" || !reflect.DeepEqual(body["passwordList"], []any{discoveryCredentialMarker}) {
				t.Error("wrong successor credential request")
			}
			if mode == "configmap-source-lost" {
				if cm == nil {
					t.Error("CM source-loss case did not use ConfigMap")
				} else if err := admin.Delete(ctx, cm); err != nil {
					t.Error(err)
				}
			}
			if mode == "ambiguous-create" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": taskID}})
			}
		case r.Method == "GET" && r.URL.Path == taskPath+taskID:
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": taskID, "startTime": started, "endTime": started, "isError": false, "data": fmt.Sprintf(`{"discoveryId":%q}`, nativeID)}})
		default:
			otherWrites++
			t.Error("unexpected successor request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	worker := statusOnlyFeatureClient{Client: admin}
	a.statusWriter = worker
	preparationFaults := 0
	if cancelMode != "" {
		a.statusWriter = publicCredentialPreparedFaultClient{worker, &preparationFaults}
	}
	faults := 0
	if mode == "lost-accepted-ack" || mode == "lost-permit-ack" {
		phase := "Accepted"
		if mode == "lost-permit-ack" {
			phase = "Submitting"
		}
		a.statusWriter = rotatingSecretJournalClient{Client: worker, phase: phase, rotate: func() error { faults++; return errors.New("lost successor journal acknowledgement") }}
	}
	if mode == "lost-receipt-ack" {
		a.statusWriter = discoveryCredentialCompletionClient{Client: worker, mode: "lost-ack", attempts: &faults}
	}
	defer func() { a.statusWriter = admin }()
	success := mode == "complete" || mode == "configmap" || mode == "known-unknown" || mode == "lost-accepted-ack" || mode == "lost-receipt-ack" || mode == "configmap-source-lost"
	sawUnknown, scopeRemoved := false, false
	complete := false
	for i := 0; i < 35; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if cancelMode != "" && state.Operation != nil && state.Operation.Phase == "Prepared" {
			if posts != 0 || preparationFaults != 1 {
				t.Fatal("cancellation did not stop before dispatch")
			}
			a.statusWriter = admin
			exerciseDiscoveryCredentialCustodyCancellation(t, a, c, cancelMode, cm, admin, realAPI)
			return
		}
		if strings.HasPrefix(cleanupMode, "drain-") && state.Operation != nil && state.Operation.Phase == "Accepted" {
			a.statusWriter = admin
			exerciseDiscoveryCredentialCustodyCleanup(t, a, c, cleanupMode, cm, admin, nativeID, realAPI)
			return
		}
		if mode == "missing-scope" && posts == 1 && !scopeRemoved {
			meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
			if err := admin.Status().Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			scopeRemoved = true
		}
		if state.Operation != nil && state.Operation.Phase == "Unknown" {
			sawUnknown = true
		}
		if state.Operation == nil && len(state.CredentialApplications) == oldCredentials+1 {
			complete = true
			break
		}
		if !success && i >= 10 {
			break
		}
	}
	state := journalState(t, a)
	expectedPosts := 1
	if mode == "lost-permit-ack" || mode == "archive-return" || mode == "missing-custody" || mode == "missing-archive" || mode == "changed-seal" || mode == "changed-inputs" {
		expectedPosts = 0
	}
	if posts != expectedPosts || otherWrites != 0 || complete != success {
		t.Fatal("successor flow replayed, lost its fence or failed", mode, posts, c.Status.Conditions, state.Operation)
	}
	if success {
		if oldActions > 0 && !reflect.DeepEqual(state.ActionApplications[:oldActions], before.Status.Configuration.ActionApplications) || oldCredentials > 0 && !reflect.DeepEqual(state.CredentialApplications[:oldCredentials], before.Status.Configuration.CredentialApplications) || len(state.ActionApplications) != oldActions+1 || state.CredentialApplications[oldCredentials].Versions[0].Version != expectedVersion || state.CredentialApplications[oldCredentials].ConfigUID != string(c.UID) {
			t.Fatal("successor changed archived history or delivery floor")
		}
		if mode == "configmap-source-lost" {
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("retained completion claimed current source convergence")
			}
		} else if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			// Slot release precedes the final current-source no-op.
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("successor did not converge", c.Status.Conditions)
			}
		}
	} else {
		if mode != "missing-archive" && !reflect.DeepEqual(state.CredentialApplications, before.Status.Configuration.CredentialApplications) {
			t.Fatal("unverified successor fabricated credential delivery")
		}
		if expectedPosts == 1 && state.Operation == nil {
			t.Fatal("uncertain or unverified successor lost original operation")
		}
	}
	if mode == "known-unknown" && !sawUnknown {
		t.Fatal("original-task timeout recovery not exercised")
	}
	if strings.HasPrefix(mode, "lost-") && faults != 1 {
		t.Fatal("acknowledgement fault not exercised", faults)
	}
	baseline := state.DeepCopy()
	for i := 0; i < 3; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	if posts != expectedPosts || !reflect.DeepEqual(baseline, journalState(t, a)) {
		t.Fatal("restarted successor replayed or changed retained evidence")
	}
	if revisionMode != "" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialRevision(t, a, c, revisionMode, cm, admin, realAPI, nativeID)
	}
	if cleanupMode != "" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialCustodyCleanup(t, a, c, cleanupMode, cm, admin, nativeID, realAPI)
	}
}
