// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

// Public manager orchestration with its real field-index contracts, separate
// from the status-only worker. Never constructs or starts a product worker.
func featureLifecycleManagerFixture(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig) (*controller.NetworkControllerReconciler, ctrlclient.Client) {
	t.Helper()
	ctx := context.Background()
	nc := mustFeatureController(t, a)
	admin := fake.NewClientBuilder().WithScheme(a.statusWriter.Scheme()).WithStatusSubresource(nc, c).
		WithIndex(&cvk.NetworkController{}, "spec.endpoint", func(obj ctrlclient.Object) []string { return []string{obj.(*cvk.NetworkController).Spec.Endpoint} }).
		WithIndex(&configapi.NetworkControllerConfig{}, "spec.controllerRef.name", func(obj ctrlclient.Object) []string {
			return []string{obj.(*configapi.NetworkControllerConfig).Spec.ControllerRef.Name}
		}).
		WithObjects(nc, c).Build()
	a.statusReader = admin
	a.statusWriter = statusOnlyFeatureClient{Client: admin}
	manager := &controller.NetworkControllerReconciler{Client: admin, APIReader: admin}
	c.Generation++
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	if err := admin.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
		t.Fatal("manager enrollment failed", err)
	}
	if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if !hasFeatureConfigFinalizer(c) {
		t.Fatal("manager did not enroll cleanup finalizer")
	}
	return manager, admin
}
