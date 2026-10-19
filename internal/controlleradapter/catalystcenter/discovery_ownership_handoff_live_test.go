//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Real native tasks/writes; Kubernetes persistence and actor restarts are local
// test fixtures. The replacement cycle introduces no journal/status fault.
func (l *liveDiscoveryPublicLifecycle) qualifyHandoff(t *testing.T, a *adapter, old *configapi.NetworkControllerConfig, trace *discoveryOwnershipTrace, baseline map[string]discoveryMetadata, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	before := journalState(t, a).DeepCopy()
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if before.Operation != nil || len(before.ActionApplications) != 1 || trace.posts != 1 || trace.deletes != 1 {
		t.Fatal("completed predecessor required")
	}
	oldUID := old.UID
	key := ctrlclient.ObjectKeyFromObject(old)
	old.Finalizers = nil
	if err := l.admin.Update(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := l.admin.Get(ctx, key, old); !apierrors.IsNotFound(err) {
		t.Fatal("predecessor still exists", err)
	}
	next := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "native-handoff-successor", Generation: 1, Finalizers: []string{"test.cisco.vk/handoff-keeper"}}, Spec: old.Spec}
	next.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	if err := l.admin.Create(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := l.manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal(err)
	}
	if err := l.admin.Get(ctx, key, next); err != nil || !hasDiscoveryConfigFinalizer(next) || next.UID == oldUID {
		t.Fatal("successor not independently enrolled", err)
	}
	trace.maxPosts, trace.maxDeletes = 2, 2
	requests := trace.attempts
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if trace.attempts != requests {
		t.Fatal("handoff itself submitted a mutation")
	}
	if err := l.admin.Get(ctx, key, next); err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(next.Status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
	if cond == nil || cond.Reason != "DiscoveryOwnershipHandedOff" {
		t.Fatal("public handoff did not execute", next.Status)
	}
	writeLiveDiscoveryPublicRecord(t, dir, "handoff-claim.json", map[string]any{"config": next, "journal": journalState(t, a), "nativeWritesDuringClaim": 0})
	read := func() {
		t.Helper()
		if err := l.admin.Get(ctx, key, next); err != nil {
			t.Fatal(err)
		}
		writeLiveDiscoveryPublicRecord(t, dir, "handoff-config.json", next)
		writeLiveDiscoveryPublicRecord(t, dir, "handoff-journal.json", journalState(t, a))
	}
	poll := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("handoff pending; preserve exact native task/UUID and private checkpoint", dir)
			case <-time.After(time.Second):
			}
		}
	}
	observedUnknown := false
	originalTask := ""
	poll(func() bool {
		rebuilt := *a
		if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		read()
		state := journalState(t, a)
		if op := state.Operation; op != nil && op.ConfigUID == string(next.UID) && op.Action == "Create" && op.TaskID != "" {
			if originalTask != "" && originalTask != op.TaskID {
				t.Fatal("successor task changed")
			}
			originalTask = op.TaskID
			observedUnknown = observedUnknown || op.Phase == "Unknown"
		}
		return next.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && state.Operation == nil
	})
	state := journalState(t, a)
	if !observedUnknown || len(state.ActionApplications) != 2 || trace.posts != 2 {
		t.Fatal("new incarnation or natural task recovery incomplete")
	}
	var created *cvk.NetworkControllerConfigurationActionApplication
	for i := range state.ActionApplications {
		r := &state.ActionApplications[i]
		if r.Operation.ConfigUID == string(next.UID) {
			created = r
		}
	}
	if created == nil || !validOwnedDiscoveryReceipt(*created) || created.Operation.Action != "Create" || created.Operation.TaskID != originalTask {
		t.Fatal("successor creation receipt unavailable")
	}
	rows, err := a.client.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, ok := rows[trace.name]
	if !ok || job.ID == trace.id {
		t.Fatal("native successor UUID not distinct")
	}
	trace.id = job.ID
	writeLiveDiscoveryPublicRecord(t, dir, "handoff-created.json", map[string]any{"receipt": created, "id": trace.id, "naturalUnknown": true, "task": originalTask})
	if err := l.admin.Delete(ctx, next); err != nil {
		t.Fatal(err)
	}
	poll(func() bool {
		rebuilt := *a
		if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := l.admin.Get(ctx, key, next); err != nil {
			t.Fatal(err)
		}
		if meta.IsStatusConditionTrue(next.Status.Conditions, discoveryConfigCleanupCondition) {
			if _, err := l.manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
				t.Fatal(err)
			}
		}
		read()
		return !hasDiscoveryConfigFinalizer(next)
	})
	state = journalState(t, a)
	if state.Operation != nil || len(state.ActionApplications) != 2 || trace.posts != 2 || trace.deletes != 2 || trace.attempts != 4 {
		t.Fatal("successor cleanup sent unexpected writes")
	}
	oldPreserved := false
	for _, r := range state.ActionApplications {
		oldPreserved = oldPreserved || reflect.DeepEqual(r, before.ActionApplications[0])
		if !validOwnedDiscoveryReceipt(r) || r.Operation.Action != "Delete" {
			t.Fatal("incomplete deletion history")
		}
	}
	if !oldPreserved {
		t.Fatal("predecessor tombstone changed")
	}
	for range 2 {
		rows, err := a.client.discoveries(ctx)
		if err != nil || !reflect.DeepEqual(rows, baseline) {
			t.Fatal("native baseline changed", err)
		}
	}
	after, err := a.client.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, after)
	if err != nil || len(after) != len(inventory) || afterTarget.ID != target.ID || afterTarget.Serial != target.Serial {
		t.Fatal("handoff changed lab inventory identity", err)
	}
	writeLiveDiscoveryPublicRecord(t, dir, "handoff-complete.json", map[string]any{"sameConfigName": true, "sameJobName": true, "newConfigUID": next.UID, "oldConfigUID": oldUID, "newNativeUUID": trace.id, "posts": trace.posts, "deletes": trace.deletes, "nativeAttempts": trace.attempts, "naturalCreateUnknown": observedUnknown, "oldTombstonePreserved": true, "nativeBaselineRestored": true, "managerReleased": true, "installedClusterMutated": false})
	next.Finalizers = nil
	if err := l.admin.Update(ctx, next); err != nil {
		t.Fatal(err)
	}
	t.Log("native same-name public ownership handoff and successor cleanup verified", trace.id)
}
