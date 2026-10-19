// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ValidateOperationBinding checks the immutable reservation against the actual
// journal operation. It neither validates live object incarnations nor permits
// dispatch; those checks and the one-shot claim must be performed separately.
func ValidateOperationBinding(r *ops.ControllerConfigurationReservation, op *cvk.NetworkControllerConfigurationOperation) error {
	if r == nil || r.UID == "" {
		return errors.New("configuration reservation incarnation is unavailable")
	}
	return validateOperationSpec(r, op)
}

func validateOperationSpec(r *ops.ControllerConfigurationReservation, op *cvk.NetworkControllerConfigurationOperation) error {
	reject := errors.New("configuration reservation does not bind the journal operation")
	if r == nil || r.Spec.Protocol != Protocol || r.Namespace == "" || r.DeletionTimestamp != nil {
		return reject
	}
	digest, err := OperationDigest(op)
	if err != nil {
		return reject
	}
	s := &r.Spec
	name, err := ReservationName(s.Controller.UID, s.OperationID)
	if err != nil || r.Name != name || s.OperationID != op.ID || s.OperationDigest != digest || s.Config.Name != op.ConfigName || s.Config.UID != op.ConfigUID || s.ConfigGeneration != op.ConfigGeneration || s.ControllerGeneration != op.ControllerGeneration || s.InventoryID != op.TargetID {
		return reject
	}
	return nil
}
