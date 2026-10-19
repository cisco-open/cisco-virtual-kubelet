//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type liveDiscoveryPublicLifecycle struct {
	manager *controller.NetworkControllerReconciler
	admin   ctrlclient.Client
}

func prepareLiveDiscoveryPublicLifecycle(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, dir string) *liveDiscoveryPublicLifecycle {
	t.Helper()
	ctx := context.Background()
	admin := a.statusWriter
	if os.Getenv("CATC_DISCOVERY_PUBLIC_SOURCE_RECOVERY") == "existing-lab-single" {
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "qualified-discovery-source", Namespace: c.Namespace, UID: "qualified-discovery-source"}, Data: map[string]string{"model": string(c.Spec.Source.Inline.Raw)}}
		if err := admin.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
	}
	c.Generation++
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = append(c.Finalizers, "test.cisco.vk/public-qualification")
	if err := admin.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("native qualification manager enrollment", err)
	}
	if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if !hasDiscoveryConfigFinalizer(c) || hasFeatureConfigFinalizer(c) {
		t.Fatal("manager enrolled wrong native lifecycle")
	}
	a.statusReader = admin
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeLiveDiscoveryPublicRecord(t, dir, "public-enrollment.json", map[string]any{"configUID": c.UID, "generation": c.Generation, "controllerUID": nc.UID, "finalizers": c.Finalizers, "managerEnrolled": true, "installedClusterMutated": false})
	return &liveDiscoveryPublicLifecycle{manager: manager, admin: admin}
}

func writeLiveDiscoveryPublicRecord(t *testing.T, dir, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func (l *liveDiscoveryPublicLifecycle) finish(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, id, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	key := ctrlclient.ObjectKeyFromObject(c)
	if os.Getenv("CATC_DISCOVERY_PUBLIC_SOURCE_RECOVERY") == "existing-lab-single" {
		state := journalState(t, a)
		if state.Operation != nil || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.SourceUID == "" {
			t.Fatal("qualified original CM create unavailable")
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		original := state.ActionApplications[0].Operation.DeepCopy()
		original.Phase = "Unknown"
		nc.Status.Configuration.Operation = original
		nc.Status.Configuration.ActionApplications = nil
		if err := l.admin.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		var cm core.ConfigMap
		cmKey := ctrlclient.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Source.ConfigMapRef.Name}
		if err := l.admin.Get(ctx, cmKey, &cm); err != nil {
			t.Fatal(err)
		}
		if err := l.admin.Delete(ctx, &cm); err != nil {
			t.Fatal(err)
		}
		if err := l.admin.Get(ctx, cmKey, &cm); !apierrors.IsNotFound(err) {
			t.Fatal("CM was not removed", err)
		}
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := l.admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		writeLiveDiscoveryPublicRecord(t, dir, "missing-source-start.json", map[string]any{"originalTask": original, "sourceUID": cm.UID, "sourceVersion": cm.ResourceVersion, "sourceRemoved": true, "simulatedOutstandingOriginalCreate": true, "conditions": c.Status.Conditions})
	}
	if err := l.admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := l.admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	recovered := false
	complete := false
	for ctx.Err() == nil {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := l.admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		writeLiveDiscoveryPublicRecord(t, dir, "public-journal.json", state)
		if state.Operation != nil && state.Operation.Phase == "Accepted" && discoveryFinalizeSelected(state.Operation.Resource) && !recovered {
			if !discoveryFinalizeSelected(state.Operation.Resource) {
				t.Fatal("unexpected public operation")
			}
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			recovered = true
			writeLiveDiscoveryPublicRecord(t, dir, "public-injected-unknown.json", journalState(t, a))
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if discoveryDeletionCompleted(nc, c) {
			complete = true
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	if !complete || !recovered || !hasDiscoveryConfigFinalizer(c) {
		t.Fatal("public cleanup timed out or worker removed finalizer; retained for inspection")
	}
	writeLiveDiscoveryPublicRecord(t, dir, "public-completion-status.json", c.Status)
	if _, err := l.manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("native qualification manager release", err)
	}
	if err := l.admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if hasDiscoveryConfigFinalizer(c) || !slices.Contains(c.Finalizers, "test.cisco.vk/public-qualification") || len(c.Finalizers) != 1 || state.Operation != nil || len(state.ActionApplications) != 1 || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || state.ActionApplications[0].Operation.TargetID != id {
		t.Fatal("public manager release lost finalizer/receipt boundary")
	}
	writeLiveDiscoveryPublicRecord(t, dir, "cleanup.json", map[string]any{"id": id, "deleteTaskId": state.ActionApplications[0].Operation.TaskID, "deleted": true, "publicLifecycle": true, "managerEnrolled": true, "managerReleased": true, "injectedKnownTaskRecovery": true, "installedClusterMutated": false})
	writeLiveDiscoveryPublicRecord(t, dir, "public-release.json", map[string]any{"configUID": c.UID, "finalizers": c.Finalizers, "managerReleased": true, "ownerRetained": len(state.Owners) == 1, "receiptRetained": true})
	t.Log("public manager enrollment, Delete-policy create, exact cleanup, injected Unknown recovery and manager-only finalizer release verified", id)
}
