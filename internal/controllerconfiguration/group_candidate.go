// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
)

// GroupReservationProtocol identifies proposed per-member reservations. The
// CRD and actor admission accept it, while runtime activation remains gated.
// Candidate construction must not be mistaken for enabling this protocol.
const GroupReservationProtocol = "controller-configuration-group-v1"

// GroupMemberSnapshot supplies observed objects, not execution permission.
// Callers must establish cluster-wide exclusive physical identity, fresh reads,
// intent/worker ownership and installed compatibility before any future write.
type GroupMemberSnapshot struct {
	Device *cvk.CiscoDevice
	Node   *core.Node
	Worker *core.Pod
}

// GroupReservationCandidates binds every member to the SAME complete operation
// digest and connector incarnation. It returns serial-ordered candidates only
// if all members validate. No partial result, API write, grant or claim is made.
func GroupReservationCandidates(nc *cvk.NetworkController, members []GroupMemberSnapshot, connector *core.Pod, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	return groupReservationCandidates(nc, members, connector, now, false)
}

func groupReservationCandidates(nc *cvk.NetworkController, members []GroupMemberSnapshot, connector *core.Pod, now time.Time, cancelling bool) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group reservation candidates require the complete current target set")
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
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
	if len(members) != len(targets) {
		return nil, reject
	}
	snapshots := make(map[string]GroupMemberSnapshot, len(members))
	for _, member := range members {
		if member.Device == nil || member.Node == nil || member.Worker == nil || member.Device.UID == "" {
			return nil, reject
		}
		uid := string(member.Device.UID)
		if _, exists := snapshots[uid]; exists {
			return nil, reject
		}
		snapshots[uid] = member
	}
	result := make([]*ops.ControllerConfigurationReservation, 0, len(targets))
	workerUIDs := map[string]bool{}
	for _, target := range targets {
		member, found := snapshots[target.Device.UID]
		if !found || member.Device.Name != target.Device.Name || member.Node.Name != target.Node.Name || string(member.Node.UID) != target.Node.UID || workerUIDs[string(member.Worker.UID)] {
			return nil, reject
		}
		workerUIDs[string(member.Worker.UID)] = true
		name := groupMemberReservationName(string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity)
		candidate, err := reservationCandidateForTarget(nc, member.Device, member.Node, member.Worker, connector, now, GroupReservationProtocol, name, op.PhysicalGroup.Digest, target.PhysicalIdentity, target.InventoryID, cancelling)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, nil
}

// The complete group digest participates in each name. Changing any member
// cannot accidentally reuse another member's previous reservation identity.
func groupMemberReservationName(controllerUID, operationID, digest, physical string) string {
	raw, _ := json.Marshal([]string{GroupReservationProtocol, controllerUID, operationID, digest, physical})
	sum := sha256.Sum256(raw)
	return "config-group-" + hex.EncodeToString(sum[:16])
}
