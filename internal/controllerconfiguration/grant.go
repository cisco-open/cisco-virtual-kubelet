// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordination "k8s.io/api/coordination/v1"
)

// ValidateLiveGrant checks lease evidence at a caller-supplied observation time.
// leaseNamespace must come from trusted device-worker configuration, not status.
// Callers must read both objects uncached and separately validate live device,
// node and worker incarnations. Success is not submission authority: the claim
// CAS, actor admission and uncertain-operation fence are still required.
func ValidateLiveGrant(r *ops.ControllerConfigurationReservation, lease *coordination.Lease, leaseNamespace string, now time.Time) error {
	reject := errors.New("configuration reservation has no valid live lease grant")
	if r == nil || r.Spec.Protocol != Protocol {
		return reject
	}
	name, err := ReservationName(r.Spec.Controller.UID, r.Spec.OperationID)
	if err != nil || r.Name != name {
		return reject
	}
	return validateLeaseGrantEvidence(r, lease, leaseNamespace, now)
}

// The caller must first validate the protocol-specific complete reservation
// binding. This shared check does not authorize acquisition, claim or release.
func validateLeaseGrantEvidence(r *ops.ControllerConfigurationReservation, lease *coordination.Lease, leaseNamespace string, now time.Time) error {
	reject := errors.New("configuration reservation has no valid live lease grant")
	if r == nil || r.Status.PreparationCancelledAt != nil || r.Status.GroupCancellation != nil || lease == nil || now.IsZero() || leaseNamespace == "" || r.Namespace == "" || r.DeletionTimestamp != nil || lease.DeletionTimestamp != nil || r.Spec.Device.Name == "" || r.Spec.Device.UID == "" {
		return reject
	}
	holder, err := Holder(r.Namespace, r)
	if err != nil {
		return reject
	}
	g := r.Status.Grant
	canonical := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	if g == nil || g.Phase != "Held" || g.LeaseNamespace != leaseNamespace || lease.Namespace != leaseNamespace || g.Lease.Name != canonical || lease.Name != canonical || g.Lease.UID == "" || string(lease.UID) != g.Lease.UID || g.Holder != holder || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holder || g.LeaseTransitions < 0 || lease.Spec.LeaseTransitions == nil || *lease.Spec.LeaseTransitions != g.LeaseTransitions {
		return reject
	}
	if g.GrantedAt.IsZero() || g.GrantedAt.After(now) || lease.Spec.AcquireTime == nil || lease.Spec.AcquireTime.IsZero() || lease.Spec.RenewTime == nil || lease.Spec.RenewTime.IsZero() || lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds <= 0 {
		return reject
	}
	acquired, renewed := lease.Spec.AcquireTime.Time, lease.Spec.RenewTime.Time
	// Grant metav1.Time is serialized to whole seconds, while Lease times
	// retain microseconds. Compare acquisition ordering at the grant precision.
	if acquired.Truncate(time.Second).After(g.GrantedAt.Time) || renewed.Before(acquired) || renewed.After(now) || !renewed.Add(time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second).After(now) {
		return reject
	}
	return nil
}
