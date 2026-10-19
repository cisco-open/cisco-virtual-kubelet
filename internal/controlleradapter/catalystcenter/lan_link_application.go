// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"time"
)

// One source-bracketed atomic publication path for both LAN effects. Existing
// group receipt persistence retains completion and retirement authority.
func (a *adapter) recordLANLinkApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, proof configurationGroupConvergenceProof, now time.Time, build func(context.Context, *configSourceSnapshot, *cvk.NetworkController, *cvk.NetworkControllerConfigurationOperation, configurationGroupConvergenceProof) (*cvk.NetworkControllerConfigurationActionApplication, error)) error {
	if a == nil || a.client == nil || !supportedLANLinkAddConfig(config) || !configurationOperationID.MatchString(operationID) || authorize == nil || proof == nil || build == nil || !a.configurationGroupAdmissionReady() {
		return errConfigurationDeviceAdmission
	}
	config = config.DeepCopy()
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation
	if op.ID != operationID || op.Phase != "Verifying" {
		return errConfigurationStale
	}
	guarded := func(ctx context.Context, current *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		if err := proof(ctx, current, targets, completed); err != nil {
			return err
		}
		return a.checkConfigurationSnapshot(ctx, config, source)
	}
	var receipt *cvk.NetworkControllerConfigurationActionApplication
	done, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "lan_automation", func(ctx context.Context) (bool, error) {
		var err error
		receipt, err = build(ctx, source, nc, op, guarded)
		return err == nil, err
	})
	if err != nil {
		return err
	}
	if !done || receipt == nil {
		return errConfigurationBusy
	}
	if err := validateLANLinkEffectPublication(nc, config, receipt, now); err != nil {
		return err
	}
	var previous *cvk.NetworkControllerConfigurationActionApplication
	for i := range nc.Status.Configuration.ActionApplications {
		old := &nc.Status.Configuration.ActionApplications[i]
		if old.Section != receipt.Section || old.Resource != receipt.Resource {
			continue
		}
		if previous != nil {
			return errConfigurationOwned
		}
		if _, err := parseLANLinkEffect(old, now); err != nil {
			return err
		}

		previous = old.DeepCopy()
	}
	if previous != nil {
		return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now, previous)
	}
	return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now)
}
