// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Worker access is deliberately insufficient for config metadata/finalizers.
// Status subresource writes remain available through the embedded real client.
type statusOnlyFeatureClient struct{ ctrlclient.Client }

func (c statusOnlyFeatureClient) Update(context.Context, ctrlclient.Object, ...ctrlclient.UpdateOption) error {
	return errors.New("worker metadata update forbidden")
}
func (c statusOnlyFeatureClient) Patch(context.Context, ctrlclient.Object, ctrlclient.Patch, ...ctrlclient.PatchOption) error {
	return errors.New("worker metadata patch forbidden")
}
func (c statusOnlyFeatureClient) Delete(context.Context, ctrlclient.Object, ...ctrlclient.DeleteOption) error {
	return errors.New("worker metadata delete forbidden")
}

func TestWirelessFeatureConfigDeletion(t *testing.T) {
	for _, mode := range []string{"known", "retain", "referenced", "native-drift", "missing-task", "lost-ack", "tombstone-missing", "pruned", "source-gone", "inflight-update", "inflight-create", "inflight-taskless", "inflight-changed-source", "prepared", "prepared-source-gone", "prepared-bad-identity"} {
		t.Run(mode, func(t *testing.T) { exerciseFeatureConfigDeletion(t, mode, nil) })
	}
}
func exerciseFeatureConfigDeletion(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config, world := featurePruneFixture(t, install)
	admin := a.statusWriter
	key := ctrlclient.ObjectKeyFromObject(config)
	config.Generation++
	config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Finalizers = append(config.Finalizers, featureConfigFinalizer, "example.com/other")
	if !featureDeletionEligible(mustFeatureController(t, a), config) {
		t.Fatal("cleanup enrollment eligibility rejected")
	}
	if mode == "source-gone" || mode == "prepared-source-gone" {
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "feature-source", Namespace: config.Namespace, UID: "source-uid"}, Data: map[string]string{"model": string(config.Spec.Source.Inline.Raw)}}
		if err := admin.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}}
	}
	if mode == "retain" {
		config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if err := admin.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(mode, "inflight-") || strings.HasPrefix(mode, "prepared") {
		if mode != "prepared-source-gone" {
			config.Generation++
			config.Spec.Source.Inline.Raw = featurePruneModel(false)
			if mode == "inflight-create" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(featurePruneModel(false)), `"name":"FRA"`, `"name":"FRA_NEW"`, 1))
			}
			if err := admin.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(mode, "prepared") {
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			resource := featureUpdatePrefix + "fra:" + featureIdentityDigest("FRA") + ":" + featureIdentityDigest("prepared-effect")
			if _, err = a.prepareConfiguration(ctx, config, source, "feature_templates", resource, "Update", configurationResourceIdentity{target: featureTestFRAUUID}); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			nc := mustFeatureController(t, a)
			op := nc.Status.Configuration.Operation
			if op == nil || op.Phase != "Accepted" {
				t.Fatal("inflight fixture not accepted", op)
			}
			op.Phase = "Unknown"
			if mode == "inflight-taskless" {
				op.TaskID = ""
			}
			if err := admin.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "inflight-changed-source" {
		config.Generation++
		config.Spec.Source.Inline.Raw = featurePruneModel(true)
		if err := admin.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "prepared-bad-identity" {
		nc := mustFeatureController(t, a)
		nc.Status.Configuration.Operation.ConfigName = "other"
		if err := admin.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	beforeDeleteWrites := len(world.writes)
	if mode == "source-gone" || mode == "prepared-source-gone" {
		if err := admin.Delete(ctx, &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "feature-source", Namespace: config.Namespace}}); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "referenced" {
		world.profiles = []map[string]any{{"id": "profile", "wirelessProfileName": "P", "featureTemplates": []any{map[string]any{"id": featureTestCAUUID, "ssids": []any{}}}}}
	}
	if mode == "native-drift" {
		world.rows[featureTestCAUUID]["futureNativeField"] = true
	}
	if mode == "lost-ack" {
		world.mode = "lost-ack"
	}
	if err := admin.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	if config.DeletionTimestamp.IsZero() {
		t.Fatal("delete did not retain finalizer")
	}
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	recovered := map[string]bool{}
	corrupted := false
	for range 100 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		nc := mustFeatureController(t, a)
		state := nc.Status.Configuration
		if state != nil && state.Operation != nil {
			op := state.Operation
			if op.Phase == "Accepted" && op.Action == "Delete" && !recovered[op.ID] {
				if !featureFinalizeSelected(op.Resource) {
					t.Fatal("cleanup lacked deletion-bound intent", op.Resource)
				}
				op.Phase = "Unknown"
				if mode == "missing-task" {
					op.TaskID = ""
				}
				raw, err := json.Marshal(nc)
				if err != nil {
					t.Fatal(err)
				}
				restored := &cvk.NetworkController{}
				if err = json.Unmarshal(raw, restored); err != nil {
					t.Fatal(err)
				}
				if err = admin.Status().Update(ctx, restored); err != nil {
					t.Fatal(err)
				}
				recovered[op.ID] = true
			}
			if op.Phase == "Verifying" && featureFinalizeSelected(op.Resource) && mode == "pruned" {
				a.statusWriter = pruningActionReceiptClient{Client: statusOnlyFeatureClient{Client: admin}}
			}
			if op.Phase == "Verified" && featureFinalizeSelected(op.Resource) && mode == "tombstone-missing" && !corrupted {
				rows := state.ActionApplications[:0]
				for _, r := range state.ActionApplications {
					if r.Operation.ID != op.ID {
						rows = append(rows, r)
					}
				}
				state.ActionApplications = rows
				if err := admin.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				corrupted = true
			}
		}
		if featureDeletionCompleted(mustFeatureController(t, a), config) {
			break
		}
	}
	nc := mustFeatureController(t, a)
	complete := featureDeletionCompleted(nc, config)
	safe := mode == "known" || mode == "retain" || mode == "source-gone" || mode == "inflight-update" || mode == "inflight-create" || mode == "prepared" || mode == "prepared-source-gone"
	if safe {
		expectedDeletes := 2
		if mode == "retain" {
			expectedDeletes = 0
		}
		if mode == "inflight-create" {
			expectedDeletes = 3
		}
		if !complete || len(world.writes) != beforeDeleteWrites+expectedDeletes {
			t.Fatal("deletion cleanup did not complete safely", mode, complete, world.writes, config.Status)
		}
		if mode != "retain" && len(world.rows) != 0 {
			t.Fatal("owned native resources remain", mode, len(world.rows))
		}
		// Completion proof binds config/controller/spec/deletion and exact evidence.
		for _, change := range []string{"spec", "uid", "controller", "history", "condition"} {
			t.Run(change, func(t *testing.T) {
				c := config.DeepCopy()
				controller := nc.DeepCopy()
				switch change {
				case "spec":
					c.Spec.ModelSource.Exporter += "-changed"
				case "uid":
					c.UID = "replacement"
				case "controller":
					controller.Generation++
				case "history":
					if controller.Status.Configuration != nil && len(controller.Status.Configuration.ActionApplications) > 0 {
						controller.Status.Configuration.ActionApplications = controller.Status.Configuration.ActionApplications[1:]
					} else {
						controller.UID = "replacement"
					}
				case "condition":
					c.Status.Conditions = nil
				}
				if featureDeletionCompleted(controller, c) {
					t.Fatal("stale completion proof accepted", change)
				}
			})
		}
		// The manager alone removes our finalizer; unrelated finalizers survive.
		if len(config.Finalizers) != 2 {
			t.Fatal("worker modified finalizers", config.Finalizers)
		}
		config.Finalizers = []string{"example.com/other"}
		if err := admin.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	} else {
		expectedExtra := 0
		switch mode {
		case "missing-task", "lost-ack", "tombstone-missing", "pruned":
			expectedExtra = 1
		}
		if complete || len(world.writes) != beforeDeleteWrites+expectedExtra || !hasFeatureConfigFinalizer(config) {
			t.Fatal("unproven cleanup escaped finalizer", mode, complete, world.writes, config.Status)
		}
	}
}
func mustFeatureController(t *testing.T, a *adapter) *cvk.NetworkController {
	t.Helper()
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return nc
}
func TestFeatureDeletePolicyRequiresManagerEnrollment(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	config.Spec.Source.Inline.Raw = featurePruneModel(false)
	config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	if supportedConfigurationApply(config) || config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
		t.Fatal("Delete policy bypassed manager enrollment", config.Status)
	}
	policy := controlleradapter.ConfigurationLifecycleFor(TypeName)
	if policy == nil || policy.Finalizer != featureConfigFinalizer {
		t.Fatal("lifecycle not registered")
	}
	if _, err := a.currentController(ctx); apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestFeatureManagerWorkerDeletionHandshake(t *testing.T) {
	ctx := context.Background()
	a, config, world := featurePruneFixture(t, nil)
	manager, admin := featureLifecycleManagerFixture(t, a, config)
	key := ctrlclient.ObjectKeyFromObject(config)
	if err := admin.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	if featureDeletionCompleted(mustFeatureController(t, a), config) {
		t.Fatal("manager completion preceded native cleanup")
	}
	complete := false
	for range 70 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		if featureDeletionCompleted(mustFeatureController(t, a), config) {
			complete = true
			break
		}
	}
	if !complete || len(world.rows) != 0 || len(world.writes) != 6 {
		t.Fatal("manager/worker native cleanup incomplete", complete, world.writes)
	}
	current := mustFeatureController(t, a)
	current.Status.Configuration.Operation = &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("f", 32), ConfigUID: "other", ConfigName: "other", ConfigGeneration: 1, ControllerGeneration: current.Generation, Section: "inventory", Resource: "other", Action: "Update", Phase: "Unknown"}
	if err := admin.Status().Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	if !featureDeletionCompleted(mustFeatureController(t, a), config) || len(world.writes) != 6 {
		t.Fatal("unrelated journal prevented completed cleanup")
	}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, key, &configapi.NetworkControllerConfig{}); !apierrors.IsNotFound(err) {
		t.Fatal("manager did not remove completed finalizer", err)
	}
	if journalState(t, a).Operation == nil || journalState(t, a).Operation.ID != strings.Repeat("f", 32) {
		t.Fatal("config finalization erased another owner uncertainty")
	}
	if len(journalState(t, a).ActionApplications) != 6 {
		t.Fatal("metadata removal erased durable history")
	}
}
