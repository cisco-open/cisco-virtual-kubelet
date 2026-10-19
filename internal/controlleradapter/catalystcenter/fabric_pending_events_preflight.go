// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Revalidate product evidence before requesting group admission. Success is not
// a permit and does not change Prepared; the group protocol must still establish
// scheduling guards, workload policy and all-member mutation authority.
func (a *adapter) revalidatePreparedPendingFabricEvent(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricSiteIntent, op *cvk.NetworkControllerConfigurationOperation) error {
	return a.revalidatePendingFabricEventOperation(ctx, config, source, want, op, "Prepared")
}

func (a *adapter) revalidatePendingFabricEventOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricSiteIntent, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Action != "Update" || op.TaskID != "" || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) || op.CancellationRequestedAt != nil || !isPendingFabricEventOperation(op.Section, op.Resource) || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	if err := declaredPendingFabricEvent(config, source, want); err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		snapshot, targets, err := a.client.pendingFabricEventTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return false, err
		}
		if snapshot.site.actual.ID != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		for _, event := range snapshot.events {
			if event.ID == op.TargetID {
				if op.Resource != pendingFabricEventResource(want.path, snapshot.site.siteID, event) {
					return false, errConfigurationStale
				}
				return true, nil
			}
		}
		return false, errConfigurationStale
	})
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return nil
}
