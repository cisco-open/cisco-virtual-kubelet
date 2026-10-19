// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// WithGroupIdentity constructs a local Prepared journal record. It performs no
// API write and cannot upgrade an accepted/ambiguous operation into a new scope.
func WithGroupIdentity(op *cvk.NetworkControllerConfigurationOperation, targets []GroupTargetIdentity) (*cvk.NetworkControllerConfigurationOperation, error) {
	if op == nil || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || op.CancellationRequestedAt != nil {
		return nil, errors.New("group identity requires an unsubmitted prepared operation")
	}
	canonical, err := CanonicalGroupTargets(targets)
	if err != nil {
		return nil, err
	}
	digest, err := GroupOperationDigest(op, canonical)
	if err != nil {
		return nil, err
	}
	if op.PhysicalGroup != nil {
		if err := ValidateGroupJournalIdentity(op); err != nil {
			return nil, err
		}
		if digest != op.PhysicalGroup.Digest {
			return nil, errors.New("prepared group identity cannot change")
		}
	}
	copy := op.DeepCopy()
	copy.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{Version: GroupIdentityVersion, Digest: digest, Targets: canonical}
	return copy, nil
}

func ValidateGroupJournalIdentity(op *cvk.NetworkControllerConfigurationOperation) error {
	if op == nil || op.PhysicalGroup == nil || op.PhysicalGroup.Version != GroupIdentityVersion {
		return errors.New("group journal identity is unavailable")
	}
	if (op.Phase == "Cancelling") != (op.CancellationRequestedAt != nil) {
		return errors.New("group cancellation phase and fence disagree")
	}
	if op.CancellationRequestedAt != nil && (op.CancellationRequestedAt.IsZero() || op.SubmittedAt != nil || op.TaskID != "") {
		return errors.New("group cancellation cannot follow submission")
	}
	digest, err := GroupOperationDigest(op, op.PhysicalGroup.Targets)
	if err != nil || op.PhysicalGroup.Digest != digest {
		return errors.New("group journal identity does not bind operation and targets")
	}
	return nil
}
