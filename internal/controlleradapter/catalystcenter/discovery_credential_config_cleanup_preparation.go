// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Bind a fresh complete native observation to the existing deletion/spec/evidence
// envelope and shared Prepared CAS. This is not public enrollment or a dispatch
// grant: no dispatchID is set, and ordinary cleanup cannot submit a typed record.
func (a *adapter) prepareDiscoveryCredentialConfigCleanup(ctx context.Context, c *configapi.NetworkControllerConfig) (*cvk.NetworkControllerConfigurationOperation, error) {
	observation, err := a.observeDiscoveryCredentialConfigCleanup(ctx, c)
	if err != nil {
		return nil, err
	}
	if len(observation.Candidates) == 0 {
		return nil, errConfigurationTransition
	}
	nc, source, err := a.discoveryCredentialCleanupObservationInput(ctx, c)
	if err != nil {
		return nil, err
	}
	return a.prepareObservedDiscoveryConfigCleanup(ctx, c, observation, nc, source, true)
}
