// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Reporting consumes the same complete, strict model as group preparation, but
// requires neither managed-device reservations nor a mutation-capable worker.
// Base provisioning/membership must be observable before interpreting handoffs.
func (c *client) compileFabricHandoffReport(raw []byte) (*sectionReportPlan, error) {
	devices, l2, ip, sda, err := parseAllFabricHandoffDeployment(raw)
	if err != nil {
		return nil, err
	}
	if len(l2)+len(ip)+len(sda) == 0 {
		return nil, errConfigurationTransition
	}
	observe := func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, []string, error) {
		drift := []configapi.NetworkControllerDriftEntry{}
		evidence := []string{}
		add := func(resource, path string, matching, conflicting bool) {
			if matching {
				return
			}
			change := configapi.NetworkControllerDriftChangeAdd
			if conflicting {
				change = configapi.NetworkControllerDriftChangeUpdate
			}
			drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: resource, Path: path, Change: change})
		}
		for _, device := range devices {
			deps, err := c.fabricDeviceDependencies(ctx, device)
			if err != nil {
				return nil, nil, err
			}
			actual, exists := deps.before[deps.target.ID]
			if !exists || !fabricDeviceConfigurationMatches(deps.native, actual) {
				return nil, nil, errConfigurationBusy
			}
			evidence = append(evidence, deps.digest)
		}
		for _, intent := range l2 {
			prerequisites, err := c.fabricL2HandoffPrerequisites(ctx, intent)
			if err != nil {
				return nil, nil, err
			}
			deps, err := c.fabricL2HandoffDependencies(ctx, intent.handoff, intent.device.inventory.reference, intent.device.fabricSite)
			if err != nil {
				return nil, nil, err
			}
			matching, conflicting := false, false
			desired := deps.binding.desired
			for _, row := range deps.before {
				matching = matching || fabricL2HandoffMatches(desired, row)
				conflicting = conflicting || strings.EqualFold(row.Port, desired.Port) && (row.ExternalVLAN == desired.ExternalVLAN || row.InternalVLAN == desired.InternalVLAN)
			}
			add(fabricL2HandoffResource(deps), "/border_devices/l2_handoffs", matching, conflicting)
			evidence = append(evidence, prerequisites.digest, deps.digest)
		}
		for _, intent := range ip {
			deps, err := c.fabricL3IPHandoffDependencies(ctx, intent.handoff, intent.device)
			if err != nil {
				return nil, nil, err
			}
			matching, conflicting := false, false
			desired := deps.binding.desired
			for _, row := range deps.before {
				matching = matching || fabricL3IPHandoffMatches(desired, row)
				conflicting = conflicting || strings.EqualFold(row.Port, desired.Port) && (row.VLAN == desired.VLAN || row.TransitID == desired.TransitID && row.Network == desired.Network)
			}
			add(fabricL3IPHandoffResource(deps), "/border_devices/l3_handoffs", matching, conflicting)
			evidence = append(evidence, deps.digest)
		}
		for _, intent := range sda {
			deps, err := c.fabricSDAHandoffDependencies(ctx, intent)
			if err != nil {
				return nil, nil, err
			}
			matching := false
			for _, row := range deps.before {
				matching = matching || fabricSDAHandoffMatches(deps.binding.desired, row)
			}
			add(fabricSDAHandoffResource(deps), "/border_devices/sda_transit", matching, len(deps.before) > 0)
			evidence = append(evidence, deps.digest)
		}
		return drift, evidence, nil
	}
	return &sectionReportPlan{name: "fabric", count: len(l2) + len(ip) + len(sda), note: "Native L2/IP/SDA handoff drift with converged base membership; Update denotes differences, not supported update execution. No device convergence, ownership or reservation authority is inferred.", observe: func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
		drift, before, err := observe(ctx)
		if err != nil {
			return nil, err
		}
		repeated, after, err := observe(ctx)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(drift, repeated) {
			return nil, errConfigurationStale
		}
		return drift, nil
	}}, nil
}
