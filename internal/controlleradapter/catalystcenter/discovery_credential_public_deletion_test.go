// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialPublicDeletionModes = []string{"inline", "configmap", "active", "known-unknown", "drain-accepted", "drain-unknown", "drain-verified", "drain-source-return", "drain-projection-return", "drain-prepared", "drain-submitting", "drain-taskless", "drain-pending", "drain-failed", "drain-foreign-task", "drain-scope-missing", "drain-spec-change", "drain-retirement", "cleanup-preflight-replan"}

func TestDiscoveryCredentialPublicConfigDeletion(t *testing.T) {
	for _, mode := range credentialPublicDeletionModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, "public/"+mode, nil) })
	}
}

func enrollPublicCredentialFixture(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, posts *int) {
	t.Helper()
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{"example.com/keeper"}
	if mode == "drain-unknown" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if supportedConfigurationApply(c) {
		t.Fatal("unenrolled Delete policy admitted mutation")
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if *posts != 0 || hasDiscoveryConfigFinalizer(c) {
		t.Fatal("worker mutated or enrolled itself")
	}
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{a.statusWriter}, APIReader: a.statusReader}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("manager enrollment", err)
	}
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if !hasDiscoveryConfigFinalizer(c) || hasFeatureConfigFinalizer(c) || !supportedConfigurationApply(c) || *posts != 0 {
		t.Fatal("wrong public cleanup enrollment", c.Finalizers)
	}
}

func exercisePublicCredentialDeletion(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, cm *core.ConfigMap, paths map[string]string, posts, deletes, gets *int, realAPI bool) {
	t.Helper()
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	admin := a.statusWriter
	state := journalState(t, a).DeepCopy()
	if strings.HasPrefix(mode, "drain-prepared") {
		exerciseUnsubmittedCredentialDeletion(t, a, c, mode, cm, paths, posts, deletes, gets, realAPI)
		return
	}
	originalID, originalTask := "", ""
	if state.Operation != nil {
		originalID = state.Operation.ID
		originalTask = state.Operation.TaskID
	}
	if strings.HasPrefix(mode, "drain-") && originalID == "" {
		t.Fatal("deletion interruption was not exercised")
	}
	if mode == "drain-source-return" {
		if err := admin.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-projection-return" {
		if err := os.Remove(paths["passwordList"]); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-scope-missing" {
		meta.RemoveStatusCondition(&c.Status.Conditions, discoveryCredentialSourceCondition)
		if err := admin.Status().Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "drain-spec-change" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		c.Generation++
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	blocked := mode == "drain-prepared" || mode == "drain-submitting" || mode == "drain-taskless" || mode == "drain-pending" || mode == "drain-failed" || mode == "drain-foreign-task" || mode == "drain-scope-missing" || mode == "drain-spec-change"
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	defer func() { a.statusWriter = admin }()
	restored := false
	complete := false
	for i := 0; i < 40; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		current := journalState(t, a)
		if (mode == "drain-source-return" || mode == "drain-projection-return") && !restored && current.Operation == nil {
			if len(current.CredentialApplications) != 1 || current.CredentialApplications[0].OperationID != originalID || *deletes != 0 {
				t.Fatal("original create was not independently drained before restoring source")
			}
			if mode == "drain-source-return" {
				replacement := cm.DeepCopy()
				replacement.UID = ""
				replacement.ResourceVersion = ""
				if cm.UID == "cleanup-model" {
					replacement.UID = "restored-cleanup-model"
				}
				if err := admin.Create(ctx, replacement); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 1, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			restored = true
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if discoveryDeletionCompleted(nc, c) {
			complete = true
			break
		}
		if blocked && i >= 11 {
			break
		}
	}
	expectedPosts := 1
	if mode == "drain-prepared" || mode == "drain-submitting" {
		expectedPosts = 0
	}
	if *posts != expectedPosts {
		t.Fatal("deletion replayed Create", *posts)
	}
	if blocked {
		current := journalState(t, a)
		if complete || !hasDiscoveryConfigFinalizer(c) || *deletes != 0 || current.Operation == nil || current.Operation.ID != originalID || current.Operation.TaskID != originalTask || !reflect.DeepEqual(current.Owners, state.Owners) {
			t.Fatal("uncertain pre-deletion work was released or replayed", current.Operation)
		}
		return
	}
	if !complete || *deletes != 1 {
		t.Fatal("public cleanup did not complete", c.Status.Conditions, journalState(t, a).Operation, *deletes)
	}
	if (mode == "drain-source-return" || mode == "drain-projection-return") && !restored {
		t.Fatal("source restoration was not exercised")
	}
	closed := journalState(t, a).DeepCopy()
	if closed.Operation != nil || len(closed.CredentialApplications) != 1 || len(closed.ActionApplications) != 2 || !reflect.DeepEqual(closed.Owners, state.Owners) {
		t.Fatal("public cleanup lost audit or claimed credential delivery")
	}
	index := 0
	if mode == "drain-retirement" {
		index = 1
	}
	if originalID != "" && (closed.ActionApplications[index].Operation.ID != originalID || closed.ActionApplications[index].Operation.TaskID != originalTask) {
		t.Fatal("deletion rewrote the original operation receipt")
	}
	if len(state.CredentialApplications) > 0 && !reflect.DeepEqual(state.CredentialApplications, closed.CredentialApplications) {
		t.Fatal("deletion changed previously delivered credential versions")
	}
	reads := *gets
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: a.statusReader}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("manager release", err)
	}
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || !slices.Contains(c.Finalizers, "example.com/keeper") || *gets != reads || !reflect.DeepEqual(closed, journalState(t, a)) {
		t.Fatal("public manager finalization changed native work or retained history", c.Finalizers)
	}
}

// Stop at the actual durable boundary; the normal apply loop otherwise prepares
// and submits within one reconcile. A lost preparation acknowledgement must not
// be interpreted as a new submission permit.
type publicCredentialPreparedFaultClient struct {
	ctrlclient.Client
	hits *int
}
type publicCredentialPreparedFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner publicCredentialPreparedFaultClient
}

func (c publicCredentialPreparedFaultClient) Status() ctrlclient.SubResourceWriter {
	return publicCredentialPreparedFaultWriter{c.Client.Status(), c}
}
func (w publicCredentialPreparedFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || *w.owner.hits > 0 || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Prepared" || !discoveryCredentialInputSelected(nc.Status.Configuration.Operation.Resource) {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.owner.hits++
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("interrupted after durable preparation")
}

func protectCredentialFixtureController(t *testing.T, a *adapter) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Finalizers = append(nc.Finalizers, "cisco.vk/network-controller-protection")
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, a.key, nc); err != nil {
		t.Fatal(err)
	}
	a.generation = nc.Generation
}

var credentialPublicCohortModes = []string{"public-delete", "public-delete-conflict", "public-delete-mixed"}

func TestDiscoveryCredentialPublicDeletionCohort(t *testing.T) {
	for _, mode := range credentialPublicCohortModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialMultipleScenario(t, mode, nil) })
	}
}
func exercisePublicCredentialCohortDeletion(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, jobs map[string]discoveryMetadata, posts, deletes *int) {
	t.Helper()
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	admin := a.statusWriter
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if mode == "public-delete-conflict" {
		job := jobs["another"]
		job.Usernames = []string{"foreign"}
		jobs["another"] = job
	}
	before := journalState(t, a).DeepCopy()
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	defer func() { a.statusWriter = admin }()
	complete := false
	for i := 0; i < 40; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if discoveryDeletionCompleted(nc, c) {
			complete = true
			break
		}
		if mode == "public-delete-conflict" && i >= 10 {
			break
		}
	}
	if mode == "public-delete-conflict" {
		if complete || *posts != 2 || *deletes != 0 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("late cohort conflict allowed partial deletion")
		}
		return
	}
	closed := journalState(t, a).DeepCopy()
	if !complete || *posts != 2 || *deletes != 2 || len(jobs) != 0 || closed.Operation != nil || len(closed.ActionApplications) != 4 || !reflect.DeepEqual(before.CredentialApplications, closed.CredentialApplications) || !reflect.DeepEqual(before.Owners, closed.Owners) || !reflect.DeepEqual(before.ActionApplications, closed.ActionApplications[:2]) {
		t.Fatal("two-job public cleanup replayed or lost audit", *posts, *deletes)
	}
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: a.statusReader}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || !slices.Contains(c.Finalizers, "example.com/keeper") || !reflect.DeepEqual(closed, journalState(t, a)) {
		t.Fatal("cohort finalization changed retained audit")
	}
}
