// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Return native mark evidence for subsequent owned receipt persistence. This
// does not mark the journal Verified or authorize unmark/replacement. A record
// with no submitted task is never sufficient for recovery of an unknown POST.
func (c *client) observeDeviceReplacementMark(ctx context.Context, ref configurationDeviceReference, want deviceReplacementIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*deviceReplacementRecord, error) {
	if op == nil || (op.Phase != "Accepted" && op.Phase != "Verifying") || op.Section != "inventory" || op.Action != "Create" || ref.Serial == "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.Before(op.SubmittedAt.Time) || !validAPIID(op.TaskID) || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 {
		return nil, errConfigurationTransition
	}
	snapshot, err := c.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return nil, err
	}
	resource, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		return nil, err
	}
	if resource != op.Resource || snapshot.target.ID != op.TargetID || snapshot.serial != op.TargetSerial || snapshot.siteID != op.ParentID {
		return nil, errConfigurationStale
	}
	done, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return nil, err
	}
	if snapshot.record == nil {
		return nil, nil
	}
	record := *snapshot.record
	// Mark completion requires fresh serial-bearing readback. Native creation
	// times are interpreted as epoch milliseconds, as with existing task evidence;
	// this mapping still requires live appliance qualification for RMA.
	serial, err := topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
	if err != nil || serial != snapshot.serial || record.CreatedAt <= 0 || record.CreatedAt < op.SubmittedAt.Add(-configurationTaskClockSkew).UnixMilli() || record.CreatedAt > now.Add(configurationTaskClockSkew).UnixMilli() {
		return nil, errConfigurationStale
	}
	if record.Status != "MARKED-FOR-REPLACEMENT" && record.Status != "READY-FOR-REPLACEMENT" {
		return nil, errConfigurationTransition
	}
	// A workflow or completed replacement is beyond the effect of this mark.
	// Preserve its fence for explicit recovery rather than claiming it as ours.
	if record.WorkflowID != "" || record.WorkflowFailedStep != "" || record.ReplacedAt != 0 || record.ReplacementSerial != "" {
		return nil, errConfigurationOwned
	}
	if err := c.revalidateDeviceReplacement(ctx, ref, want, snapshot); err != nil {
		return nil, err
	}
	return &record, nil
}
