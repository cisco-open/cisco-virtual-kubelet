// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// Image evidence is separate from claim/workflow completion. In particular,
// Skipped is not a successful activation and Matched is not proof that the
// onboarding template ran or that the image was activated by this operation.
type pnpClaimImageObservation struct {
	state                                    string
	imageID, expectedVersion, runningVersion string
}

func observePnPClaimImage(want pnpDeviceIntent, dependencies *pnpClaimDependencies, inventory *pnpClaimInventorySnapshot) (pnpClaimImageObservation, error) {
	if _, err := pnpClaimResource(want, dependencies); err != nil {
		return pnpClaimImageObservation{}, err
	}
	if inventory == nil {
		return pnpClaimImageObservation{}, errConfigurationTransition
	}
	if inventory.pnp.ID != dependencies.refs.deviceID || inventory.site.ID != dependencies.refs.siteID {
		return pnpClaimImageObservation{}, errConfigurationStale
	}
	result := pnpClaimImageObservation{state: "Skipped", imageID: dependencies.refs.imageID}
	if dependencies.image != nil {
		result.expectedVersion = dependencies.image.Version
	}
	if inventory.inventory != nil {
		result.runningVersion = inventory.inventory.SoftwareVersion
	}
	if want.ImageSkip == nil || *want.ImageSkip {
		return result, nil
	}
	if inventory.inventory == nil {
		result.state = "PendingInventory"
		return result, nil
	}
	if inventory.inventory.Reachability != "Reachable" {
		result.state = "PendingReachability"
		return result, nil
	}
	result.state = "PendingVersion"
	if swimVersionMatches(result.runningVersion, result.expectedVersion) {
		result.state = "Matched"
	}
	return result, nil
}
