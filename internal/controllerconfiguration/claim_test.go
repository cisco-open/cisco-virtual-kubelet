// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func claimFixture(t *testing.T) (client.Client, *ops.ControllerConfigurationReservation, *cvk.NetworkControllerConfigurationOperation, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config-uid", ConfigName: "intent", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "resource", TargetID: "device-id", TargetSerial: "serial", Action: "Update", Phase: "Prepared"}
	digest, _ := OperationDigest(op)
	name, _ := ReservationName("controller-uid", op.ID)
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: Protocol, Controller: ops.ConfigurationReservationReference{UID: "controller-uid"}, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device-uid"}, ConfigGeneration: 1, ControllerGeneration: 1, OperationID: op.ID, OperationDigest: digest, InventoryID: op.TargetID}}
	holder, _ := Holder(r.Namespace, r)
	lname := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	duration, transitions := int32(60), int32(1)
	acquired, renewed := metav1.NewMicroTime(now.Add(-30*time.Second)), metav1.NewMicroTime(now.Add(-10*time.Second))
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: lname, UID: "lease-uid"}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, LeaseTransitions: &transitions, AcquireTime: &acquired, RenewTime: &renewed}}
	r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: lname, UID: string(lease.UID)}, Holder: holder, LeaseTransitions: 1, GrantedAt: metav1.NewTime(now.Add(-20 * time.Second))}
	scheme := runtime.NewScheme()
	if err := ops.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := coordination.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(r).WithObjects(r, lease).Build()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	return c, r, op, now
}

func TestClaimOnceAndRevocation(t *testing.T) {
	ctx := context.Background()
	t.Run("claim wins", func(t *testing.T) {
		c, r, op, now := claimFixture(t)
		if err := ClaimOnce(ctx, c, c, r, op, "leases", now); err != nil {
			t.Fatal(err)
		}
		if err := RevokeUnclaimed(ctx, c, r); err == nil {
			t.Fatal("stale revocation erased claim")
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if r.Status.Claim == nil {
			t.Fatal("missing claim")
		}
		if ClaimOnce(ctx, c, c, r, op, "leases", now) == nil {
			t.Fatal("repeat claim authorized")
		}
		if RevokeUnclaimed(ctx, c, r) == nil {
			t.Fatal("claimed work released")
		}
	})
	t.Run("revocation wins", func(t *testing.T) {
		c, r, op, now := claimFixture(t)
		if err := RevokeUnclaimed(ctx, c, r); err != nil {
			t.Fatal(err)
		}
		if ClaimOnce(ctx, c, c, r, op, "leases", now) == nil {
			t.Fatal("stale claim accepted")
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if ClaimOnce(ctx, c, c, r, op, "leases", now) == nil {
			t.Fatal("revoked claim accepted")
		}
		if r.Status.Claim != nil {
			t.Fatal("revocation created claim")
		}
	})
	t.Run("expired lease", func(t *testing.T) {
		c, r, op, now := claimFixture(t)
		if ClaimOnce(ctx, c, c, r, op, "leases", now.Add(time.Minute)) == nil {
			t.Fatal("expired lease accepted")
		}
	})
	t.Run("ambiguous write", func(t *testing.T) {
		c, r, op, now := claimFixture(t)
		w := ambiguousStatus{Client: c}
		if ClaimOnce(ctx, c, w, r, op, "leases", now) == nil {
			t.Fatal("ambiguous write authorized")
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if r.Status.Claim == nil {
			t.Fatal("fixture did not persist claim")
		}
		if ClaimOnce(ctx, c, c, r, op, "leases", now) == nil {
			t.Fatal("ambiguous dispatch retried")
		}
	})
}

type ambiguousStatus struct{ client.Client }

func (w ambiguousStatus) Status() client.SubResourceWriter {
	return ambiguousWriter{SubResourceWriter: w.Client.Status()}
}

type ambiguousWriter struct{ client.SubResourceWriter }

func (w ambiguousWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("simulated lost update response")
}

// Revoke after the reservation read but before the claim CAS, while the lease
// read is in progress. This is the critical interleaving, not just a stale
// caller arriving after an already completed revocation.
type revokingReader struct {
	client.Reader
	revoke func() error
}

func (r *revokingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*coordination.Lease); ok && r.revoke != nil {
		f := r.revoke
		r.revoke = nil
		if err := f(); err != nil {
			return err
		}
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}
func TestClaimLosesConcurrentRevocation(t *testing.T) {
	ctx := context.Background()
	c, r, op, now := claimFixture(t)
	reader := &revokingReader{Reader: c, revoke: func() error { return RevokeUnclaimed(ctx, c, r) }}
	if ClaimOnce(ctx, reader, c, r, op, "leases", now) == nil {
		t.Fatal("claim overwrote concurrent revocation")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Grant.Phase != "Revoked" || r.Status.Claim != nil {
		t.Fatal("revocation evidence changed")
	}
}
