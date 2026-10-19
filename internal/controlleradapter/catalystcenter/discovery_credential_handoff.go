// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// The public bridge reuses the qualified observer and atomic custody publisher.
// It performs metadata transfer only. Normal planning, one-use dispatch and
// result verification occur on later reconciles with the new owner visible.
func (a *adapter) handoffDiscoveryCredentialOwnership(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, error) {
	if !discoveryDeletionEligible(nc, c) || !hasDiscoveryConfigFinalizer(c) || !discoveryCredentialApplyShape(c) || source == nil || nc.Status.Configuration == nil {
		return false, nil
	}
	foreign := false
	for _, owner := range nc.Status.Configuration.Owners {
		if configurationSectionsOverlap(owner.Section, "inventory") && (owner.ConfigUID != string(c.UID) || owner.ConfigName != c.Name) {
			foreign = true
		}
	}
	if !foreign {
		return false, nil
	}
	if nc.Status.Configuration.Operation != nil {
		return true, errConfigurationBusy
	}
	if !discoveryCredentialAttestationMatchesSource(nc, c, source) {
		return true, errConfigurationOwned
	}
	observed, err := a.observeDiscoveryCredentialOwnershipHandoff(ctx, c, source)
	if err != nil {
		return true, err
	}
	return true, a.publishDiscoveryCredentialCustody(ctx, c, source, observed)
}
