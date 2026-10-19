// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func isPnPRegistrationOperation(section, resource string) bool {
	return section == "inventory" && strings.HasPrefix(resource, "pnp-register:")
}

func validPnPRegistrationReceiptOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || !isPnPRegistrationOperation(op.Section, op.Resource) || !configurationOperationID.MatchString(op.ID) || op.Phase != "Verified" || op.Action != "Create" || op.TargetID != "" || op.TargetSerial != "" || op.TaskID != "" || op.ParentID != "" || !validAPIID(op.CreatedResourceID) || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
		return false
	}
	digest := strings.TrimPrefix(op.Resource, "pnp-register:")
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == digest
}

func pnpRegistrationReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || !validPnPRegistrationReceiptOperation(op) {
		return false
	}
	found := false
	for _, receipt := range state.ActionApplications {
		base := pnpRecordPrefix + op.CreatedResourceID
		if receipt.Operation.ID != op.ID && receipt.Resource != base && !strings.HasPrefix(receipt.Resource, base+":") {
			continue
		}
		record, err := decodePnPRecord(receipt.Resource)
		if found || receipt.Section != "inventory" || err != nil || record.ID != op.CreatedResourceID || !reflect.DeepEqual(receipt.Operation, *op) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(time.Now()) {
			return false
		}
		found = true
	}
	return found
}

func (a *adapter) recordOwnedPnPRegistration(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent, operationID string) error {
	if err := declaredPnPRegistration(config, source, want); err != nil {
		return err
	}
	return a.recordOwnedInventoryAction(ctx, config, source, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
		done, err := a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
			return a.client.observePnPRegistrationOperation(ctx, want, op)
		})
		if err != nil || !done {
			return "", err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return "", err
		}
		if current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
			return "", errConfigurationStale
		}
		if err := pnpRegistrationPublicationCapacity(current.Status.Configuration, op); err != nil {
			return "", err
		}
		return "pnp-record:" + op.CreatedResourceID, nil
	})
}

func declaredPnPRegistration(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent) error {
	if config == nil {
		return errConfigurationTransition
	}
	var raw []byte
	if source != nil {
		raw = source.raw
	} else if config.Spec.Source.Inline != nil {
		raw = config.Spec.Source.Inline.Raw
	}
	wants, err := parsePnPInventory(raw)
	if err != nil {
		return err
	}
	found := false
	for _, declared := range wants {
		if reflect.DeepEqual(declared, want) {
			found = true
		}
	}
	if !found {
		return errConfigurationStale
	}
	return nil
}
