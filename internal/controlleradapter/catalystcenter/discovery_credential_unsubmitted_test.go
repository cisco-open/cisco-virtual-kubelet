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
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialUnsubmittedModes = []string{"drain-prepared", "drain-prepared-handoff", "drain-prepared-retained-source-loss", "drain-prepared-retained-projection-loss", "drain-prepared-configmap", "drain-prepared-source-loss", "drain-prepared-projection-loss", "drain-prepared-scope-loss", "drain-prepared-native-conflict", "drain-prepared-native-error", "drain-prepared-source-race", "drain-prepared-config-race", "drain-prepared-journal-race", "drain-prepared-task-contamination", "drain-prepared-credential-contamination", "drain-prepared-lost-retirement-ack", "drain-prepared-conflict-retirement", "drain-prepared-lost-completion-ack", "drain-prepared-conflict-completion"}

func TestDiscoveryCredentialUnsubmittedDeletion(t *testing.T) {
	for _, mode := range credentialUnsubmittedModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, "public/"+mode, nil) })
	}
}

type credentialUnsubmittedFaultClient struct {
	ctrlclient.Client
	mode        string
	hits        *int
	afterCommit func() error
}
type credentialUnsubmittedFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner credentialUnsubmittedFaultClient
}

func (c credentialUnsubmittedFaultClient) Status() ctrlclient.SubResourceWriter {
	return credentialUnsubmittedFaultWriter{c.Client.Status(), c}
}
func (w credentialUnsubmittedFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || *w.owner.hits > 0 || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil || !strings.Contains(cond.Message, discoveryCredentialUnsubmittedPrefix) {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.owner.hits++
	if w.owner.mode == "drain-prepared-conflict-retirement" {
		return errors.New("rejected atomic unsubmitted retirement")
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.owner.afterCommit != nil {
		if err := w.owner.afterCommit(); err != nil {
			return err
		}
	}
	return errors.New("lost atomic unsubmitted retirement acknowledgement")
}

func exerciseUnsubmittedCredentialDeletion(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, paths map[string]string, posts, deletes, gets *int, realAPI bool) {
	t.Helper()
	ctx := context.Background()
	admin := a.statusWriter
	key := ctrlclient.ObjectKeyFromObject(c)
	before := journalState(t, a).DeepCopy()
	if before.Operation == nil || before.Operation.Phase != "Prepared" || before.Operation.SubmittedAt != nil || before.Operation.TaskID != "" || *posts != 0 {
		t.Fatal("test did not stop at the real unsubmitted preparation")
	}
	if mode == "drain-prepared-source-loss" {
		if err := admin.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-prepared-projection-loss" {
		if err := os.Remove(paths["passwordList"]); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-prepared-scope-loss" {
		meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
		if err := admin.Status().Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-prepared-missing-seal" || mode == "drain-prepared-archive-loss" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "drain-prepared-archive-loss" {
			nc.Status.Configuration.ActionApplications = nil
			nc.Status.Configuration.CredentialApplications = nil
		} else {
			cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
			var record discoveryOwnershipRetirement
			if cond == nil || json.Unmarshal([]byte(cond.Message), &record) != nil {
				t.Fatal("missing active custody")
			}
			record.CredentialHistoryDigest = ""
			raw, _ := json.Marshal(record)
			cond.Message = string(raw)
		}
		if err := admin.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-prepared-task-contamination" || mode == "drain-prepared-credential-contamination" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "drain-prepared-task-contamination" {
			nc.Status.Configuration.Operation.TaskID = "foreign-task"
		} else {
			nc.Status.Configuration.CredentialApplications = append(nc.Status.Configuration.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: "inventory", ConfigUID: string(c.UID), OperationID: before.Operation.ID, Resource: before.Operation.Resource, TargetID: "foreign-job", Versions: before.Operation.CredentialVersions})
		}
		if err := admin.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	retainedNativeReturned := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != discoveryPath+"/1/100" {
			t.Error("unsubmitted cleanup attempted native mutation", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		reads++
		*gets++
		if mode == "drain-prepared-native-error" {
			w.WriteHeader(503)
			return
		}
		if reads == 1 {
			switch mode {
			case "drain-prepared-secret-replan":
				if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, before.Operation.CredentialVersions[0].Version, `["OTHER_SYNTHETIC_VALUE"]`), 0600); err != nil {
					t.Error(err)
				}
			case "drain-prepared-source-race":
				cm.Data["model"] += " "
				if err := admin.Update(ctx, cm); err != nil {
					t.Error(err)
				}
			case "drain-prepared-config-race":
				var fresh configapi.NetworkControllerConfig
				if err := admin.Get(ctx, key, &fresh); err != nil {
					t.Error(err)
					break
				}
				fresh.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
				if err := admin.Update(ctx, &fresh); err != nil {
					t.Error(err)
				}
			case "drain-prepared-journal-race":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					break
				}
				nc.Status.Configuration.Owners = nil
				if err := admin.Status().Update(ctx, nc); err != nil {
					t.Error(err)
				}
			}
		}
		rows := []discoveryMetadata{}
		if mode == "drain-prepared-returned-archive" {
			rows = append(rows, discoveryMetadata{ID: "job-id", Name: "returned-archived-job", Type: "SINGLE"})
		}
		if retainedNativeReturned {
			rows = append(rows, discoveryMetadata{ID: "foreign-after-cancellation", Name: "fixture", Type: "SINGLE"})
		}
		if mode == "drain-prepared-native-conflict" {
			rows = append(rows, discoveryMetadata{ID: "foreign-job", Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH"})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	worker := statusOnlyFeatureClient{Client: admin}
	a.statusWriter = worker
	hits := 0
	if strings.HasSuffix(mode, "retirement-ack") || strings.HasSuffix(mode, "conflict-retirement") || strings.HasPrefix(mode, "drain-prepared-retained-") {
		fault := credentialUnsubmittedFaultClient{Client: worker, mode: mode, hits: &hits}
		if strings.HasPrefix(mode, "drain-prepared-retained-") {
			fault.afterCommit = func() error {
				switch mode {
				case "drain-prepared-retained-source-loss":
					return admin.Delete(ctx, cm)
				case "drain-prepared-retained-projection-loss":
					return os.Remove(paths["passwordList"])
				case "drain-prepared-retained-native-return":
					retainedNativeReturned = true
				}
				return nil
			}
		}
		a.statusWriter = fault
	}
	if strings.HasSuffix(mode, "completion-ack") {
		a.statusWriter = credentialCompletionFaultClient{worker, "completion-lost-ack", &hits}
	}
	if strings.HasSuffix(mode, "conflict-completion") {
		a.statusWriter = credentialCompletionFaultClient{worker, "completion-conflict", &hits}
	}
	defer func() { a.statusWriter = admin }()
	success := mode == "drain-prepared-retained-source-loss" || mode == "drain-prepared-retained-projection-loss" || mode == "drain-prepared-secret-replan" || mode == "drain-prepared-handoff" || mode == "drain-prepared" || mode == "drain-prepared-configmap" || strings.Contains(mode, "-ack") || strings.Contains(mode, "conflict-retirement") || strings.Contains(mode, "conflict-completion")
	complete := false
	secretReplanRefused := false
	for i := 0; i < 8; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "drain-prepared-secret-replan" && reads > 0 && !secretReplanRefused {
			if discoveryDeletionCompleted(nc, c) || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.ID != before.Operation.ID {
				t.Fatal("changed projected snapshot was cancelled without revalidation")
			}
			secretReplanRefused = true
		}
		if discoveryDeletionCompleted(nc, c) {
			complete = true
			break
		}
	}
	if mode == "drain-prepared-secret-replan" && !secretReplanRefused {
		t.Fatal("same-version projection replan was not exercised")
	}
	if *posts != 0 || *deletes != 0 {
		t.Fatal("unsubmitted completion created native work")
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !success {
		if mode == "drain-prepared-retained-native-return" {
			record, err := discoveryCredentialUnsubmittedRecord(nc, c)
			if complete || !hasDiscoveryConfigFinalizer(c) || err != nil || !reflect.DeepEqual(record.UnsubmittedOperation, before.Operation) || nc.Status.Configuration.Operation != nil || !reflect.DeepEqual(nc.Status.Configuration.ActionApplications, before.ActionApplications) || !reflect.DeepEqual(nc.Status.Configuration.CredentialApplications, before.CredentialApplications) {
				t.Fatal("committed cancellation lost its native-absence fence", err)
			}
			return
		}
		if complete || !hasDiscoveryConfigFinalizer(c) || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.ID != before.Operation.ID {
			t.Fatal("unproven preparation was released")
		}
		return
	}
	if !complete || reads < 2 || nc.Status.Configuration.Operation != nil || !reflect.DeepEqual(nc.Status.Configuration.ActionApplications, before.ActionApplications) || !reflect.DeepEqual(nc.Status.Configuration.CredentialApplications, before.CredentialApplications) || !reflect.DeepEqual(nc.Status.Configuration.Owners, before.Owners) {
		t.Fatal("unsubmitted completion missing or fabricated delivery", c.Status.Conditions, nc.Status.Configuration)
	}
	record, err := discoveryCredentialUnsubmittedRecord(nc, c)
	if err != nil || !reflect.DeepEqual(record.UnsubmittedOperation, before.Operation) {
		t.Fatal("original preparation witness was lost", err)
	}
	legacy := nc.DeepCopy()
	legacyRecord := *record
	legacyRecord.CredentialHistoryDigest = ""
	legacyRaw, _ := json.Marshal(legacyRecord)
	meta.FindStatusCondition(legacy.Status.Conditions, discoveryRetirementCondition).Message = string(legacyRaw)
	if discoveryDeletionCompleted(legacy, c) != (len(before.CredentialApplications) == 0) {
		t.Fatal("legacy no-work closure escaped its empty-history scope")
	}
	if _, _, err := discoveryCredentialRetiredHandoffRecord(legacy); err == nil {
		t.Fatal("unsealed no-work record authorized handoff")
	}
	if strings.HasPrefix(mode, "drain-prepared-retained-") || strings.Contains(mode, "-ack") || strings.Contains(mode, "conflict-") {
		if hits != 1 {
			t.Fatal("acknowledgement fault was not exercised", hits)
		}
	}
	for _, mutation := range []string{"witness", "task", "phase", "source", "digest", "completion", "owner", "audit", "controller", "spec", "scope"} {
		altered := nc.DeepCopy()
		config := c.DeepCopy()
		r := *record
		r.UnsubmittedOperation = record.UnsubmittedOperation.DeepCopy()
		switch mutation {
		case "witness":
			r.UnsubmittedOperation = nil
		case "task":
			r.UnsubmittedOperation.TaskID = "foreign-task"
		case "phase":
			r.UnsubmittedOperation.Phase = "Submitting"
		case "source":
			r.UnsubmittedOperation.SourceVersion = "different"
		case "digest":
			r.RetainedDigest = strings.Repeat("a", 64)
		case "completion":
			meta.RemoveStatusCondition(&config.Status.Conditions, discoveryConfigCleanupCondition)
		case "owner":
			altered.Status.Configuration.Owners = nil
		case "audit":
			altered.Status.Configuration.CredentialApplications = append(altered.Status.Configuration.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{ConfigUID: string(c.UID), OperationID: before.Operation.ID})
		case "controller":
			altered.Spec.Paused = true
		case "spec":
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		case "scope":
			meta.RemoveStatusCondition(&config.Status.Conditions, discoveryCredentialSourceCondition)
		}
		raw, _ := json.Marshal(r)
		cond := meta.FindStatusCondition(altered.Status.Conditions, discoveryRetirementCondition)
		cond.Message = string(raw)
		if discoveryDeletionCompleted(altered, config) {
			t.Fatal("manager accepted altered unsubmitted witness", mutation)
		}
	}
	if _, err := decodeDiscoveryRetirement(nc); err == nil {
		t.Fatal("ordinary handoff accepted unsubmitted credential authority")
	}
	baseline := nc.Status.Configuration.DeepCopy()
	readBaseline := *gets
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: a.statusReader}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || c.Finalizers[0] != "example.com/keeper" || *gets != readBaseline || !reflect.DeepEqual(baseline, journalState(t, a)) {
		t.Fatal("manager release changed unrelated finalizer or retained audit")
	}
	if mode == "drain-prepared-handoff" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialHandoffObservation(t, a, c, "custody-public-cleanup-inline", realAPI)
	}
}
