// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"time"
)

// wirelessProvisionExecutor bridges imperative revision receipts and the shared
// configuration executor. The enclosing inventory planner must first establish
// site/profile/AP dependencies and pacing. Submission still requires the common
// physical reservation and one-shot dispatch path; next grants no authority.
type wirelessProvisionExecutor struct {
	adapter              *adapter
	config               *configapi.NetworkControllerConfig
	source               *configSourceSnapshot
	reference            configurationDeviceReference
	state, siteID        string
	modelConfigRequired  bool
	dependencyResources  []string
	dependencyDigest     string
	validateDependencies func(context.Context) error
	apDependencies       []wirelessProvisionAPDependency
}

func (e *wirelessProvisionExecutor) count() int { return 1 }
func (e *wirelessProvisionExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e.adapter == nil || e.config == nil {
		return nil, errConfigurationTransition
	}
	a := e.adapter
	if err := a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	// In a multi-WLC stage, leave another device's Prepared operation to its
	// matching executor. Do not let this device's pending dependencies block it.
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		pending := nc.Status.Configuration.Operation
		if pending.Phase == "Prepared" && strings.HasPrefix(pending.Resource, wirelessProvisionRecordPrefix) {
			record, err := decodeWirelessProvisionRecord(pending.Resource)
			if err != nil {
				return nil, err
			}
			nameKey, err := wirelessTagDigest(e.reference.Name)
			if err != nil {
				return nil, err
			}
			if record.NameKey != nameKey {
				return nil, nil
			}
		}
	}
	resources, err := e.requiredAPReceipts(ctx)
	if err != nil {
		return nil, err
	}
	if err := e.dependenciesSettled(ctx, nc, resources, true, time.Now()); err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		write, err := a.planBoundWirelessProvisionRevision(ctx, e.config, e.source, e.reference, e.state, e.siteID, e.modelConfigRequired, e.dependencyDigest)
		if err != nil {
			return nil, err
		}
		return e.guardAPDependencies(write, resources), nil
	}
	op := nc.Status.Configuration.Operation
	sourceUID, sourceVersion := "", ""
	if e.source != nil {
		sourceUID, sourceVersion = string(e.source.uid), e.source.version
	}
	if op.Phase != "Prepared" || op.Section != "inventory" || op.ConfigName != e.config.Name || op.ConfigUID != string(e.config.UID) || op.ConfigGeneration != e.config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != sourceUID || op.SourceVersion != sourceVersion || op.ParentID != "" || len(op.CredentialVersions) != 0 {
		return nil, errConfigurationBusy
	}
	// Replan against current inventory, but never replace the durable baseline.
	// A changed timestamp, identity or model requirement invalidates preparation.
	write, err := a.client.planWirelessDeviceProvision(ctx, e.reference, e.state, e.siteID, e.modelConfigRequired)
	if err != nil {
		return nil, err
	}
	write, err = bindWirelessProvisionDependencies(write, e.dependencyDigest)
	if err != nil {
		return nil, err
	}
	if write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.serial != op.TargetSerial {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return e.guardAPDependencies(write, resources), nil
}

func (e *wirelessProvisionExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e.adapter == nil || e.config == nil || op == nil || op.Phase != "Verifying" || op.Section != "inventory" || op.ConfigName != e.config.Name || op.ConfigUID != string(e.config.UID) || op.ConfigGeneration != e.config.Generation {
		return false, errConfigurationStale
	}
	if err := e.adapter.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return false, err
	}
	nc, err := e.adapter.currentController(ctx)
	if err != nil {
		return false, err
	}
	sourceUID, sourceVersion := "", ""
	if e.source != nil {
		sourceUID, sourceVersion = string(e.source.uid), e.source.version
	}
	if op.ControllerGeneration != nc.Generation || op.SourceUID != sourceUID || op.SourceVersion != sourceVersion {
		return false, errConfigurationStale
	}
	var raw []byte
	if e.source != nil {
		raw = e.source.raw
	}
	if err := e.adapter.client.verifyWirelessProvisionDependencies(ctx, raw, op); err != nil {
		return false, err
	}
	return e.adapter.client.verifyRecordedWirelessProvision(ctx, e.reference.Name, op, time.Now())
}

var _ configurationExecutor = (*wirelessProvisionExecutor)(nil)
