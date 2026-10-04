// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwarelifecycle

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

var _ lifecycle.PreparationRetirementObserver = (*Adapter)(nil)

// ObservePreparationRetirement never modifies native inventory. Invalidation
// revokes old intent, not files. Unknown/stale in-progress inventory is held
// even if one location claims no activity; no generic absence shortcut exists.
func (a *Adapter) ObservePreparationRetirement(ctx context.Context, target, running string) (lifecycle.PreparationRetirementObservation, error) {
	if err := lifecycle.ValidateTargetVersion(target); err != nil {
		return lifecycle.PreparationRetirementObservation{}, err
	}
	if err := lifecycle.ValidateTargetVersion(running); err != nil {
		return lifecycle.PreparationRetirementObservation{}, err
	}
	raw, err := a.transport.Fetch(ctx, installOperDataPath)
	if err != nil {
		return lifecycle.PreparationRetirementObservation{}, fmt.Errorf("read native retirement evidence: %w", err)
	}
	return preparationRetirementFromJSON(raw, target, running)
}

func preparationRetirementFromJSON(raw []byte, target, running string) (lifecycle.PreparationRetirementObservation, error) {
	var result lifecycle.PreparationRetirementObservation
	root, err := decodeObject(raw)
	if err != nil {
		return result, err
	}
	locations, found, err := collectNamedList(root, "install-location-information")
	if err != nil || !found || len(locations) == 0 {
		return result, fmt.Errorf("retirement requires complete install locations")
	}
	for _, location := range locations {
		state, ok := objectField(location, "oper-state")
		if !ok || stringField(state, "sys-activity") != "install-no-activity" {
			return result, fmt.Errorf("retirement requires a quiescent native installer at every location")
		}
		versions, found, err := directNamedList(location, "install-version-info")
		if err != nil || !found || len(versions) == 0 {
			return result, fmt.Errorf("retirement requires complete version inventory at every location")
		}
		seen, committed := make(map[string]bool), 0
		for _, version := range versions {
			entry := inventoryVersion{Version: stringField(version, "version"), VersionExtension: stringField(version, "version-extension")}
			identity := entry.identity()
			state := normalizeInventoryState(stringField(version, "current"))
			if lifecycle.ValidateTargetVersion(identity) != nil || seen[identity] ||
				state == lifecycle.InventoryStateUnknown || state == lifecycle.InventoryStateInProgress ||
				state == lifecycle.InventoryStateProvisionedUncommitted {
				return result, fmt.Errorf("retirement inventory contains ambiguous or unsettled version records")
			}
			seen[identity] = true
			if state == lifecycle.InventoryStateProvisionedCommitted {
				committed++
			}
		}
		if committed != 1 {
			return result, fmt.Errorf("retirement requires exactly one committed running image per location")
		}
		// Evaluate each location independently: a missing running image on one
		// member must not be masked by a committed image on another member.
		one := map[string]any{"install-location-information": []any{location}}
		image, err := inventoryImageFromNode(one, running)
		if err != nil || image.State != lifecycle.InventoryStateProvisionedCommitted {
			return result, fmt.Errorf("original running image is not uniquely committed at every location")
		}
		image, err = inventoryImageFromNode(one, target)
		stateOfTarget := lifecycle.InventoryStateAbsent
		if err == nil {
			if image.Version != target || !image.State.Activatable() {
				return result, fmt.Errorf("retirement target is not the exact inactive image")
			}
			stateOfTarget = image.State
		} else if !errors.Is(err, lifecycle.ErrTargetNotFound) {
			return result, err
		}
		if result.TargetState != "" && result.TargetState != stateOfTarget {
			return result, fmt.Errorf("retirement target differs across install locations")
		}
		result.TargetState = stateOfTarget
	}
	active, _, err := collectNamedList(root, "install-oper")
	if err != nil {
		return result, err
	}
	for _, operation := range active {
		state := normalizeOperationState(stringField(operation, "op-status"), stringField(operation, "op-done"))
		if state != lifecycle.OperationStateSucceeded && state != lifecycle.OperationStateFailed {
			return result, fmt.Errorf("native install operation remains active or unknown")
		}
	}
	result.EvidenceHash = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	return result, nil
}
