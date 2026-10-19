// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type pnpClaimRecoveryObservation struct {
	snapshot *pnpClaimInventorySnapshot
	image    pnpClaimImageObservation
	workflow pnpClaimWorkflowObservation
}

// Read recovery evidence for the exact owned operation. Unknown can be observed
// after a lost response, but this function never changes phase, releases a fence
// or substitutes inventory presence for native workflow completion.
func (a *adapter) observeOwnedPnPClaimInventory(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent, op *cvk.NetworkControllerConfigurationOperation, index *nacmodel.TemplateIndex) (*pnpClaimRecoveryObservation, error) {
	if a == nil || op == nil || !isPnPSiteClaimOperation(op.Section, op.Resource) || (op.Phase != "Verifying" && op.Phase != "Unknown") || op.Action != "Update" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TaskID != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
		return nil, errConfigurationTransition
	}
	if err := declaredPnPRegistration(config, source, want); err != nil {
		return nil, err
	}
	serials, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	if len(serials) != 1 || serials[0] != op.TargetSerial {
		return nil, errConfigurationStale
	}
	var evidence *pnpClaimInventorySnapshot
	var imageEvidence pnpClaimImageObservation
	var workflowEvidence pnpClaimWorkflowObservation
	observed, err := a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
		nc, err := a.currentController(ctx)
		if err != nil {
			return false, err
		}
		registration, err := ownedPnPRegistrationReceipt(nc, config, want)
		if err != nil {
			return false, err
		}
		if registration == nil || registration.CreatedResourceID != op.TargetID {
			return false, errConfigurationOwned
		}
		dependencies, err := a.client.pnpClaimDependencies(ctx, want, op.TargetID, index)
		if err != nil {
			return false, err
		}
		resource, err := pnpClaimResource(want, dependencies)
		if err != nil {
			return false, err
		}
		if !pnpClaimResourceMatchesDependencies(op.Resource, resource) || dependencies.refs.siteID != op.ParentID {
			return false, errConfigurationStale
		}
		evidence, err = a.client.pnpClaimInventorySnapshot(ctx, want, op.TargetID, op.ParentID)
		if err != nil {
			return false, err
		}
		if err := a.client.revalidatePnPClaimInventory(ctx, want, op.TargetID, op.ParentID, evidence); err != nil {
			return false, err
		}
		if err := a.client.revalidatePnPClaimDependencies(ctx, want, op.TargetID, index, dependencies); err != nil {
			return false, err
		}
		imageEvidence, err = observePnPClaimImage(want, dependencies, evidence)
		if err != nil {
			return false, err
		}
		workflowEvidence, err = observePnPClaimWorkflow(want, dependencies, evidence)
		if err != nil {
			return false, err
		}
		// True means this observation finished consistently, not that the
		// device or the claim finished. The caller receives pending evidence.
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if !observed {
		return nil, errConfigurationBusy
	}
	return &pnpClaimRecoveryObservation{snapshot: evidence, image: imageEvidence, workflow: workflowEvidence}, nil
}
