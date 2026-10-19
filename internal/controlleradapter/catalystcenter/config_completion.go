// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

// The shared journal owns timeout, phase transitions and fencing. A compiled
// plan may supply receipt-specific observation (for example template deployment
// status). An error or pending result must never fall back to generic tasks.
func (p configurationApplyPlan) taskComplete(ctx context.Context, c *client, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if op == nil || (op.Phase != "Accepted" && !(p.canObserveUnknownTask(op))) || op.Section != p.section || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.Before(op.SubmittedAt.Time) || !validAPIID(op.TaskID) {
		return false, errConfigurationTransition
	}
	if p.observeCompletion != nil {
		return p.observeCompletion(ctx, op.DeepCopy(), now)
	}
	if c == nil {
		return false, errConfigurationTransition
	}
	return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
}

// Optional result-specific completion never falls back to generic verification.
// Require the callback to retain the unchanged operation and one receipt in the
// same Verified state; passing a copy prevents mutation of the caller's journal.
func (p configurationApplyPlan) completeWithReceipt(ctx context.Context, a *adapter, op *cvk.NetworkControllerConfigurationOperation) error {
	if a == nil || p.recordCompletion == nil || op == nil || op.Phase != "Verifying" || op.Section != p.section || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return errConfigurationTransition
	}
	if p.receiptOperation != nil && !p.receiptOperation(op.DeepCopy()) {
		return errConfigurationTransition
	}
	before, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if before.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(before.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	expected := op.DeepCopy()
	expected.Phase = "Verified"
	if err := p.recordCompletion(ctx, op.DeepCopy()); err != nil {
		return err
	}
	after, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if after.UID != before.UID || after.Generation != before.Generation || after.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(after.Status.Configuration.Operation, expected) {
		return errConfigurationStale
	}
	count := 0
	for _, receipt := range after.Status.Configuration.ActionApplications {
		if receipt.Operation.ID != op.ID {
			continue
		}
		if receipt.Section != op.Section || receipt.Resource == "" || !apiequality.Semantic.DeepEqual(&receipt.Operation, expected) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(time.Now()) {
			return errConfigurationStale
		}
		count++
	}
	if count != 1 {
		return errConfigurationStale
	}
	return nil
}

func (p configurationApplyPlan) canObserveUnknownTask(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Phase != "Unknown" {
		return false
	}
	if p.recoverKnownTask != nil {
		return p.recoverKnownTask(op.DeepCopy())
	}
	// Preserve the existing SSID-only fallback and unrelated observer behavior.
	return p.observeCompletion == nil && ssidKnownTaskRecoverySupported(op)
}
