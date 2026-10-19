// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package mutationguard

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func TestConfigurationRiskSurvivesLeaseExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "reservation", Namespace: "default", UID: "reservation-uid", Finalizers: []string{controllerconfiguration.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: controllerconfiguration.Protocol, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device-uid"}, OperationID: strings.Repeat("a", 32), OperationDigest: strings.Repeat("b", 64)}, Status: ops.ControllerConfigurationReservationStatus{Grant: &ops.ConfigurationReservationGrant{Phase: "Held", GrantedAt: metav1.NewTime(now.Add(-2 * time.Hour))}, Claim: &ops.ConfigurationReservationClaim{OperationID: strings.Repeat("a", 32), OperationDigest: strings.Repeat("b", 64), ClaimedAt: metav1.NewTime(now.Add(-time.Hour))}}}
	holder, _ := controllerconfiguration.Holder("default", r)
	key := devicecoordination.DeviceKey("default", "switch")
	duration := int32(60)
	renewed := metav1.NewMicroTime(now.Add(-time.Hour))
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: engine.LeaseName(key, devicecoordination.MutationLeaseFamily), Namespace: "default"}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, RenewTime: &renewed, LeaseDurationSeconds: &duration}}
	c := fake.NewClientBuilder().WithScheme(guardScheme(t)).WithStatusSubresource(r).WithObjects(r, lease).Build()
	leaser := &engine.FamilyLeaser{Client: c, Namespace: "default", TTL: time.Minute}
	result, err := EnsureCanonicalQuarantine(ctx, c, leaser, "default", "switch", key, "competing-upgrade", now)
	if err != nil || result.Risk == nil || !result.LeaseOwned || result.CallerOwnsRisk || result.Risk.HolderIdentity != holder {
		t.Fatalf("quarantine: %#v %v", result, err)
	}
	if err = c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	r.Status.Grant.Phase = "Released"
	if err = c.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	risk, err := FindCanonicalRisk(ctx, c, "default", "switch", now)
	if err != nil || risk == nil {
		t.Fatalf("missing completion cleared risk: %v", err)
	}
	r.Status.Completion = &ops.ConfigurationReservationCompletion{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, TaskID: "task", SubmittedAt: metav1.NewTime(now.Add(-time.Hour)), VerifiedAt: metav1.NewTime(now.Add(-time.Minute))}
	if err = c.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	risk, err = FindCanonicalRisk(ctx, c, "default", "switch", now)
	if err != nil || risk != nil {
		t.Fatalf("released verified work remains risky: %#v %v", risk, err)
	}
	// Unknown protocols and deleting unresolved reservations never age out.
	r.Spec.Protocol = "future"
	r.DeletionTimestamp = &metav1.Time{Time: now}
	if !controllerconfiguration.RequiresQuarantine(r, now.Add(365*24*time.Hour)) {
		t.Fatal("unknown deleting evidence aged out")
	}
}

type reservationListFailure struct {
	client.Reader
	err error
}

func (r reservationListFailure) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*ops.ControllerConfigurationReservationList); ok {
		return r.err
	}
	return r.Reader.List(ctx, list, opts...)
}
func TestConfigurationRiskDiscoveryFailure(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(guardScheme(t)).Build()
	ctx := context.Background()
	if _, err := FindCanonicalRisk(ctx, reservationListFailure{Reader: c, err: errors.New("forbidden or unavailable")}, "default", "switch", time.Now()); err == nil {
		t.Fatal("failed-open on risk read error")
	}
	risk, err := FindCanonicalRisk(ctx, reservationListFailure{Reader: c, err: &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "ops.cisco.vk", Kind: "ControllerConfigurationReservationList"}}}, "default", "switch", time.Now())
	if err != nil || risk != nil {
		t.Fatalf("absent additive API broke legacy guard: %v", err)
	}
}
