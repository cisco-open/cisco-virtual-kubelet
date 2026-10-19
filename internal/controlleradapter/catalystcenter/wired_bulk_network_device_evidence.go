// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Compare supported scoped network intent with fresh device-worker observations.
// Success covers only these declared fields. This is deliberately not exposed
// as a final wiredBulkConvergenceProof: switching profiles, other settings and
// the complete rendered provisioning footprint still require qualification.
func (a *adapter) observeWiredBulkDeclaredNetwork(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, batch wiredBulkBatch, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, completed, now time.Time) error {
	if a == nil || a.client == nil || config == nil || source == nil || op == nil || op.Phase != "Verifying" || contract.ValidateGroupJournalIdentity(op) != nil {
		return errConfigurationDeviceAdmission
	}
	expected, err := compileWiredBulkNetworkContext(source, batch)
	if err != nil {
		return err
	}
	if expected == nil {
		return errWiredBulkNetworkContextUnavailable
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return err
	}
	native, targets, err := a.client.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members, dependencies...)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
		return errConfigurationStale
	}
	wants := map[string][]networkSetting{}
	queries := map[string][]string{}
	for _, member := range batch.members {
		device, err := resolveConfigurationDevice(member.desired.reference, native.physical())
		if err != nil {
			return err
		}
		settings := expected[member.desired.reference.Name]
		commands, err := wiredBulkNetworkCommands(settings)
		if err != nil {
			return err
		}
		wants[device.ID], queries[device.ID] = settings, commands
	}
	evidence, err := a.collectWiredBulkDeviceEvidence(ctx, config, source, batch, op, members, authorize, queries, completed, now)
	if err != nil {
		return err
	}
	for id, settings := range wants {
		if err := verifyWiredBulkNetworkCLI(settings, evidence[id].outputs); err != nil {
			return err
		}
	}
	return a.checkConfigurationSnapshot(ctx, config, source)
}
