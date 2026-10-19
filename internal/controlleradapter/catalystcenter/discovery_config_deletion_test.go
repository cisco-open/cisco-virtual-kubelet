// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
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
	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Emulate only manager cache indexes for envtest's direct client. Reads, status
// CAS and optimistic metadata patches still reach the real Kubernetes API.
type discoveryLifecycleIndexedClient struct{ ctrlclient.Client }

func (c discoveryLifecycleIndexedClient) List(ctx context.Context, list ctrlclient.ObjectList, options ...ctrlclient.ListOption) error {
	opts := &ctrlclient.ListOptions{}
	opts.ApplyOptions(options)
	selector := opts.FieldSelector
	if selector == nil {
		return c.Client.List(ctx, list, options...)
	}
	opts.FieldSelector = nil
	if err := c.Client.List(ctx, list, opts); err != nil {
		return err
	}
	switch rows := list.(type) {
	case *cvk.NetworkControllerList:
		rows.Items = slices.DeleteFunc(rows.Items, func(row cvk.NetworkController) bool {
			return !selector.Matches(fields.Set{"spec.endpoint": row.Spec.Endpoint})
		})
	case *configapi.NetworkControllerConfigList:
		rows.Items = slices.DeleteFunc(rows.Items, func(row configapi.NetworkControllerConfig) bool {
			return !selector.Matches(fields.Set{"spec.controllerRef.name": row.Spec.ControllerRef.Name})
		})
	default:
		return errConfigurationTransition
	}
	return nil
}

var discoveryPublicDeletionModes = []string{"owned", "create", "inflight-create", "configmap-create", "prepared", "empty", "unowned", "lost-ack", "native-drift", "source-missing", "unsupported-source", "foreign-operation", "family-conflict", "task-timeout", "configmap-gone", "configmap-gone-inflight", "configmap-replaced", "configmap-edited", "scope-pruned", "scope-forged", "scope-spec-edited", "gone-native-drift", "gone-lost-ack"}

func TestDiscoveryPublicConfigDeletion(t *testing.T) {
	for _, mode := range discoveryPublicDeletionModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryPublicDeletion(t, mode, nil) })
	}
}

func exerciseDiscoveryPublicDeletion(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, world := discoveryPruneFixture(t)
	if install != nil {
		install(a, c)
	}
	admin := a.statusWriter
	if install == nil {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		admin = fake.NewClientBuilder().WithScheme(admin.Scheme()).WithStatusSubresource(nc, c).WithObjects(nc, c).Build()
	}
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: admin}
	a.statusReader = admin
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	if mode == "task-timeout" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	}
	c.Finalizers = []string{"example.com/keeper"}
	retainedCM := strings.HasPrefix(mode, "configmap-") && mode != "configmap-create" || strings.HasPrefix(mode, "scope-") || strings.HasPrefix(mode, "gone-")
	create := mode == "create" || mode == "inflight-create" || mode == "configmap-create" || retainedCM
	if create {
		c.Spec.Source.Inline.Raw = discoveryModel()
	}
	if mode == "configmap-create" || mode == "source-missing" || retainedCM {
		cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "public-source", Namespace: c.Namespace}, Data: map[string]string{"model": string(discoveryModel())}}
		if install == nil {
			cm.UID = "public-source"
		}
		if mode != "source-missing" {
			if err := admin.Create(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
	}
	if mode == "unsupported-source" {
		c.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"tags":[{"name":"foreign"}]}}}`)
	}
	if err := admin.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	key := ctrlclient.ObjectKeyFromObject(c)
	worker := &configReconciler{a: a}
	if supportedConfigurationApply(c) {
		t.Fatal("Delete policy bypassed manager enrollment")
	}
	if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if len(world.writes) != 0 {
		t.Fatal("unenrolled worker mutated native state")
	}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("manager enrollment", err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if !hasDiscoveryConfigFinalizer(c) || hasFeatureConfigFinalizer(c) || !slices.Contains(c.Finalizers, "example.com/keeper") {
		t.Fatal("wrong cleanup family enrolled", c.Finalizers)
	}
	// Model the worker incarnation after manager metadata/defaulting updates.
	var nc cvk.NetworkController
	if err := admin.Get(ctx, a.key, &nc); err != nil {
		t.Fatal(err)
	}
	if nc.UID != a.uid {
		t.Fatal("controller replaced")
	}
	a.generation = nc.Generation
	if create || mode == "empty" || mode == "unowned" {
		nc.Status.Configuration = nil
	}
	if create {
		delete(world.jobs, "fixture")
	}
	if mode == "prepared" {
		op := nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
		op.Phase = "Prepared"
		op.ConfigGeneration = c.Generation
		op.ControllerGeneration = nc.Generation
		op.TaskID = ""
		op.SubmittedAt = nil
		nc.Status.Configuration.Operation = op
	}
	if mode == "foreign-operation" {
		op := nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
		op.ConfigUID = "foreign"
		op.ConfigName = "foreign"
		op.Phase = "Unknown"
		nc.Status.Configuration.Operation = op
	}
	if err := admin.Status().Update(ctx, &nc); err != nil {
		t.Fatal(err)
	}
	if mode == "empty" {
		delete(world.jobs, "fixture")
	}
	if create && c.Spec.Source.ConfigMapRef != nil {
		if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		if meta.FindStatusCondition(c.Status.Conditions, discoveryScopeCondition) == nil || len(world.writes) != 0 {
			t.Fatal("CM source scope was not persisted before native mutation")
		}
	}
	if create {
		settled := false
		for range 15 {
			if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if (mode == "inflight-create" || mode == "configmap-gone-inflight") && state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" {
				settled = true
				break
			}
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && state != nil && state.Operation == nil && len(state.ActionApplications) == 1 {
				settled = true
				break
			}
		}
		if !settled || len(world.writes) != 1 {
			t.Fatal("enrolled public creation did not reach boundary", c.Status, world.writes)
		}
	}
	baseWrites := len(world.writes)
	if retainedCM {
		var cm core.ConfigMap
		cmKey := ctrlclient.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Source.ConfigMapRef.Name}
		if err := admin.Get(ctx, cmKey, &cm); err != nil {
			t.Fatal(err)
		}
		if mode == "configmap-edited" {
			cm.Data["model"] = `{"catalyst_center":{"inventory":{"tags":[{"name":"unapplied"}]}}}`
			if err := admin.Update(ctx, &cm); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := admin.Delete(ctx, &cm); err != nil {
				t.Fatal(err)
			}
			if mode == "configmap-replaced" {
				replacement := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmKey.Name, Namespace: cmKey.Namespace}, Data: map[string]string{"model": `{"catalyst_center":{"inventory":{"tags":[{"name":"unapplied"}]}}}`}}
				if install == nil {
					replacement.UID = "replacement"
				}
				if err := admin.Create(ctx, replacement); err != nil {
					t.Fatal(err)
				}
			}
		}
		if mode == "scope-pruned" || mode == "scope-forged" {
			if mode == "scope-pruned" {
				meta.RemoveStatusCondition(&c.Status.Conditions, discoveryScopeCondition)
			} else {
				meta.FindStatusCondition(c.Status.Conditions, discoveryScopeCondition).Message += " "
			}
			if err := admin.Status().Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "scope-spec-edited" {
			c.Spec.ModelSource.Exporter = "changed"
			if err := admin.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "gone-native-drift" {
			row := world.jobs["fixture"]
			row.Protocol = "TELNET"
			world.jobs["fixture"] = row
		}
		if mode == "gone-lost-ack" {
			world.mode = "lost-ack"
		}
		if mode == "configmap-gone" || mode == "configmap-gone-inflight" {
			// A retained scope is observation/cleanup authority only after deletion.
			if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := admin.Get(ctx, key, c); err != nil {
				t.Fatal(err)
			}
			if len(world.writes) != baseWrites {
				t.Fatal("missing active CM authorized another native mutation")
			}
		}
	}
	if mode == "lost-ack" {
		world.mode = "lost-ack"
	}
	if mode == "native-drift" {
		row := world.jobs["fixture"]
		row.Protocol = "TELNET"
		world.jobs["fixture"] = row
	}
	if mode == "family-conflict" {
		c.Finalizers = append(c.Finalizers, featureConfigFinalizer)
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	complete := false
	timeoutObserved := false
	for range 30 {
		if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := admin.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		if mode == "task-timeout" {
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Unknown" {
				timeoutObserved = true
			}
		}
		if hasDiscoveryConfigFinalizer(c) == false {
			t.Fatal("worker removed finalizer")
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if discoveryDeletionCompleted(current, c) {
			complete = true
			break
		}
	}
	blocked := strings.HasPrefix(mode, "scope-") || strings.HasPrefix(mode, "gone-") || mode == "lost-ack" || mode == "native-drift" || mode == "source-missing" || mode == "unsupported-source" || mode == "foreign-operation" || mode == "family-conflict"
	if mode == "task-timeout" && !timeoutObserved {
		t.Fatal("timeout boundary not exercised")
	}
	if complete == blocked {
		t.Fatal("incorrect public completion", mode, complete, c.Status)
	}
	expected := baseWrites
	if !blocked && mode != "empty" && mode != "unowned" || mode == "lost-ack" || mode == "gone-lost-ack" {
		expected++
	}
	if len(world.writes) != expected {
		t.Fatal("public lifecycle replayed or touched unsafe state", mode, world.writes)
	}
	if blocked {
		return
	}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("manager release", err)
	}
	if err := admin.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	if hasDiscoveryConfigFinalizer(c) || !slices.Contains(c.Finalizers, "example.com/keeper") || len(c.Finalizers) != 1 {
		t.Fatal("manager did not remove only its completed finalizer", c.Finalizers)
	}
	state := journalState(t, a)
	if mode != "empty" && mode != "unowned" && (state == nil || state.Operation != nil || len(state.Owners) != 1 || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.Action != "Delete" || !validOwnedDiscoveryReceipt(state.ActionApplications[0])) {
		t.Fatal("manager lost owner/tombstone evidence")
	}
	if world.jobs["foreign"].ID != "foreign-id" {
		t.Fatal("foreign discovery changed")
	}
	if mode == "unowned" && world.jobs["fixture"].ID != "42" {
		t.Fatal("unowned discovery adopted/deleted")
	}
	if create && !strings.HasPrefix(world.writes[0], "POST ") {
		t.Fatal("creation path not exercised")
	}
}
