// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwarelifecycle

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

var _ lifecycle.PreparationRetirementObserver = (*Adapter)(nil)

// ObservePreparationRetirement never modifies native inventory. Invalidation
// revokes old intent, not files. IOS XE's stale in-progress marker requires
// exact receipt/native-history corroboration; idle state alone never suffices.
func (a *Adapter) ObservePreparationRetirement(ctx context.Context, request lifecycle.PreparationRetirementRequest) (lifecycle.PreparationRetirementObservation, error) {
	target, running := request.TargetVersion, request.RunningVersion
	if err := lifecycle.ValidateTargetVersion(target); err != nil {
		return lifecycle.PreparationRetirementObservation{}, err
	}
	if err := lifecycle.ValidateTargetVersion(running); err != nil {
		return lifecycle.PreparationRetirementObservation{}, err
	}
	var raw []byte
	var deviceTime, localTime time.Time
	var err error
	if timed, ok := a.transport.(deviceTimeFetcher); ok {
		raw, deviceTime, localTime, err = timed.FetchWithDeviceTime(ctx, installOperDataPath)
	} else {
		raw, err = a.transport.Fetch(ctx, installOperDataPath)
	}
	if err != nil {
		return lifecycle.PreparationRetirementObservation{}, fmt.Errorf("read native retirement evidence: %w", err)
	}
	return preparationRetirementFromJSON(raw, request, deviceTime, localTime)
}

func preparationRetirementFromJSON(raw []byte, request lifecycle.PreparationRetirementRequest, deviceTime, localTime time.Time) (lifecycle.PreparationRetirementObservation, error) {
	var result lifecycle.PreparationRetirementObservation
	target, running := request.TargetVersion, request.RunningVersion
	root, err := decodeObject(raw)
	if err != nil {
		return result, err
	}
	// Use the very same native snapshot for inventory, history and quiescence.
	// A separate Inspect call could race a replacement or installer transition.
	image, imageErr := inventoryImageFromNode(root, target)
	corroborated := false
	if imageErr == nil && image.State == lifecycle.InventoryStateInProgress {
		if err := corroborateRetirement(root, image, request, deviceTime, localTime); err != nil {
			return result, err
		}
		corroborated = true
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
				state == lifecycle.InventoryStateUnknown ||
				(state == lifecycle.InventoryStateInProgress && !(corroborated && identity == target)) ||
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
			if image.Version != target || (!image.State.Activatable() &&
				!(corroborated && image.State == lifecycle.InventoryStateInProgress)) {
				return result, fmt.Errorf("retirement target is not the exact inactive image")
			}
			stateOfTarget = image.State
			if corroborated && stateOfTarget == lifecycle.InventoryStateInProgress {
				stateOfTarget = lifecycle.InventoryStateInstalled
			}
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

// Corroborate only the immutable preparation interval. Recovery requests may
// arrive days later: widening that interval to "now" could match an unrelated
// reinstall. Require the response's clock mapping, verified source size, all
// packages added and one exact successful add, then reject any later/unknown
// activity. Missing history or clock evidence stays held for explicit recovery.
func corroborateRetirement(root map[string]any, image lifecycle.InventoryImage, request lifecycle.PreparationRetirementRequest, deviceTime, localTime time.Time) error {
	if image.Version != request.TargetVersion || request.SourceSize <= 0 ||
		request.InstallStartedAt.IsZero() || request.PreparedAt.IsZero() ||
		request.PreparedAt.Before(request.InstallStartedAt) || deviceTime.IsZero() || localTime.IsZero() ||
		request.PreparedAt.After(localTime) {
		return fmt.Errorf("in-progress retirement requires exact receipt interval, source size and native response clock")
	}
	correlation := lifecycle.InterruptedInstallRequest{
		TargetVersion: request.TargetVersion, SourceSize: request.SourceSize,
		NotBefore: request.InstallStartedAt, ObservedAt: request.PreparedAt,
		DeviceNotBefore:  deviceTime.Add(request.InstallStartedAt.Sub(localTime)),
		DeviceObservedAt: deviceTime.Add(request.PreparedAt.Sub(localTime)),
	}
	completed, err := correlateInterruptedInstall(root, image, correlation)
	if err != nil {
		return fmt.Errorf("in-progress retirement is not corroborated: %w", err)
	}
	for _, list := range []string{"install-oper", "install-oper-hist"} {
		operations, _, err := collectNamedList(root, list)
		if err != nil {
			return err
		}
		for _, operation := range operations {
			state := normalizeOperationState(stringField(operation, "op-status"), stringField(operation, "op-done"))
			start, startErr := time.Parse(time.RFC3339Nano, stringField(operation, "start-time"))
			end, endErr := time.Parse(time.RFC3339Nano, stringField(operation, "end-time"))
			if (state != lifecycle.OperationStateSucceeded && state != lifecycle.OperationStateFailed) ||
				startErr != nil || endErr != nil || end.Before(start) || end.After(completed) {
				return fmt.Errorf("native history contains later or unresolved activity after the retained preparation")
			}
		}
	}
	return nil
}
