// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"time"
)

// Persist receipt and Verified in one status CAS. This is recovery bookkeeping,
// not device admission; no controller API mutation is performed here.
func (a *adapter) recordWirelessProvisionApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, operationID, name string) error {
	if !configurationOperationID.MatchString(operationID) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation
	uid, version := "", ""
	if source != nil {
		uid, version = string(source.uid), source.version
	}
	if op.ID != operationID || op.Phase != "Verifying" || op.Section != "inventory" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != uid || op.SourceVersion != version {
		return errConfigurationStale
	}
	key, err := wirelessTagDigest(name)
	if err != nil {
		return err
	}
	resource := "wireless-provision:" + key
	var raw []byte
	if source != nil {
		raw = source.raw
	}
	if err := a.client.verifyWirelessProvisionDependencies(ctx, raw, op); err != nil {
		return err
	}
	verified, err := a.client.verifyRecordedWirelessProvision(ctx, name, op, time.Now())
	if err != nil {
		return err
	}
	if !verified {
		return errConfigurationBusy
	}
	op.Phase = "Verified"
	if err := a.retainConfigurationActionReceipt(ctx, nc, resource); err != nil {
		return err
	}
	return a.persistConfigurationActionReceipt(ctx, nc)
}

func configurationActionReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || op == nil || op.Phase != "Verified" {
		return false
	}
	record, err := decodeWirelessProvisionRecord(op.Resource)
	if err != nil || record.DeviceID != op.TargetID || record.Serial != op.TargetSerial || record.Action != op.Action || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
		return false
	}
	key := "wireless-provision:" + record.NameKey
	found := false
	for _, receipt := range state.ActionApplications {
		if receipt.Section == op.Section && receipt.Operation.ID == op.ID {
			if found || receipt.Resource != key || !reflect.DeepEqual(&receipt.Operation, op) {
				return false
			}
			found = true
		}
	}
	return found
}

// A revision-bound receipt suppresses repeating an imperative action. This
// predicate does not assert current device compliance or grant write authority.
func wirelessProvisionAlreadyApplied(state *cvk.NetworkControllerConfigurationStatus, desired *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if state == nil || desired == nil {
		return false, nil
	}
	if desired.ConfigUID == "" || desired.ConfigName == "" || desired.ConfigGeneration < 1 || desired.ControllerGeneration < 1 || desired.Section != "inventory" {
		return false, errConfigurationTransition
	}
	intent, err := decodeWirelessProvisionRecord(desired.Resource)
	if err != nil {
		return false, err
	}
	if desired.TargetID != intent.DeviceID || desired.TargetSerial != intent.Serial || desired.Action != intent.Action {
		return false, errConfigurationTransition
	}
	key := "wireless-provision:" + intent.NameKey
	found := false
	matched := false
	for _, receipt := range state.ActionApplications {
		if receipt.Section != desired.Section || receipt.Resource != key {
			continue
		}
		if found {
			return false, errConfigurationOwned
		}
		found = true
		op := &receipt.Operation
		if op.ConfigUID != desired.ConfigUID {
			return false, errConfigurationOwned
		}
		if op.Phase != "Verified" || op.SubmittedAt == nil || !validAPIID(op.TaskID) || !configurationOperationID.MatchString(op.ID) {
			return false, errConfigurationStale
		}
		previous, err := decodeWirelessProvisionRecord(op.Resource)
		if err != nil {
			return false, err
		}
		if op.TargetID != previous.DeviceID || op.TargetSerial != previous.Serial || op.Action != previous.Action || op.Section != receipt.Section || op.ParentID != "" || len(op.CredentialVersions) > 0 {
			return false, errConfigurationStale
		}
		previous.Timestamp, intent.Timestamp = "", ""
		matched = previous == intent && op.ConfigName == desired.ConfigName && op.ConfigGeneration == desired.ConfigGeneration && op.ControllerGeneration == desired.ControllerGeneration && op.SourceUID == desired.SourceUID && op.SourceVersion == desired.SourceVersion && desired.ParentID == "" && len(desired.CredentialVersions) == 0
	}
	return matched, nil
}
