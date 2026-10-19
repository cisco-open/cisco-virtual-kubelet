// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Preserve the record UUID as evidence only after the shared owner/source/exact
// journal bracket accepts the native observation. This grants no admission.
func (a *adapter) observeOwnedDeviceReplacementMark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*deviceReplacementRecord, error) {
	var evidence *deviceReplacementRecord
	done, err := a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
		var err error
		evidence, err = a.client.observeDeviceReplacementMark(ctx, ref, want, op, now)
		return evidence != nil, err
	})
	if err != nil || !done {
		return nil, err
	}
	return evidence, nil
}

// Retain the native record UUID separately from the immutable admitted operation
// identity, using the existing action receipt key and atomic completion writer.
func (a *adapter) recordOwnedDeviceReplacementMark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent, operationID string, now time.Time) error {
	return a.recordOwnedInventoryAction(ctx, config, source, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
		evidence, err := a.observeOwnedDeviceReplacementMark(ctx, config, source, ref, want, op, now)
		if err != nil || evidence == nil {
			return "", err
		}
		if !validAPIID(evidence.ID) {
			return "", errInvalidResponse
		}
		return "rma-mark-record:" + evidence.ID, nil
	})
}
