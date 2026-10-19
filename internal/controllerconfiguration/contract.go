// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// Package controllerconfiguration defines non-secret configuration reservation
// identities shared by future manager, device-worker and controller adapters.
// It contains no lease acquisition or submission authority.
package controllerconfiguration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"regexp"
)

const Protocol = managedprotocol.ConfigurationProtocolVersion
const Finalizer = "ops.cisco.vk/configuration-reservation-evidence"

var operationIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ReservationName(controllerUID, operationID string) (string, error) {
	if controllerUID == "" || !operationIDPattern.MatchString(operationID) {
		return "", errors.New("incomplete configuration reservation identity")
	}
	sum := sha256.Sum256([]byte(controllerUID + "\x00" + operationID))
	return "config-" + hex.EncodeToString(sum[:16]), nil
}
func Holder(namespace string, r *ops.ControllerConfigurationReservation) (string, error) {
	if r == nil || r.UID == "" || r.Namespace != namespace || r.Name == "" {
		return "", errors.New("configuration reservation incarnation is unavailable")
	}
	return devicecoordination.HolderIdentity("controller-configuration", namespace, r.Name, string(r.UID)), nil
}

// OperationDigest binds immutable journal/source/target fields. Task receipt,
// phase and submission time advance without changing this reservation binding.
func OperationDigest(op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || !operationIDPattern.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || op.Resource == "" || op.Section == "" || op.TargetID == "" || op.TargetSerial == "" || (op.Action != "Create" && op.Action != "Update" && op.Action != "Delete") || len(op.CredentialVersions) > 0 {
		return "", errors.New("incomplete physical configuration operation")
	}
	if err := validateOperationDependencies(op); err != nil {
		return "", err
	}
	copy := op.DeepCopy()
	copy.Phase = ""
	copy.TaskID = ""
	copy.SubmittedAt = nil
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
