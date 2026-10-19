// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Report shares Apply's prerequisite observations without acquiring managed
// authority. Native false alone cannot attest the separate disable reload.
func (c *client) compileFabricEWLCReport(raw []byte) (*sectionReportPlan, error) {
	intents, err := parseFabricEWLCIntents(raw)
	if err != nil {
		return nil, err
	}
	observe := func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, []string, error) {
		drift := []configapi.NetworkControllerDriftEntry{}
		evidence := []string{}
		seen := map[string]bool{}
		for _, want := range intents {
			deps, err := c.fabricEWLCFabricDependencies(ctx, want)
			if err != nil {
				return nil, nil, err
			}
			if seen[deps.target.ID] {
				return nil, nil, errConfigurationStale
			}
			seen[deps.target.ID] = true
			payload, err := fabricEWLCPayload(deps.target.ID, want.settings)
			if err != nil {
				return nil, nil, err
			}
			evidence = append(evidence, deps.digest)
			if deps.observed != nil && *deps.observed == payload {
				if !payload.Enabled {
					return nil, nil, errFabricEWLCDisableUnavailable
				}
				continue
			}
			drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "inventory", Resource: fabricEWLCResource(deps, payload), Path: "/inventory/devices", Change: configapi.NetworkControllerDriftChangeUpdate})
		}
		return drift, evidence, nil
	}
	return &sectionReportPlan{name: "inventory", count: len(intents), note: "Embedded wireless settings with converged fabric, provisioning and AP-placement prerequisites. Differences do not grant mutation authority. Native disable requires separate reload verification; platform/package support is not attested.", observe: func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
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
