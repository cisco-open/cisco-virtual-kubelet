// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
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
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialCustodyCleanupModes = []string{"inline", "drain-accepted", "drain-unknown", "guard-seal-change", "configmap", "configmap-source-loss", "known-unknown", "lost-accepted-ack", "lost-permit-ack", "lost-retirement-ack", "lost-closure-ack", "lost-completion-ack", "ambiguous-delete", "returned-archive", "returned-current", "missing-custody", "changed-seal", "missing-archive", "missing-scope", "changed-spec", "configmap-source-changed", "missing-projection", "missing-scope-after-submit", "later-handoff"}

func TestDiscoveryCredentialCustodyCleanup(t *testing.T) {
	for _, mode := range credentialCustodyCleanupModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-execute-cleanup-"+mode, nil)
		})
	}
}

type credentialCustodyCleanupFaultClient struct {
	ctrlclient.Client
	mode, uid string
	hits      *int
}
type credentialCustodyCleanupFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner credentialCustodyCleanupFaultClient
}

func (c credentialCustodyCleanupFaultClient) Status() ctrlclient.SubResourceWriter {
	return credentialCustodyCleanupFaultWriter{c.Client.Status(), c}
}
func (w credentialCustodyCleanupFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	match := false
	if *w.owner.hits == 0 {
		switch o := obj.(type) {
		case *cvk.NetworkController:
			if o.Status.Configuration != nil && o.Status.Configuration.Operation != nil {
				op := o.Status.Configuration.Operation
				match = op.ConfigUID == w.owner.uid && op.Action == "Delete" && (w.owner.mode == "lost-accepted-ack" && op.Phase == "Accepted" || w.owner.mode == "lost-permit-ack" && op.Phase == "Submitting" || w.owner.mode == "lost-retirement-ack" && op.Phase == "Verified")
				if op.ConfigUID == w.owner.uid && op.Action == "Create" && strings.HasPrefix(op.Resource, discoveryCredentialMetadataScopePrefix) {
					match = w.owner.mode == "lost-metadata-accepted-ack" && op.Phase == "Accepted" || w.owner.mode == "lost-metadata-permit-ack" && op.Phase == "Submitting" || (w.owner.mode == "lost-metadata-receipt-ack" || w.owner.mode == "rejected-metadata-receipt") && op.Phase == "Verified"
					if match && op.Phase == "Verified" && !discoveryCredentialReceiptPairPresent(o.Status.Configuration, op) {
						return errors.New("non-atomic metadata-only discovery receipt")
					}
				}
			}
			if w.owner.mode == "lost-closure-ack" {
				cond := meta.FindStatusCondition(o.Status.Conditions, discoveryRetirementCondition)
				var r discoveryOwnershipRetirement
				match = cond != nil && json.Unmarshal([]byte(cond.Message), &r) == nil && r.Completion.ConfigUID == w.owner.uid && r.CredentialCustody == nil
			}
		case *configapi.NetworkControllerConfig:
			cond := meta.FindStatusCondition(o.Status.Conditions, discoveryConfigCleanupCondition)
			match = w.owner.mode == "lost-completion-ack" && string(o.UID) == w.owner.uid && cond != nil && cond.Reason == "DiscoveryConfigFinalized"
		}
	}
	if match && w.owner.mode == "rejected-metadata-receipt" {
		*w.owner.hits++
		return errors.New("rejected metadata-only receipt before persistence")
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if match {
		*w.owner.hits++
		return errors.New("lost successor cleanup acknowledgement")
	}
	return nil
}

func exerciseDiscoveryCredentialCustodyCleanup(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, admin ctrlclient.Client, nativeID string, realAPI bool) {
	t.Helper()
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	before, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline := before.Status.Configuration.DeepCopy()
	originalCreate := baseline.Operation.DeepCopy()
	baseline.Operation = nil
	if mode == "drain-unknown" {
		if originalCreate == nil {
			t.Fatal("missing original acknowledged Create")
		}
		if err := a.advanceConfiguration(ctx, originalCreate.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
	}
	deleteTaskID := fmt.Sprintf("successor-delete-%d", len(baseline.CredentialApplications)+1)
	createTaskReads := 0
	guardChanged := false
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "missing-custody", "changed-seal", "missing-archive":
		copy := before.DeepCopy()
		cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
		var r discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &r)
		if mode == "missing-custody" {
			r.CredentialCustody = nil
		}
		if mode == "changed-seal" {
			r.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)
		}
		if mode == "missing-archive" {
			copy.Status.Configuration.ActionApplications = copy.Status.Configuration.ActionApplications[2:]
			copy.Status.Configuration.CredentialApplications = copy.Status.Configuration.CredentialApplications[1:]
		}
		raw, _ := json.Marshal(r)
		cond.Message = string(raw)
		if err := admin.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	case "missing-scope":
		meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
		if err := admin.Status().Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	case "changed-spec":
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	case "configmap-source-changed":
		cm.Data["model"] += " "
		if err := admin.Update(ctx, cm); err != nil {
			t.Fatal(err)
		}
	case "missing-projection":
		if err := filepath.Walk(a.intentSecretPath, func(p string, i os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if !i.IsDir() {
				return os.Remove(p)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	deletes, writes, taskReads := 0, 0, 0
	present := true
	var started int64
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			if mode == "guard-seal-change" && !guardChanged {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
				} else if current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Submitting" {
					cond := meta.FindStatusCondition(current.Status.Conditions, discoveryRetirementCondition)
					var record discoveryOwnershipRetirement
					json.Unmarshal([]byte(cond.Message), &record)
					record.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)
					raw, _ := json.Marshal(record)
					cond.Message = string(raw)
					if err := admin.Status().Update(ctx, current); err != nil {
						t.Error(err)
					}
					guardChanged = true
				}
			}
			rows := []discoveryMetadata{}
			if present || mode == "returned-current" && deletes > 0 {
				rows = append(rows, discoveryMetadata{ID: nativeID, Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"})
			}
			if mode == "returned-archive" {
				rows = append(rows, discoveryMetadata{ID: "job-id", Name: "reappeared", Type: "SINGLE"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "DELETE" && r.URL.Path == discoveryPath+"/"+nativeID:
			deletes++
			present = false
			started = time.Now().UnixMilli()
			if mode == "configmap-source-loss" {
				if err := admin.Delete(ctx, cm); err != nil {
					t.Error(err)
				}
			}
			if mode == "ambiguous-delete" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": deleteTaskID}})
			}
		case r.Method == "GET" && r.URL.Path == taskPath+deleteTaskID:
			taskReads++
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": deleteTaskID, "startTime": started, "endTime": started, "isError": false}})
		case r.Method == "GET" && originalCreate != nil && r.URL.Path == taskPath+originalCreate.TaskID:
			createTaskReads++
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": originalCreate.TaskID, "startTime": originalCreate.SubmittedAt.UnixMilli(), "endTime": time.Now().UnixMilli(), "isError": false, "data": fmt.Sprintf(`{"discoveryId":%q}`, nativeID)}})
		default:
			writes++
			t.Error("unexpected cleanup request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	hits := 0
	worker := statusOnlyFeatureClient{Client: admin}
	a.statusWriter = worker
	if strings.HasPrefix(mode, "lost-") {
		a.statusWriter = credentialCustodyCleanupFaultClient{worker, mode, string(c.UID), &hits}
	}
	defer func() { a.statusWriter = admin }()
	success := strings.HasPrefix(mode, "drain-") || mode == "inline" || mode == "configmap" || mode == "configmap-source-loss" || mode == "known-unknown" || strings.HasPrefix(mode, "lost-") && mode != "lost-permit-ack" || mode == "later-handoff"
	complete, sawUnknown, scopeRemoved := false, false, false
	for i := 0; i < 45; i++ {
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
		op := nc.Status.Configuration.Operation
		if mode == "known-unknown" && !sawUnknown && op != nil && op.Phase == "Accepted" {
			if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			sawUnknown = true
		}
		if mode == "missing-scope-after-submit" && deletes == 1 && !scopeRemoved {
			meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
			if err := admin.Status().Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			scopeRemoved = true
		}
		if discoveryDeletionCompleted(nc, c) {
			complete = true
			break
		}
		if !success && i >= 12 {
			break
		}
	}
	expectedDeletes := 1
	switch mode {
	case "guard-seal-change", "returned-archive", "missing-custody", "changed-seal", "missing-archive", "missing-scope", "changed-spec", "configmap-source-changed", "missing-projection", "lost-permit-ack":
		expectedDeletes = 0
	}
	if complete != success || deletes != expectedDeletes || writes != 0 {
		t.Fatal("successor cleanup failed or replayed", mode, deletes, c.Status.Conditions, journalState(t, a).Operation)
	}
	if mode == "guard-seal-change" && !guardChanged {
		t.Fatal("post-permit native snapshot race not exercised")
	}
	if strings.HasPrefix(mode, "lost-") && hits != 1 {
		t.Fatal("fault not exercised", hits)
	}
	if mode == "known-unknown" && !sawUnknown {
		t.Fatal("original Unknown task not recovered")
	}
	closed, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := closed.Status.Configuration
	if !success {
		if hasDiscoveryConfigFinalizer(c) == false || !reflect.DeepEqual(state.CredentialApplications, baseline.CredentialApplications) && mode != "missing-archive" {
			t.Fatal("unverified cleanup released or fabricated delivery")
		}
		if deletes == 1 && state.Operation == nil {
			t.Fatal("uncertain original deletion lost its fence")
		}
		return
	}
	newActions, newCredentials := 1, 0
	if originalCreate != nil {
		newActions, newCredentials = 2, 1
		if createTaskReads == 0 || state.ActionApplications[len(baseline.ActionApplications)].Operation.ID != originalCreate.ID || state.ActionApplications[len(baseline.ActionApplications)].Operation.TaskID != originalCreate.TaskID {
			t.Fatal("pre-deletion Create was not completed through its original task")
		}
	}
	if state.Operation != nil || len(state.ActionApplications) != len(baseline.ActionApplications)+newActions || !reflect.DeepEqual(state.ActionApplications[:len(baseline.ActionApplications)], baseline.ActionApplications) || len(state.CredentialApplications) != len(baseline.CredentialApplications)+newCredentials || !reflect.DeepEqual(state.CredentialApplications[:len(baseline.CredentialApplications)], baseline.CredentialApplications) || !reflect.DeepEqual(state.Owners, baseline.Owners) {
		t.Fatal("cleanup rewrote or lost custody audit")
	}
	record, history, err := discoveryCredentialRetiredHandoffRecord(closed)
	archivedOwners := map[string]bool{}
	for _, app := range state.ActionApplications {
		if discoveryCredentialReceiptSelected(app.Resource) && app.Operation.ConfigUID != string(c.UID) {
			archivedOwners[app.Operation.ConfigUID] = true
		}
	}
	if err != nil || record.Completion.ConfigUID != string(c.UID) || record.CredentialCustody != nil || record.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+discoveryCredentialAuditDigest(state, "") || len(history.archives) != len(archivedOwners) {
		t.Fatal("new complete sealed closure missing", err)
	}
	for _, damage := range []string{"archive-row", "archive-pair", "seal", "completion"} {
		copy := closed.DeepCopy()
		cond := meta.FindStatusCondition(copy.Status.Conditions, discoveryRetirementCondition)
		var r discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &r)
		switch damage {
		case "archive-row":
			copy.Status.Configuration.ActionApplications = copy.Status.Configuration.ActionApplications[2:]
		case "archive-pair":
			copy.Status.Configuration.CredentialApplications = copy.Status.Configuration.CredentialApplications[1:]
		case "seal":
			r.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)
		case "completion":
			r.Completion.ConfigUID = "foreign"
		}
		raw, _ := json.Marshal(r)
		cond.Message = string(raw)
		if discoveryDeletionCompleted(copy, c) {
			t.Fatal("manager trusted damaged multi-owner closure", damage)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
	}
	if deletes != 1 || !reflect.DeepEqual(state, journalState(t, a)) {
		t.Fatal("closed cleanup was replayed")
	}
	reads := taskReads
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || c.Finalizers[0] != "example.com/keeper" || taskReads != reads || !reflect.DeepEqual(state, journalState(t, a)) {
		t.Fatal("manager release changed audit/native work or unrelated finalizer")
	}
	if mode == "later-handoff" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialHandoffObservation(t, a, c, "custody-execute-cleanup-inline", realAPI)
		after := journalState(t, a)
		if len(after.ActionApplications) != 6 || len(after.CredentialApplications) != 3 || after.CredentialApplications[2].Versions[0].Version != 3 {
			t.Fatal("third-owner execution did not preserve complete lineage")
		}
		var previous configapi.NetworkControllerConfig
		if err := admin.Get(ctx, key, &previous); !apierrors.IsNotFound(err) {
			t.Fatal("previous owner not gone", err)
		}
	}
}
