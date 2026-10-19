// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

type configurationDeviceEvidence struct{ outputs []string }

func (configurationDeviceEvidence) String() string   { return "<private device evidence>" }
func (configurationDeviceEvidence) GoString() string { return "<private device evidence>" }

// Copy and validate the complete query plan before creating any read request.
// DeviceOperation's platform policy remains responsible for CLI authorization.
func freezeConfigurationDeviceProofQueries(queries map[string][]string, targets []contract.GroupTargetIdentity) (map[string][]string, error) {
	if len(queries) != len(targets) || len(targets) == 0 {
		return nil, errConfigurationStale
	}
	requested := make(map[string][]string, len(queries))
	seenTargets := map[string]bool{}
	for _, target := range targets {
		id := target.InventoryID
		commands, ok := queries[id]
		if !ok || !validAPIID(id) || seenTargets[id] {
			return nil, errConfigurationStale
		}
		seenTargets[id] = true
		if len(commands) < 2 || len(commands) > 16 || commands[0] != "show version" {
			return nil, errConfigurationTransition
		}
		seen := map[string]bool{}
		for _, command := range commands {
			if !strings.HasPrefix(command, "show ") || strings.TrimSpace(command) != command || len(command) > 1024 || strings.ContainsFunc(command, unicode.IsControl) || seen[command] {
				return nil, errConfigurationTransition
			}
			seen[command] = true
		}
		requested[id] = append([]string(nil), commands...)
	}
	return requested, nil
}

// Resource-specific callers bracket this read-only batch with their complete
// native/source/managed observations. No grants, journal phases or completion
// receipts are changed here. Protected worker bindings remain manager-owned.
func (a *adapter) readConfigurationDeviceProofBatch(ctx context.Context, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, physical map[string]Device, queries map[string][]string, result string, completed, now time.Time, namePrefix, annotation string, pending error, check func() error) (map[string]configurationDeviceEvidence, error) {
	if a == nil || a.statusReader == nil || a.statusWriter == nil || nc == nil || op == nil || op.Phase != "Verifying" || contract.ValidateGroupJournalIdentity(op) != nil || check == nil || pending == nil || completed.IsZero() || now.IsZero() || namePrefix == "" || annotation == "" || result == "" || nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) {
		return nil, errConfigurationTransition
	}
	if !now.UTC().Truncate(time.Second).After(completed) {
		return nil, pending
	}
	requested, err := freezeConfigurationDeviceProofQueries(queries, op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	bound, err := contract.ValidateGroupReservationSet(nc, members)
	if err != nil {
		return nil, err
	}
	// Freeze expected identity across callbacks; they cannot rewrite requests.
	nc = nc.DeepCopy()
	op = op.DeepCopy()
	copies := make([]*ops.ControllerConfigurationReservation, len(bound))
	for i, m := range bound {
		copies[i] = m.DeepCopy()
	}
	frozenPhysical := make(map[string]Device, len(copies))
	for _, m := range copies {
		d, ok := physical[m.Spec.InventoryID]
		serial, err := topologyidentity.CanonicalPhysicalIdentity(d.Serial)
		if !ok || d.ID != m.Spec.InventoryID || err != nil || serial != m.Spec.PhysicalIdentity {
			return nil, errConfigurationStale
		}
		frozenPhysical[m.Spec.InventoryID] = d
	}
	physical = frozenPhysical
	evidence := make(map[string]configurationDeviceEvidence, len(copies))
	waiting := false
	for _, member := range copies {
		if err := check(); err != nil {
			return nil, err
		}
		id := member.Spec.InventoryID
		expected := configurationDeviceProofOperation(nc, op, member, result, completed, requested[id], namePrefix, annotation)
		outputs, err := a.readConfigurationDeviceProof(ctx, expected, member, annotation, completed, now, pending)
		if errors.Is(err, pending) {
			waiting = true
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := verifyConfigurationChassisSerial(physical[id].Serial, outputs[0]); err != nil {
			return nil, err
		}
		evidence[id] = configurationDeviceEvidence{outputs: outputs}
	}
	if waiting {
		return nil, pending
	}
	return evidence, nil
}

// Same read-only LISP observation plan used by SDA transit evidence. This is
// deliberately not a role/configuration semantic convergence predicate.
func fabricDeviceEvidenceCommands() []string {
	return []string{"show version", "show lisp session all"}
}
