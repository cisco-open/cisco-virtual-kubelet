// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"time"
)

// RequiresQuarantine recognizes durable reservation evidence, independently of
// Lease expiry. Only an admitted device-worker release can settle claimed work;
// the worker must have independently verified the retained controller receipt.
// This predicate does not authorize release or remote submission. Deletion does
// not suppress risk while the evidence finalizer retains the object.
func RequiresQuarantine(r *ops.ControllerConfigurationReservation, now time.Time) bool {
	if r == nil {
		return false
	}
	if r.Spec.Protocol != Protocol && r.Spec.Protocol != GroupReservationProtocol {
		return true
	}
	if r.Status.GroupCancellation != nil {
		return requiresCancelledGroupQuarantine(r, now)
	}
	if r.Spec.Protocol == GroupReservationProtocol {
		// A group release is meaningful only for a canonical member with a
		// completed claim. Unclaimed cancellation uses a separate protocol.
		if r.Status.Claim == nil || !validGroupIdentityToken(r.Spec.Controller.UID) || !validGroupIdentityToken(r.Spec.PhysicalIdentity) || r.Name != groupMemberReservationName(r.Spec.Controller.UID, r.Spec.OperationID, r.Spec.OperationDigest, r.Spec.PhysicalIdentity) {
			return true
		}
	}
	s := &r.Status
	if s.PreparationCancelledAt != nil && (s.Grant != nil || s.Claim != nil || s.Completion != nil) {
		return true
	}
	if s.Grant == nil {
		return s.Claim != nil || s.Completion != nil
	}
	if s.Grant.Phase != "Released" {
		return true
	}
	if s.Claim == nil {
		return s.Completion != nil
	}
	c := s.Completion
	if c == nil || s.Claim.OperationID != r.Spec.OperationID || s.Claim.OperationDigest != r.Spec.OperationDigest || c.OperationID != r.Spec.OperationID || c.OperationDigest != r.Spec.OperationDigest || c.TaskID == "" || len(c.TaskID) > 128 || now.IsZero() {
		return true
	}
	if s.Grant.GrantedAt.IsZero() || s.Claim.ClaimedAt.IsZero() || c.SubmittedAt.IsZero() || c.VerifiedAt.IsZero() {
		return true
	}
	return s.Claim.ClaimedAt.Before(&s.Grant.GrantedAt) || c.SubmittedAt.Before(&s.Claim.ClaimedAt) || c.VerifiedAt.Before(&c.SubmittedAt) || c.VerifiedAt.After(now)
}

// An admitted cancellation release is distinct from verified completion. The
// worker has checked the durable no-submission fence before writing Released;
// this local predicate merely recognizes that retained evidence, not authority.
func requiresCancelledGroupQuarantine(r *ops.ControllerConfigurationReservation, now time.Time) bool {
	s, c := &r.Status, r.Status.GroupCancellation
	if r.Spec.Protocol != GroupReservationProtocol || c == nil || now.IsZero() || (s.Grant != nil && s.Grant.Phase != "Released") || s.Completion != nil || s.PreparationCancelledAt != nil || !operationIDPattern.MatchString(r.Spec.OperationID) || len(r.Spec.OperationDigest) != 64 || r.Name != groupMemberReservationName(r.Spec.Controller.UID, r.Spec.OperationID, r.Spec.OperationDigest, r.Spec.PhysicalIdentity) {
		return true
	}
	holder, err := Holder(r.Namespace, r)
	if err != nil || c.OperationID != r.Spec.OperationID || c.OperationDigest != r.Spec.OperationDigest || c.RequestedAt.IsZero() || c.AcknowledgedAt.IsZero() || c.AcknowledgedAt.Before(&c.RequestedAt) || c.AcknowledgedAt.After(now) {
		return true
	}
	if s.Grant == nil {
		return s.Claim != nil || s.ManagerSettledAt == nil || s.ManagerSettledAt.IsZero() || s.ManagerSettledAt.Before(&c.AcknowledgedAt) || s.ManagerSettledAt.After(now)
	}
	if s.Grant.Holder != holder || s.Grant.GrantedAt.IsZero() || s.Grant.GrantedAt.After(now) {
		return true
	}
	if claim := s.Claim; claim != nil {
		return claim.OperationID != r.Spec.OperationID || claim.OperationDigest != r.Spec.OperationDigest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.Before(&s.Grant.GrantedAt) || claim.ClaimedAt.After(now)
	}
	return false
}
