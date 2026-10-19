// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package configurationreservation

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
)

func TestExpiredUnclaimedGrantRevokedWithoutRefreshingPreparation(t *testing.T) {
	ctx := context.Background()
	r, reservation, _ := recoveryFixture(t)
	reservation.Status.Claim = nil
	if err := r.Client.Status().Update(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	r.StrictDevicePodLister = func(context.Context) ([]*core.Pod, error) { t.Fatal("old grant refreshed inventory"); return nil, nil }
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.Grant.Phase != "Revoked" || reservation.Status.Claim != nil {
		t.Fatal("expired grant not revoked")
	}
}

func TestClaimedRecoveryDoesNotRequireNewWorkGates(t *testing.T) {
	ctx := context.Background()
	r, reservation, lease := recoveryFixture(t)
	r.Authorize, r.Prepare, r.StrictDevicePodLister = nil, nil, nil
	r.ControllerType = ""
	before := reservation.Status.Grant.GrantedAt
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}); err != nil {
		t.Fatal(err)
	}
	var current ops.ControllerConfigurationReservation
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Claim == nil || current.Status.Grant.Phase != "Held" || !current.Status.Grant.GrantedAt.Equal(&before) {
		t.Fatal("recovery refreshed or released old grant")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != current.Status.Grant.Holder {
		t.Fatal("lost quarantine holder")
	}
}
