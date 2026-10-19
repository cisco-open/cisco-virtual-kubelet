// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Bridge only PnP database registration to the synchronous-created receipt.
// Claim/preview actions and physical admission are not enabled by this bridge.
func (w *pnpRegistrationWrite) configurationWrite() (*configurationWrite, error) {
	if w == nil || w.submit == nil || w.resource == "" {
		return nil, errConfigurationTransition
	}
	return &configurationWrite{resource: w.resource, action: "Create", noDispatch: w.noDispatch, submitCreated: func(ctx context.Context) (string, error) {
		receipt, err := w.submit(ctx)
		if err != nil {
			return "", err
		}
		if receipt == nil || receipt.resource != w.resource || !validAPIID(receipt.deviceID) {
			return "", errInvalidResponse
		}
		return receipt.deviceID, nil
	}}, nil
}

func (c *client) observePnPRegistrationOperation(ctx context.Context, want pnpDeviceIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || (op.Phase != "Verifying" && op.Phase != "Verified") || op.Section != "inventory" || op.Action != "Create" || op.TaskID != "" || op.TargetID != "" || op.TargetSerial != "" || op.ParentID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || !validAPIID(op.CreatedResourceID) {
		return false, errConfigurationTransition
	}
	body, err := pnpRegistrationPayload(want)
	if err != nil {
		return false, err
	}
	resource := fmt.Sprintf("pnp-register:%x", sha256.Sum256(body))
	if resource != op.Resource {
		return false, errConfigurationStale
	}
	serials, err := validatePnPDeviceIntent(want)
	if err != nil {
		return false, err
	}
	return c.observePnPRegistration(ctx, &pnpRegistrationReceipt{resource: resource, deviceID: op.CreatedResourceID, serial: serials[0], pid: want.PID, hostname: want.Name, stack: len(serials) > 1})
}
