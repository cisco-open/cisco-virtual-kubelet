// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Observation permission only. The common reconciler still checks the current
// config/source/controller/owner bindings before observing a retained task.
// No taskless outcome, bulk site operation or physical action is admitted.
// Planned-AP drawing tasks must match an explicitly declared placement.
func siteKnownTaskRecovery(desired []desiredSite, placements ...plannedAPIntent) func(*cvk.NetworkControllerConfigurationOperation) bool {
	return func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op == nil {
			return false
		}
		_, declared := siteDesiredOperation(desired, op.Resource)
		selected := siteSingularOperation(op) && declared
		if siteDesignOperation(op) && plannedAPResourceKey(op.Resource) != "" {
			for _, want := range placements {
				if plannedAPResourceMatches(op.Resource, want) {
					selected = true
				}
			}
		}
		return selected && op.Phase == "Unknown" &&
			configurationOperationID.MatchString(op.ID) && op.ConfigName != "" && op.ConfigUID != "" &&
			op.ConfigGeneration > 0 && op.ControllerGeneration > 0 && validAPIID(op.TaskID) &&
			op.SubmittedAt != nil && !op.SubmittedAt.IsZero()
	}
}

func (c *client) siteTaskCompletion(desired []desiredSite, placements ...plannedAPIntent) func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
	selected := siteKnownTaskRecovery(desired, placements...)
	return func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if c == nil || op == nil || op.SubmittedAt == nil {
			return false, errConfigurationTransition
		}
		if validSitePath(op.Resource) && op.Resource != "Global" || strings.HasPrefix(op.Resource, sitePrivateCreatePrefix) || siteRecreateSelected(op.Resource) || siteAppliedSelected(op) || plannedAPResourceKey(op.Resource) != "" {
			observation := op.DeepCopy()
			if observation.Phase == "Accepted" {
				observation.Phase = "Unknown"
			}
			if !selected(observation) {
				return false, errConfigurationTransition
			}
			// Never fall back to a successful root when a child is pending,
			// failed, uncorrelated or unavailable. This proves task execution,
			// not creator ownership or dependency-safe deletion authority.
			return c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if strings.HasPrefix(op.Resource, siteBulkResourcePrefix) {
			return c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		// Legacy task-based floor images retain their existing contract.
		return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
