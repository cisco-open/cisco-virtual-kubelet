// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const templateProvisionSettleDelay = 10 * time.Second

// Inventory-only execution corresponds to the pinned module's scoped-device
// mode. All declared provisioned devices settle before either template kind.
// Existing devices use the exact Prepared observation floor rather than a
// fabricated provisioning receipt or a process-local timer.
func templateProvisionDependenciesSettled(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, devices map[string]wiredInventoryBinding, write *configurationWrite, now time.Time) error {
	if nc == nil || config == nil || write == nil || !isTemplateDeploymentResource(write.resource) || write.identity.serial == "" || now.IsZero() || len(devices) == 0 {
		return errConfigurationTransition
	}
	state := nc.Status.Configuration
	if state == nil {
		return nil // planning grants no dispatch authority
	}
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section == "inventory" {
			if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
				return errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return errConfigurationOwned
	}
	uid, version := "", ""
	if source != nil {
		uid, version = string(source.uid), source.version
	}
	revision := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op.ConfigUID == string(config.UID) && op.ConfigName == config.Name && op.ConfigGeneration == config.Generation && op.ControllerGeneration == nc.Generation && op.SourceUID == uid && op.SourceVersion == version && op.Section == "inventory"
	}
	missing, pending := false, false
	for _, device := range devices {
		if !validAPIID(device.deviceID) || !validAPIID(device.siteID) || device.serial == "" {
			return errConfigurationStale
		}
		var found *cvk.NetworkControllerConfigurationActionApplication
		for i := range state.ActionApplications {
			r := &state.ActionApplications[i]
			op := &r.Operation
			if r.Section != "inventory" || !validWiredProvisionResource(r.Resource) || op.TargetID != device.deviceID {
				continue
			}
			if op.ConfigUID != string(config.UID) || op.ConfigName != config.Name {
				return errConfigurationOwned
			}
			if !revision(op) {
				continue
			}
			if found != nil {
				return errConfigurationOwned
			}
			found = r
		}
		if found == nil {
			missing = true
			continue
		}
		op := &found.Operation
		if op.Resource != found.Resource || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || op.TargetSerial != device.serial || op.ParentID != device.siteID || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.DependencyVersions != nil || (op.Action != "Create" && op.Action != "Update") || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) {
			return errConfigurationStale
		}
		if found.VerifiedAt == nil {
			missing = true
			continue
		}
		if found.VerifiedAt.IsZero() || found.VerifiedAt.After(now) || found.VerifiedAt.Before(op.SubmittedAt) {
			return errConfigurationStale
		}
		if now.Before(found.VerifiedAt.Add(templateProvisionSettleDelay + time.Second)) {
			pending = true
		}
	}
	op := state.Operation
	if op == nil {
		return nil // read-only planning can prepare the durable observation floor
	}
	if !revision(op) || !configurationOperationID.MatchString(op.ID) || (op.Phase != "Prepared" && op.Phase != "Submitting") || op.Resource != write.resource || op.Action != write.action || op.TargetID != write.identity.target || op.TargetSerial != write.identity.serial || op.ParentID != write.identity.parent || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || op.TaskID != "" {
		return errConfigurationStale
	}
	if missing {
		if op.PreparedAt == nil || op.PreparedAt.IsZero() || op.PreparedAt.After(now) || (op.SubmittedAt != nil && op.PreparedAt.After(op.SubmittedAt.Time)) {
			return errConfigurationBusy
		}
		if now.Before(op.PreparedAt.Add(templateProvisionSettleDelay + time.Second)) {
			pending = true
		}
	}
	if pending {
		return errConfigurationBusy
	}
	return nil
}

func (a *adapter) templateProvisionSettling(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, devices map[string]wiredInventoryBinding, write *configurationWrite) error {
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err := templateProvisionDependenciesSettled(nc, config, source, devices, write, time.Now()); err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return errConfigurationStale
	}
	return nil
}
