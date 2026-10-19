// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryScopePublicationJournalRace(t *testing.T) {
	exerciseDiscoveryScopePublicationRace(t, nil)
}

func exerciseDiscoveryScopePublicationRace(t *testing.T, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, world := discoveryPruneFixture(t)
	if install != nil {
		install(a, c)
	}
	admin := a.statusWriter
	cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "scope-race", Namespace: c.Namespace}, Data: map[string]string{"model": string(discoveryModel())}}
	if install == nil {
		cm.UID = "scope-race"
	}
	if err := admin.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	c.Spec.Source.Inline = nil
	c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{discoveryConfigFinalizer}
	if err := admin.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := *c.Status.DeepCopy()
	source, handled, err := (&configReconciler{a: a}).recordDiscoverySourceScope(ctx, nc, c, &status)
	if err != nil || !handled || source.discoveryScopeControllerVersion == "" {
		t.Fatal("scope not planned", err)
	}
	// A concurrent operation after scope planning cannot receive overwritten
	// scope metadata. The standard publication snapshot must reject it.
	nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
	nc.Status.Configuration.Operation.Phase = "Unknown"
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.checkConfigurationSnapshot(ctx, c, source); err == nil {
		t.Fatal("scope publication ignored journal race")
	}
	if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if meta.FindStatusCondition(c.Status.Conditions, discoveryScopeCondition) != nil || len(world.writes) != 0 {
		t.Fatal("rejected scope was published or mutated native state")
	}
}
