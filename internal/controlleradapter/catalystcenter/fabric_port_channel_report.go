// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func (c *client) compileFabricPortChannelReport(raw []byte) (*sectionReportPlan, error) {
	devices, channels, err := parseFabricPortChannelDeployment(raw)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, errConfigurationTransition
	}
	observe := func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, []string, error) {
		drift := []configapi.NetworkControllerDriftEntry{}
		evidence := []string{}
		// Consume every declared base device, including those without channels.
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
		for _, want := range channels {
			deps, err := c.observeFabricPortChannel(ctx, want)
			if err != nil {
				return nil, nil, err
			}
			evidence = append(evidence, deps.digest)
			if deps.existing != nil {
				continue
			}
			change := configapi.NetworkControllerDriftChangeAdd
			if deps.conflict {
				change = configapi.NetworkControllerDriftChangeUpdate
			}
			drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: fabricPortChannelResource(deps), Path: "/inventory/devices/port_channels", Change: change})
		}
		return drift, evidence, nil
	}
	return &sectionReportPlan{name: "fabric", count: len(channels), note: "Native port-channel drift with converged base membership. Update indicates differences, not update authority. VLAN intent requires supported native readback; no device convergence or ownership is inferred.", observe: func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
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
