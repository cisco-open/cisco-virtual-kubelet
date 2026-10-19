// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CompletionForReceipt constructs non-secret completion evidence only from a
// persisted Verified action receipt. Callers must fetch nc uncached and verify
// the controller actor before publishing. It does not authorize Lease release.
func CompletionForReceipt(r *ops.ControllerConfigurationReservation, nc *cvk.NetworkController, now time.Time) (*ops.ConfigurationReservationCompletion, error) {
	reject := errors.New("configuration completion requires a bound verified action receipt")
	if r == nil || nc == nil || now.IsZero() || nc.Namespace != r.Namespace || nc.Name != r.Spec.Controller.Name || string(nc.UID) != r.Spec.Controller.UID || nc.Status.Configuration == nil || r.Status.Claim == nil || r.Status.Grant == nil || (r.Status.Grant.Phase != "Held" && r.Status.Grant.Phase != "Released") {
		return nil, reject
	}
	claim := r.Status.Claim
	if claim.OperationID != r.Spec.OperationID || claim.OperationDigest != r.Spec.OperationDigest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.After(now) {
		return nil, reject
	}
	var matched *cvk.NetworkControllerConfigurationOperation
	// A DELETE request does not invalidate historical receipt evidence while
	// the finalizer retains this exact reservation. Dispatch still validates the
	// original object and rejects deletion; only this audit uses the live binding.
	receiptBinding := r.DeepCopy()
	receiptBinding.DeletionTimestamp = nil
	for i := range nc.Status.Configuration.ActionApplications {
		receipt := &nc.Status.Configuration.ActionApplications[i]
		if receipt.Operation.ID != r.Spec.OperationID {
			continue
		}
		op := &receipt.Operation
		if matched != nil || receipt.Section != op.Section || receipt.Resource == "" || op.Phase != "Verified" || op.TaskID == "" || len(op.TaskID) > 128 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.Before(&claim.ClaimedAt) || op.SubmittedAt.After(now) || ValidateOperationBinding(receiptBinding, op) != nil {
			return nil, reject
		}
		matched = op
	}
	if matched == nil {
		return nil, reject
	}
	// A retained journal for this operation must agree with its receipt. A newer
	// operation is permitted: the verified action receipt survives journal release.
	if active := nc.Status.Configuration.Operation; active != nil && active.ID == matched.ID && !reflect.DeepEqual(active, matched) {
		return nil, reject
	}
	return &ops.ConfigurationReservationCompletion{OperationID: matched.ID, OperationDigest: r.Spec.OperationDigest, TaskID: matched.TaskID, SubmittedAt: *matched.SubmittedAt.DeepCopy(), VerifiedAt: metav1.NewTime(now)}, nil
}

// ValidateCompletionReceipt independently correlates completion evidence with
// the controller's retained action receipt. A deletion request does not erase
// verified evidence on the same controller UID. Expiry alone never settles work.
func ValidateCompletionReceipt(r *ops.ControllerConfigurationReservation, nc *cvk.NetworkController, now time.Time) error {
	if r == nil || r.Status.Completion == nil {
		return errors.New("configuration completion is absent")
	}
	c := r.Status.Completion
	if c.VerifiedAt.IsZero() || c.VerifiedAt.After(now) {
		return errors.New("configuration completion timestamp is invalid")
	}
	expected, err := CompletionForReceipt(r, nc, c.VerifiedAt.Time)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, c) {
		return errors.New("configuration completion receipt changed")
	}
	return nil
}
