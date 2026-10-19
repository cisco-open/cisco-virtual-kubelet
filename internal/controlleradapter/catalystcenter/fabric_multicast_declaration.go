// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "context"

// Shared declaration/foundation preflight for wired multicast and replication.
// Native VN convergence is checked by the later replication stage, allowing
// earlier VN operations to run while retaining complete model validation.
func (a *adapter) declaredWiredMulticastFoundation(ctx context.Context, source *configSourceSnapshot) (*fabricMulticastConfiguration, error) {
	if source == nil {
		return nil, errConfigurationTransition
	}
	plan, err := a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return nil, err
	}
	multicast, ok := plan.configurationExecutor.(*fabricMulticastConfiguration)
	if !ok || len(plan.reports) == 0 {
		return nil, errConfigurationTransition
	}
	for _, site := range multicast.desired {
		if site.intent.wireless != nil {
			return nil, errConfigurationDeviceAdmission
		}
	}
	for _, report := range plan.reports[:len(plan.reports)-1] {
		drift, err := report(ctx)
		if err != nil {
			return nil, err
		}
		if len(drift) > 0 {
			return nil, errConfigurationBusy
		}
	}
	return multicast, nil
}
