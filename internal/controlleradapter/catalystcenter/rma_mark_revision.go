// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// A mark belongs to the native record and physical device, not the desired
// replacement serial or current source generation. This permits the model to
// advance from MARK_FOR_REPLACEMENT to REPLACE without repeating the mark POST.
// It does not authorize that later workflow or silently recreate a deleted mark.
func deviceReplacementMarkApplied(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, snapshot *deviceReplacementSnapshot, now time.Time) (bool, error) {
	if state == nil || config == nil || snapshot == nil {
		return false, errConfigurationTransition
	}
	scope, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		return false, err
	}
	receipt, err := ownedRMAMarkReceipt(state, config, scope, snapshot.target.ID, snapshot.serial, snapshot.siteID, now)
	if err != nil {
		return false, err
	}
	if receipt == nil {
		if snapshot.record != nil {
			return false, errConfigurationOwned
		}
		return false, nil
	}
	op := receipt.Operation
	id := strings.TrimPrefix(receipt.Resource, "rma-mark-record:")
	record := snapshot.record
	if record == nil || record.ID != id || record.FaultyID != op.TargetID {
		return false, errConfigurationStale
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
	if err != nil || serial != op.TargetSerial || record.CreatedAt <= 0 || record.CreatedAt < op.SubmittedAt.Add(-configurationTaskClockSkew).UnixMilli() || record.CreatedAt > receipt.VerifiedAt.Add(configurationTaskClockSkew).UnixMilli() {
		return false, errConfigurationStale
	}
	if record.Status != "MARKED-FOR-REPLACEMENT" && record.Status != "READY-FOR-REPLACEMENT" || record.WorkflowID != "" || record.WorkflowFailedStep != "" || record.ReplacedAt != 0 || record.ReplacementSerial != "" {
		return false, errConfigurationTransition
	}
	return true, nil
}

// Validate historical ownership independently of the present native status.
// Cleanup recovery must retain this authority even after the native mark is
// removed; it must not invent a MARKED snapshot to validate an old receipt.
func ownedRMAMarkReceipt(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, scope, targetID, serial, siteID string, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if state == nil || config == nil {
		return nil, errConfigurationTransition
	}
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section != "inventory" {
			continue
		}
		if owner.ConfigName != config.Name || owner.ConfigUID != string(config.UID) {
			return nil, errConfigurationOwned
		}
		owners++
	}
	if owners != 1 {
		return nil, errConfigurationOwned
	}
	var found *cvk.NetworkControllerConfigurationActionApplication
	for _, receipt := range state.ActionApplications {
		op := receipt.Operation
		if !strings.HasPrefix(receipt.Resource, "rma-mark-record:") && !strings.HasPrefix(op.Resource, "rma-mark:") {
			continue
		}
		id, ok := strings.CutPrefix(receipt.Resource, "rma-mark-record:")
		digest, valid := strings.CutPrefix(op.Resource, "rma-mark:")
		decoded, err := hex.DecodeString(digest)
		if !ok || !validAPIID(id) || !valid || err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest || receipt.Section != "inventory" || op.Section != "inventory" {
			return nil, errConfigurationStale
		}
		if op.TargetID != targetID && op.TargetSerial != serial {
			continue
		}
		if found != nil || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) {
			return nil, errConfigurationOwned
		}
		found = receipt.DeepCopy()
		if scope != op.Resource {
			return nil, errConfigurationStale
		}
		if op.TargetID != targetID || op.TargetSerial != serial || op.ParentID != siteID || op.Phase != "Verified" || op.Action != "Create" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(now) {
			return nil, errConfigurationStale
		}

	}
	return found, nil
}

// Planning after settlement. Existing action receipts remain the
// authority; active journals must resume through their exact-operation executor.
func (a *adapter) planOwnedDeviceReplacementMark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent) (*configurationWrite, error) {
	return a.planOwnedDeviceReplacementAction(ctx, config, source, ref, want, func(ctx context.Context, state *cvk.NetworkControllerConfigurationStatus, snapshot *deviceReplacementSnapshot) (*configurationWrite, error) {
		applied, err := deviceReplacementMarkApplied(state, config, snapshot, time.Now())
		if err != nil {
			return nil, err
		}
		if applied {
			return nil, nil
		}
		cleanup, err := deviceReplacementUnmarkApplied(state, config, snapshot, time.Now())
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			if err := a.retireCleanupForNewMark(ctx, config, source, ref, want, cleanup.Operation.ID); err != nil {
				return nil, err
			}
			return nil, errConfigurationReplan
		}
		return a.client.planDeviceReplacementMark(ctx, ref, want)
	})
}

// Share current source, ownership, receipt and native snapshot checks between
// mark planning and explicitly authorized cleanup planning.
func (a *adapter) planOwnedDeviceReplacementAction(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent, plan func(context.Context, *cvk.NetworkControllerConfigurationStatus, *deviceReplacementSnapshot) (*configurationWrite, error)) (*configurationWrite, error) {
	if a == nil || config == nil || plan == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	snapshot, err := a.client.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return nil, err
	}
	write, err := plan(ctx, nc.Status.Configuration, snapshot)
	if err != nil {
		return nil, err
	}
	if err := a.client.revalidateDeviceReplacement(ctx, ref, want, snapshot); err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return write, nil
}
