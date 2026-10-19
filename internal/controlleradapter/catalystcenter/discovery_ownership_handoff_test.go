// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var discoveryHandoffModes = []string{"closed", "public", "recreate", "same-name", "prior-present", "prior-deleting", "missing-proof", "malformed-proof", "proof-edit", "proof-generation", "receipt-edit", "create-receipt", "receipt-missing", "foreign-owner", "pending", "reappeared", "native-race", "source-race", "evidence-race", "controller-race", "cas-conflict", "read-error", "old-read-error", "mixed", "unenrolled", "retain", "empty-owned", "history-edit", "history-prune", "history-proof-missing", "history-reappeared"}

func TestDiscoveryOwnershipHandoff(t *testing.T) {
	for _, mode := range discoveryHandoffModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryHandoff(t, mode, nil) })
	}
}

func exerciseDiscoveryHandoff(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, old, world := discoveryPruneFixture(t)
	native := a.client
	if install != nil {
		install(a, old)
	}
	old.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	old.Finalizers = []string{discoveryConfigFinalizer}
	if mode == "recreate" {
		old.Finalizers = nil
	}
	if mode == "prior-deleting" {
		old.Finalizers = append(old.Finalizers, "test.cisco.vk/other-keeper")
	}
	if err := a.statusWriter.Update(ctx, old); err != nil {
		t.Fatal(err)
	}
	if mode == "recreate" {
		manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{a.statusWriter}, APIReader: a.statusReader}
		if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
			t.Fatal("initial manager enrollment", err)
		}
		var current cvk.NetworkController
		if err := a.statusReader.Get(ctx, a.key, &current); err != nil {
			t.Fatal(err)
		}
		a.generation = current.Generation
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(old), old); err != nil || !hasDiscoveryConfigFinalizer(old) {
			t.Fatal("initial discovery enrollment", err)
		}
	}
	if err := a.statusWriter.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(old), old); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := &nc.Status.Configuration.ActionApplications[0]
	r.Operation.Action = "Delete"
	r.Operation.TargetID = "42"
	r.Operation.Resource += discoveryPruneSuffix
	if mode == "empty-owned" {
		nc.Status.Configuration.ActionApplications = nil
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	delete(world.jobs, "fixture")
	if err := a.publishDiscoveryConfigCompletion(ctx, old); err != nil {
		t.Fatal("archive qualified completion", err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(old), old); err != nil {
		t.Fatal(err)
	}
	oldCopy := old.DeepCopy()
	if mode == "prior-deleting" {
		old.Finalizers = []string{"test.cisco.vk/other-keeper"}
		if err := a.statusWriter.Update(ctx, old); err != nil {
			t.Fatal(err)
		}
	}
	if mode != "prior-present" && mode != "prior-deleting" {
		old.Finalizers = nil
		if err := a.statusWriter.Update(ctx, old); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(old), old); !apierrors.IsNotFound(err) {
			t.Fatal("old UID still exists", err)
		}
	}
	name := "next-discovery"
	if mode == "same-name" {
		name = oldCopy.Name
	}
	next := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Namespace: oldCopy.Namespace, Name: name, Finalizers: []string{discoveryConfigFinalizer}}, Spec: oldCopy.Spec}
	if install == nil {
		next.UID = "next-config"
		next.Generation = 1
	}
	if mode == "mixed" {
		next.Spec.Source.Inline = oldCopy.Spec.Source.Inline.DeepCopy()
		next.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"tags":[{"name":"foreign"}]}}}`)
	}
	if mode == "unenrolled" {
		next.Finalizers = nil
	}
	if mode == "retain" {
		next.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if mode == "recreate" {
		next.Spec.Source.Inline = oldCopy.Spec.Source.Inline.DeepCopy()
		next.Spec.Source.Inline.Raw = discoveryModel()
	}
	if strings.HasPrefix(mode, "history-") {
		next.Spec.Source.Inline = oldCopy.Spec.Source.Inline.DeepCopy()
		next.Spec.Source.Inline.Raw = []byte(strings.ReplaceAll(string(discoveryModel()), "fixture", "replacement"))
		world.allowedCreates["replacement"] = true
	}
	if err := a.statusWriter.Create(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(next), next); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil {
		t.Fatal("completion did not retain retirement proof")
	}
	switch mode {
	case "missing-proof":
		meta.RemoveStatusCondition(&nc.Status.Conditions, discoveryRetirementCondition)
	case "malformed-proof":
		cond.Message = "{}"
	case "proof-edit":
		var p discoveryOwnershipRetirement
		_ = json.Unmarshal([]byte(cond.Message), &p)
		p.RetainedDigest = strings.Repeat("0", 64)
		raw, _ := json.Marshal(p)
		cond.Message = string(raw)
	case "proof-generation":
		cond.ObservedGeneration++
	case "receipt-edit":
		nc.Status.Configuration.ActionApplications[0].Operation.TaskID = "changed-task"
	case "create-receipt":
		nc.Status.Configuration.ActionApplications[0].Operation.Action = "Create"
	case "receipt-missing":
		nc.Status.Configuration.ActionApplications = nil
	case "foreign-owner":
		nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
	case "pending":
		nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	reads, writes := 0, 0
	admin := a.statusWriter
	a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == authPath {
			w.Write([]byte(`{"Token":"test"}`))
			return
		}
		if req.Method != http.MethodGet || req.URL.Path != discoveryPath+"/1/100" {
			writes++
			w.WriteHeader(400)
			return
		}
		reads++
		if mode == "read-error" {
			w.WriteHeader(503)
			return
		}
		if reads == 1 {
			switch mode {
			case "source-race":
				changed := next.DeepCopy()
				changed.Spec.ModelSource.Exporter = "changed"
				if err := admin.Update(ctx, changed); err != nil {
					t.Error(err)
				}
			case "evidence-race", "controller-race":
				fresh, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if mode == "evidence-race" {
					fresh.Status.Configuration.ActionApplications[0].Operation.TaskID = "raced"
				} else {
					meta.SetStatusCondition(&fresh.Status.Conditions, condition("Unrelated", true, "Changed", "changed", fresh.Generation))
				}
				if err := admin.Status().Update(ctx, fresh); err != nil {
					t.Error(err)
				}
			}
		}
		rows := []discoveryMetadata{world.jobs["foreign"]}
		if mode == "reappeared" || mode == "native-race" && reads == 2 {
			row := world.jobs["foreign"]
			row.ID = "42"
			row.Name = "renamed"
			rows = append(rows, row)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	if mode == "old-read-error" {
		a.statusReader = discoveryOldReadFailure{Reader: a.statusReader, name: oldCopy.Name}
	}
	if mode == "cas-conflict" {
		a.statusWriter = discoveryHandoffConflictClient{Client: admin}
	}
	source, err := loadControllerSource(ctx, a.statusReader, next)
	if err != nil {
		t.Fatal(err)
	}
	handled := false
	if mode == "public" {
		_, err = (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(next)})
		if err == nil {
			err = a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(next), next)
		}
		cond := meta.FindStatusCondition(next.Status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
		handled = cond != nil && cond.Reason == "DiscoveryOwnershipHandedOff"
	} else {
		handled, err = a.handoffDiscoveryOwnership(ctx, nc, next, source)
	}
	current, readErr := a.currentController(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	success := mode == "closed" || mode == "public" || mode == "recreate" || mode == "same-name" || mode == "empty-owned" || strings.HasPrefix(mode, "history-")
	if writes != 0 {
		t.Fatal("handoff submitted a native mutation")
	}
	if success {
		if !handled || err != nil || reads != 2 || configurationOwnersMatch(current.Status.Configuration, next) != nil {
			t.Fatal("handoff did not converge", handled, err, reads)
		}
		if !reflect.DeepEqual(current.Status.Configuration.ActionApplications, before.ActionApplications) {
			t.Fatal("handoff erased or relabeled audit receipts")
		}
		if current.Status.Configuration.Operation != nil {
			t.Fatal("handoff prepared a native write")
		}
		if handled, err := a.handoffDiscoveryOwnership(ctx, current, next, source); handled || err != nil {
			t.Fatal("handoff replay changed ownership", err)
		}
		if mode == "recreate" {
			a.client = native
			exerciseDiscoveryHandoffRepeatedCycle(t, a, next, world, install == nil)
		}
		if strings.HasPrefix(mode, "history-") {
			a.client = native
			switch mode {
			case "history-edit":
				current.Status.Configuration.ActionApplications[0].Operation.TaskID = "changed-history"
			case "history-prune":
				current.Status.Configuration.ActionApplications = nil
			case "history-proof-missing":
				meta.RemoveStatusCondition(&current.Status.Conditions, discoveryRetirementCondition)
			case "history-reappeared":
				row := world.jobs["foreign"]
				row.ID = "42"
				row.Name = "resurrected"
				world.jobs[row.Name] = row
			}
			if err := admin.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(next)}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(next), next); err != nil {
				t.Fatal(err)
			}
			if len(world.writes) != 0 || journalState(t, a).Operation != nil || next.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("corrupt history authorized new intent", next.Status, world.writes)
			}
		}
	} else {
		if err == nil && handled {
			t.Fatal("unsafe handoff accepted", mode)
		}
		if !reflect.DeepEqual(current.Status.Configuration.Owners, before.Owners) {
			t.Fatal("refused handoff changed ownership")
		}
	}
}

func exerciseDiscoveryHandoffRepeatedCycle(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, world *discoveryPruneWorld, fakeUID bool) {
	t.Helper()
	ctx := context.Background()
	admin := a.statusWriter
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
	worker := &configReconciler{a: a}
	var prior []cvk.NetworkControllerConfigurationActionApplication
	for incarnation := 2; incarnation <= 4; incarnation++ {
		key := ctrlclient.ObjectKeyFromObject(c)
		settled := false
		for range 24 {
			if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && state.Operation == nil {
				settled = true
				break
			}
		}
		state := journalState(t, a)
		if !settled || len(state.ActionApplications) != incarnation || len(world.writes) != (incarnation-2)*2+1 {
			t.Fatal("replacement did not create exactly one new incarnation", c.Status, state, world.writes)
		}
		for _, old := range prior {
			found := false
			for _, r := range state.ActionApplications {
				found = found || reflect.DeepEqual(old, r)
			}
			if !found {
				t.Fatal("replacement modified a predecessor tombstone")
			}
		}
		for range 2 {
			if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
		}
		if len(world.writes) != (incarnation-2)*2+1 {
			t.Fatal("converged replacement replayed native work")
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		if err := admin.Delete(ctx, c); err != nil {
			t.Fatal(err)
		}
		gone := false
		for range 24 {
			if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			if meta.IsStatusConditionTrue(c.Status.Conditions, discoveryConfigCleanupCondition) {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !discoveryDeletionCompleted(current, c) {
					t.Fatalf("completion proof not current at incarnation %d: config=%+v proof=%+v", incarnation, c.ObjectMeta, meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition))
				}
				if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
					t.Fatalf("manager release incarnation %d: %v", incarnation, err)
				}
			}
			err := admin.Get(ctx, key, c)
			if apierrors.IsNotFound(err) {
				gone = true
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		state = journalState(t, a)
		if !gone || state.Operation != nil || len(world.writes) != (incarnation-1)*2 || len(state.ActionApplications) != incarnation || len(world.jobs) != 1 {
			t.Fatal("replacement deletion/finalization incomplete", state, world.writes)
		}
		for _, r := range state.ActionApplications {
			if !validOwnedDiscoveryReceipt(r) || r.Operation.Action != "Delete" {
				t.Fatal("incomplete audit history")
			}
		}
		prior = state.DeepCopy().ActionApplications
		if incarnation == 4 {
			break
		}
		replacement := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name}, Spec: c.Spec}
		if fakeUID {
			replacement.UID = types.UID(fmt.Sprint("incarnation-", incarnation+1))
			replacement.Generation = 1
		}
		if err := admin.Create(ctx, replacement); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
			t.Fatalf("manager enrollment incarnation %d: %v", incarnation+1, err)
		}
		if err := admin.Get(ctx, key, replacement); err != nil {
			t.Fatal(err)
		}
		if !hasDiscoveryConfigFinalizer(replacement) {
			t.Fatal("replacement not manager-enrolled")
		}
		c = replacement
	}
}

type discoveryHandoffConflictClient struct{ ctrlclient.Client }

func (c discoveryHandoffConflictClient) Status() ctrlclient.SubResourceWriter {
	return discoveryHandoffConflictStatus{SubResourceWriter: c.Client.Status(), client: c.Client}
}

type discoveryHandoffConflictStatus struct {
	ctrlclient.SubResourceWriter
	client ctrlclient.Client
}

func (s discoveryHandoffConflictStatus) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok {
		var raced cvk.NetworkController
		if err := s.client.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &raced); err != nil {
			return err
		}
		meta.SetStatusCondition(&raced.Status.Conditions, condition("HandoffRace", true, "Changed", "changed", raced.Generation))
		if err := s.client.Status().Update(ctx, &raced); err != nil {
			return err
		}
	}
	return s.SubResourceWriter.Update(ctx, obj, opts...)
}

type discoveryOldReadFailure struct {
	ctrlclient.Reader
	name string
}

func (c discoveryOldReadFailure) Get(ctx context.Context, key ctrlclient.ObjectKey, obj ctrlclient.Object, opts ...ctrlclient.GetOption) error {
	if _, ok := obj.(*configapi.NetworkControllerConfig); ok && key.Name == c.name {
		return apierrors.NewForbidden(configapi.GroupVersion.WithResource("networkcontrollerconfigs").GroupResource(), key.Name, nil)
	}
	return c.Reader.Get(ctx, key, obj, opts...)
}
