// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// The stable resource names the floor, while the journal resource additionally
// names the exact bytes. Neither the logical filename nor binary data is stored
// in status. A receipt is evidence of upload completion, not image readback.
func (i floorImageIntent) operationResource() string {
	return strings.Replace(i.resource(), "floor-image:", "floor-image:v2:", 1) + ":" + i.digest + ":" + fmt.Sprintf("%x", sha256.Sum256([]byte(i.filename)))
}
func (i floorImageIntent) legacyOperationResource() string { return i.resource() + ":" + i.digest }
func (i floorImageIntent) synchronousOperationResource() string {
	return strings.Replace(i.operationResource(), "floor-image:", "floor-image-sync:", 1)
}

// Old receipts never recorded the filename. Preserve their historical no-op
// and original-task recovery semantics instead of inferring a path change and
// dispatching an unrequested migration upload. All new writes bind the path.
func (i floorImageIntent) matchesOperation(resource string) bool {
	return resource == i.operationResource() || resource == i.synchronousOperationResource() || resource == i.legacyOperationResource() || resource == strings.Replace(i.legacyOperationResource(), "floor-image:", "floor-image-sync:", 1)
}
func floorImageCompletionKindMatches(op *cvk.NetworkControllerConfigurationOperation) bool {
	if strings.HasPrefix(op.Resource, "floor-image-sync:") {
		return op.TaskID == ""
	}
	return validAPIID(op.TaskID)
}
func floorImageReceiptKey(resource string) (string, error) {
	parts := strings.Split(resource, ":")
	if len(parts) != 3 && len(parts) != 5 || parts[0] != "floor-image" && parts[0] != "floor-image-sync" {
		return "", errConfigurationTransition
	}
	start := 1
	if len(parts) == 5 {
		if parts[1] != "v2" {
			return "", errConfigurationTransition
		}
		start = 2
	}
	for _, part := range parts[start:] {
		decoded, err := hex.DecodeString(part)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != part {
			return "", errConfigurationTransition
		}
	}
	return "floor-image:" + parts[start], nil
}

// Reject foreign, ambiguous or malformed retained evidence before considering
// another upload. A replaced floor needs explicit recovery rather than silent
// adoption using the old floor's receipt. Changing bytes on the same floor is
// an update. New v2 receipts additionally bind the declared source path.
func floorImageApplied(want floorImageIntent, floorID, configName, configUID string, receipts []cvk.NetworkControllerConfigurationActionApplication, now time.Time) (bool, error) {
	if !validAPIID(floorID) || configName == "" || configUID == "" {
		return false, errConfigurationTransition
	}
	if key, err := floorImageReceiptKey(want.operationResource()); err != nil || key != want.resource() {
		return false, errConfigurationTransition
	}
	found, applied := false, false
	for _, receipt := range receipts {
		key, omitted, err := floorImageRowKey(receipt, now)
		if err != nil {
			return false, err
		}
		if key != want.resource() {
			continue
		}
		if found {
			return false, errConfigurationOwned
		}
		found = true
		if err := validateFloorImageReceipt(receipt, key, floorID, configName, configUID, now); err != nil {
			return false, err
		}
		applied = omitted == nil && want.matchesOperation(receipt.Operation.Resource)
	}
	return applied, nil
}

func validateFloorImageReceipt(receipt cvk.NetworkControllerConfigurationActionApplication, key, floorID, configName, configUID string, now time.Time) error {
	op := receipt.Operation
	if op.ConfigName != configName || op.ConfigUID != configUID {
		return errConfigurationOwned
	}
	actualKey, err := floorImageReceiptKey(op.Resource)
	if err != nil || actualKey != key || receipt.Section != "sites" || op.Section != "sites" || op.Phase != "Verified" || op.Action != "Update" || !configurationOperationID.MatchString(op.ID) || !floorImageCompletionKindMatches(&op) || op.TargetID != floorID || op.ParentID != "" || op.TargetSerial != "" || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 || len(receipt.EffectReceipts) != 0 || op.CreatedResourceID != "" || op.DependencyVersions != nil || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || op.PreparedAt != nil || (op.SourceUID == "") != (op.SourceVersion == "") || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(now) {
		return errConfigurationStale
	}
	return nil
}
