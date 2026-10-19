// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// This is routing only. The strict PnP parser still consumes the entire source
// before any registration. Retained PnP operations cannot escape through an
// ordinary inventory executor when the source removes their declaration.
func pnpRegistrationStageSelected(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if config == nil || source == nil {
		return false
	}
	if nc != nil && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if isPnPRegistrationOperation(op.Section, op.Resource) || isPnPSiteClaimOperation(op.Section, op.Resource) || pnpRemovalSelected(op.Resource) {
			return true
		}
	}
	if retainedPnPRemovalSelected(nc, config) {
		return true
	}
	if len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" {
		return false
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return false
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return false
	}
	inventory, _ := root["inventory"].(map[string]any)
	devices, _ := inventory["devices"].([]any)
	for _, value := range devices {
		device, _ := value.(map[string]any)
		if device["state"] == "PNP" {
			return true
		}
	}
	return false
}

// Registration is metadata execution, not physical onboarding. This stage never
// returns InSync: the declared claim still needs physical admission and native
// workflow completion, even when every registration receipt is verified.
func (r *configReconciler) evaluatePnPRegistrationStage(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, status *configapi.NetworkControllerConfigStatus) (bool, string, string) {
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "inventory", State: configapi.NetworkControllerSectionStateApplying}}
	nc, readErr := r.a.currentController(ctx)
	if readErr != nil {
		return true, "PnPRemovalRecoveryRequired", "Controller state could not be read; retained work remains fenced"
	}
	if retainedPnPRemovalSelected(nc, config) {
		return r.evaluatePnPRemovalRecovery(ctx, nc, config, source, status)
	}
	wants, err := parsePnPInventory(source.raw)
	if err != nil {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus[0].State = configapi.NetworkControllerSectionStateApplyError
		return false, "UnsupportedIntent", "PnP registration requires the complete supported PnP-only inventory fragment; mixed states, unknown fields and other sections cannot be discarded"
	}
	status.SectionStatus[0].ResourceCount = int64(len(wants))
	if !supportedPnPRegistrationConfig(config) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus[0].State = configapi.NetworkControllerSectionStateApplyError
		return false, "UnsupportedPnPRegistrationScope", "PnP database registration requires retained inventory-only Apply without projected secrets"
	}
	done, err := r.a.reconcilePnPRegistrationStage(ctx, config, source)
	if err != nil {
		if errors.Is(err, errPnPRegistrationCapacity) {
			message := "PnP ownership receipt capacity is insufficient; existing ownership and acknowledged operations are retained, and further registration requires capacity for the complete remaining cohort"
			status.SectionStatus[0].Message = message
			return true, "PnPRegistrationCapacityUnavailable", message
		}
		if errors.Is(err, errConfigurationNotDispatched) {
			message := "PnP registration was refused before native dispatch; ownership is retained and a fresh source and cohort plan will be evaluated"
			status.SectionStatus[0].Message = message
			return true, "PnPRegistrationReplanRequired", message
		}
		message := "PnP registration remains pending or fenced until source, ownership, native identity and any retained operation can be verified; uncertain submissions are not replayed"
		status.SectionStatus[0].Message = message
		return true, "PnPRegistrationRecoveryRequired", message
	}
	if done {
		message := "PnP database registrations are owned and verified; site claiming remains unavailable until physical identity admission and native workflow completion are supported"
		status.SectionStatus[0].Message = message
		return true, "PnPClaimAdmissionUnavailable", message
	}
	message := "PnP database registration advanced through its durable stage; this does not attest claim or device provisioning completion"
	status.SectionStatus[0].Message = message
	return true, "PnPRegistrationProgressing", message
}
