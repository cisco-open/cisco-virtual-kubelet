// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"errors"
	"slices"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ValidateGroupReservationSet checks persisted member identities against the
// complete journal group. Results are deep copies in canonical acquisition
// order. It does not check live workers, intent, grants, claims or leases and
// cannot authorize dispatch or release. Missing members are never inferred.
func ValidateGroupReservationSet(nc *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("configuration reservations do not bind the complete group journal")
	if nc == nil || nc.Namespace == "" || nc.Name == "" || nc.UID == "" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	if err := ValidateGroupJournalIdentity(op); err != nil {
		return nil, err
	}
	targets, err := CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	if len(targets) != len(reservations) {
		return nil, reject
	}
	byPhysical := make(map[string]*ops.ControllerConfigurationReservation, len(reservations))
	targetByPhysical := make(map[string]GroupTargetIdentity, len(targets))
	for _, target := range targets {
		targetByPhysical[target.PhysicalIdentity] = target
	}
	seenUID := map[string]bool{}
	for _, r := range reservations {
		if r == nil || seenUID[string(r.UID)] {
			return nil, reject
		}
		target, found := targetByPhysical[r.Spec.PhysicalIdentity]
		if !found || validateGroupReservationMember(nc, r, target) != nil || byPhysical[r.Spec.PhysicalIdentity] != nil {
			return nil, reject
		}
		seenUID[string(r.UID)] = true
		byPhysical[r.Spec.PhysicalIdentity] = r
	}
	result := make([]*ops.ControllerConfigurationReservation, 0, len(targets))
	var connector ops.ConfigurationReservationReference
	var username string
	for _, target := range targets {
		r := byPhysical[target.PhysicalIdentity]
		if r == nil {
			return nil, reject
		}
		s := &r.Spec
		if len(result) == 0 {
			connector = s.ControllerWorkerPod
			username = s.ControllerUsername
		} else if s.ControllerWorkerPod != connector || s.ControllerUsername != username {
			return nil, reject
		}
		result = append(result, r.DeepCopy())
	}
	return result, nil
}

// The caller must first validate the full group journal identity and obtain
// target from that canonical set. This validates ONE member, not completeness.
func validateGroupReservationMember(nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation, target GroupTargetIdentity) error {
	reject := errors.New("reservation does not bind its journal group member")
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || r == nil || r.UID == "" || r.Namespace != nc.Namespace || r.DeletionTimestamp != nil || len(r.OwnerReferences) != 0 || !slices.Contains(r.Finalizers, Finalizer) {
		return reject
	}
	op, s := nc.Status.Configuration.Operation, &r.Spec
	if op.PhysicalGroup == nil || s.Protocol != GroupReservationProtocol || s.Controller.Name != nc.Name || s.Controller.UID != string(nc.UID) || s.OperationID != op.ID || s.OperationDigest != op.PhysicalGroup.Digest || s.Config.Name != op.ConfigName || s.Config.UID != op.ConfigUID || s.ControllerGeneration != op.ControllerGeneration || s.ConfigGeneration != op.ConfigGeneration || s.PhysicalIdentity != target.PhysicalIdentity || s.Device != target.Device || s.Node != target.Node || s.InventoryID != target.InventoryID || r.Name != groupMemberReservationName(string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity) || s.ControllerWorkerPod.Name == "" || s.ControllerWorkerPod.UID == "" || s.ControllerUsername == "" {
		return reject
	}
	return nil
}
