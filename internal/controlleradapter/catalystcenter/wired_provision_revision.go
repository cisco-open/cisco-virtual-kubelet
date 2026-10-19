// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"time"
)

func validWiredProvisionResource(resource string) bool {
	digest, ok := strings.CutPrefix(resource, "wired-provision:")
	if !ok || len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// Match the shared physical action receipt; do not introduce another receipt
// collection or infer completion from an existing provisioned-device record.
func wiredProvisionAlreadyApplied(state *cvk.NetworkControllerConfigurationStatus, desired *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if desired == nil || !validWiredProvisionResource(desired.Resource) || desired.ConfigUID == "" || desired.ConfigName == "" || desired.ConfigGeneration < 1 || desired.ControllerGeneration < 1 || desired.Section != "inventory" || !validAPIID(desired.TargetID) || !validAPIID(desired.ParentID) || desired.TargetSerial == "" || len(desired.CredentialVersions) != 0 || (desired.Action != "Create" && desired.Action != "Update") {
		return false, errConfigurationTransition
	}
	if state == nil {
		return false, nil
	}
	var retained *cvk.NetworkControllerConfigurationActionApplication
	for i := range state.ActionApplications {
		receipt := &state.ActionApplications[i]
		if receipt.Section != desired.Section || receipt.Resource != desired.Resource {
			continue
		}
		if retained != nil {
			return false, errConfigurationOwned
		}
		retained = receipt
	}
	if retained == nil {
		return false, nil
	}
	op := &retained.Operation
	if op.ConfigUID != desired.ConfigUID {
		return false, errConfigurationOwned
	}
	owned := false
	for _, owner := range state.Owners {
		if owner.Section == desired.Section {
			if owner.ConfigUID != desired.ConfigUID || owner.ConfigName != desired.ConfigName {
				return false, errConfigurationOwned
			}
			if owned {
				return false, errConfigurationOwned
			}
			owned = true
		}
	}
	if !owned {
		return false, errConfigurationOwned
	}
	if op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || op.Section != retained.Section || op.Resource != retained.Resource || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || retained.VerifiedAt == nil || retained.VerifiedAt.IsZero() || retained.VerifiedAt.Before(op.SubmittedAt) || retained.VerifiedAt.After(now) || len(op.CredentialVersions) != 0 || op.TargetSerial == "" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || (op.Action != "Create" && op.Action != "Update") {
		return false, errConfigurationStale
	}
	return op.ConfigName == desired.ConfigName && op.ConfigGeneration == desired.ConfigGeneration && op.ControllerGeneration == desired.ControllerGeneration && op.SourceUID == desired.SourceUID && op.SourceVersion == desired.SourceVersion && op.Action == desired.Action && op.TargetID == desired.TargetID && op.ParentID == desired.ParentID && op.TargetSerial == desired.TargetSerial, nil
}

// Native planning is read-only. A receipt suppresses the action only after
// current dependencies have been resolved and the journal/source re-read.
func (a *adapter) planWiredProvisionRevision(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want inventoryDeviceIntent, state string) (*configurationWrite, error) {
	if config == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	controllerUID, controllerGeneration := nc.UID, nc.Generation
	write, err := a.client.planWiredDeviceProvision(ctx, want, state)
	if err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.UID != controllerUID || nc.Generation != controllerGeneration {
		return nil, errConfigurationStale
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	if write == nil {
		return nil, nil
	}
	desired := &cvk.NetworkControllerConfigurationOperation{ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent, TargetSerial: write.identity.serial}
	if source != nil {
		desired.SourceUID = string(source.uid)
		desired.SourceVersion = source.version
	}
	applied, err := wiredProvisionAlreadyApplied(nc.Status.Configuration, desired, time.Now())
	if err != nil {
		return nil, err
	}
	if applied {
		return nil, nil
	}
	return write, nil
}
