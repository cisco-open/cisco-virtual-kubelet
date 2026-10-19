// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// Opaque historical ADD commitments can be disambiguated only by a current
// declaration, its exact logical/native binding, latest independently verified
// receipt and positive current result. Device-pair equality alone is insufficient.
func (a *adapter) independentDeclaredLANAdd(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, deleted *lanLinkEndpointSnapshot, receipt *cvk.NetworkControllerConfigurationActionApplication, now time.Time) (bool, error) {
	effect, err := parseLANLinkEffect(receipt, now)
	if err != nil {
		return false, err
	}
	if effect.deleted {
		return false, errConfigurationOwned
	}
	latest, err := latestLANLinkEffect(nc, config, effect.logical, now)
	if err != nil {
		return false, err
	}
	if latest == nil || latest.deleted || latest.receipt.Operation.ID != receipt.Operation.ID {
		return false, nil
	}
	wants, err := declaredLANLinks(source)
	if err != nil {
		return false, err
	}
	for _, want := range wants {
		if want.action != "ADD_LINK" {
			continue
		}
		endpoints, err := a.client.lanLinkEndpointSnapshot(ctx, want)
		if err != nil {
			return false, err
		}
		logical, err := lanLinkAddLogicalDigest(want, endpoints)
		if err != nil {
			return false, err
		}
		if logical != effect.logical {
			continue
		}
		for _, left := range []lanLinkEndpoint{deleted.primary, deleted.secondary} {
			for _, right := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
				if left.device.ID == right.device.ID && left.port.ID == right.port.ID {
					return false, errConfigurationOwned
				}
			}
		}
		owned, found, err := a.observeOwnedLANLinkAdd(ctx, config, source, nc, want, now)
		if err != nil {
			return false, err
		}
		return found && owned.owned && owned.resource == receipt.Resource, nil
	}
	return false, nil
}
