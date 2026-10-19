// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func isPnPSiteClaimOperation(section, resource string) bool {
	return section == "inventory" && strings.HasPrefix(resource, "pnp-claim:")
}

// Private single-target bridge. The supplied admission step must produce the
// shared physical dispatch permit; public runtime wiring is deliberately absent
// until PnP identity admission and independent completion recovery are enabled.
// A native acknowledgement advances only Verifying, never Verified. The shared
// write-ahead journal fences restart/lost responses and cannot replay a claim.
func (a *adapter) submitOwnedPnPSiteClaim(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent, index *nacmodel.TemplateIndex, admit configurationAdmissionStep) error {
	if a == nil || admit == nil {
		return errConfigurationDeviceAdmission
	}
	op, err := a.prepareOwnedPnPSiteClaim(ctx, config, source, want, index)
	if err != nil {
		return err
	}
	return a.resumeOwnedPnPSiteClaim(ctx, config, source, want, index, op, admit)
}

// Persist the original native target and dependency binding without admission
// or a POST. Preparation carries no physical onboarding authority.
func (a *adapter) prepareOwnedPnPSiteClaim(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent, index *nacmodel.TemplateIndex) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil {
		return nil, errConfigurationTransition
	}
	target, err := a.observeOwnedPnPClaimTarget(ctx, config, source, want)
	if err != nil {
		return nil, err
	}
	if len(target.serials) != 1 {
		return nil, errConfigurationDeviceAdmission
	}
	plan, err := a.client.planPnPSiteClaim(ctx, want, target.device.ID, index)
	if err != nil {
		return nil, err
	}
	identity := configurationResourceIdentity{target: plan.deviceID, serial: target.serials[0], parent: plan.siteID}
	return a.prepareConfiguration(ctx, config, source, "inventory", plan.resource, "Update", identity)
}

// Reconstruct only an exact unsubmitted Prepared claim. A new physical permit
// remains mandatory; Submitting/Unknown/Verifying cannot replay. Replanning must
// reproduce the persisted v2 target baseline, not accept a fresh restart view.
func (a *adapter) resumeOwnedPnPSiteClaim(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent, index *nacmodel.TemplateIndex, op *cvk.NetworkControllerConfigurationOperation, admit configurationAdmissionStep) error {
	if a == nil || op == nil || admit == nil {
		return errConfigurationDeviceAdmission
	}
	if op.Phase != "Prepared" || op.Action != "Update" || !isPnPSiteClaimOperation(op.Section, op.Resource) || !strings.HasPrefix(op.Resource, "pnp-claim:v2:") || op.SubmittedAt != nil || op.TaskID != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 {
		return errConfigurationTransition
	}
	if err := declaredPnPRegistration(config, source, want); err != nil {
		return err
	}
	serials, err := validatePnPDeviceIntent(want)
	if err != nil || len(serials) != 1 || serials[0] != op.TargetSerial {
		return errConfigurationStale
	}
	var plan *pnpClaimWrite
	_, err = a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
		nc, err := a.currentController(ctx)
		if err != nil {
			return false, err
		}
		receipt, err := ownedPnPRegistrationReceipt(nc, config, want)
		if err != nil {
			return false, err
		}
		if receipt == nil || receipt.CreatedResourceID != op.TargetID {
			return false, errConfigurationOwned
		}
		plan, err = a.client.planPnPSiteClaim(ctx, want, op.TargetID, index)
		if err != nil {
			return false, err
		}
		if plan.resource != op.Resource || plan.siteID != op.ParentID {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	identity := configurationResourceIdentity{target: plan.deviceID, serial: serials[0], parent: plan.siteID}
	preflight := func(ctx context.Context) error {
		nc, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.ID != op.ID || nc.Status.Configuration.Operation.Phase != "Submitting" {
			return errConfigurationStale
		}
		current := nc.Status.Configuration.Operation
		if current.SubmittedAt == nil || current.SubmittedAt.IsZero() || current.SubmittedAt.After(time.Now()) {
			return errConfigurationStale
		}
		expected := op.DeepCopy()
		expected.Phase, expected.SubmittedAt = "Submitting", current.SubmittedAt
		if !reflect.DeepEqual(current, expected) {
			return errConfigurationStale
		}
		_, err = a.observeOwnedInventoryDeployment(ctx, config, source, nc.Status.Configuration.Operation, func(ctx context.Context) (bool, error) {
			if err := plan.preflight(ctx); err != nil {
				return false, err
			}
			return true, nil
		})
		return err
	}
	return a.submitPreparedConfigurationReceiptWithPreflight(ctx, config, source, op, "inventory", plan.resource, "Update", preflight, func(ctx context.Context) (configurationReceipt, error) {
		response, err := plan.submitValidated(ctx)
		if err != nil {
			return configurationReceipt{}, err
		}
		if response == nil || response.deviceID != identity.target || response.siteID != identity.parent {
			return configurationReceipt{}, errConfigurationStale
		}
		// Historical field name: completed means the HTTP call completed;
		// authoritative native completion is a separate Verifying operation.
		return configurationReceipt{completed: true}, nil
	}, admit, identity)
}
