// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// Reference evidence is deliberately separate from workflow success. Matching
// IDs do not attest template version, variables, task outcome or correlation to
// this submission. System/reset workflows cannot stand in for the main workflow.
type pnpClaimWorkflowObservation struct {
	state      string
	workflowID string
}

func observePnPClaimWorkflow(want pnpDeviceIntent, dependencies *pnpClaimDependencies, inventory *pnpClaimInventorySnapshot) (pnpClaimWorkflowObservation, error) {
	if _, err := pnpClaimResource(want, dependencies); err != nil {
		return pnpClaimWorkflowObservation{}, err
	}
	if inventory == nil {
		return pnpClaimWorkflowObservation{}, errConfigurationTransition
	}
	if inventory.pnp.ID != dependencies.refs.deviceID || inventory.site.ID != dependencies.refs.siteID {
		return pnpClaimWorkflowObservation{}, errConfigurationStale
	}
	imageRequired := want.ImageSkip != nil && !*want.ImageSkip
	templateRequired := want.Template != nil
	result := pnpClaimWorkflowObservation{state: "NotRequested"}
	if !imageRequired && !templateRequired {
		return result, nil
	}
	result.state = "PendingWorkflow"
	workflow := inventory.pnp.Workflow
	if workflow == nil {
		return result, nil
	}
	if err := validatePnPWorkflowMetadata(workflow); err != nil {
		return pnpClaimWorkflowObservation{}, err
	}
	// Check all present requested references before reporting a missing one.
	// A stable foreign template must not be hidden by a pending image field.
	if imageRequired && workflow.ImageID != "" && workflow.ImageID != dependencies.refs.imageID {
		return pnpClaimWorkflowObservation{}, errConfigurationStale
	}
	configIDs := pnpWorkflowConfigIDs(workflow)
	if templateRequired {
		for _, id := range configIDs {
			if id != dependencies.refs.templateID {
				return pnpClaimWorkflowObservation{}, errConfigurationStale
			}
		}
	}
	if workflow.ID == "" {
		return result, nil
	}
	result.workflowID = workflow.ID
	switch {
	case imageRequired && workflow.ImageID == "":
		result.state = "PendingImageReference"
	case templateRequired && len(configIDs) == 0:
		result.state = "PendingTemplateReference"
	default:
		result.state = "MatchedReferences"
	}
	return result, nil
}
