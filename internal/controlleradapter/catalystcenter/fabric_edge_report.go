// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func (c *client) compileFabricEdgeReport(raw []byte) (*sectionReportPlan, error) {
	model, err := parseFabricEdgeDeployment(raw)
	if err != nil {
		return nil, err
	}
	plans := []sectionReportPlan{}
	if len(model.channels) > 0 {
		plan, err := c.compileFabricPortChannelReport(model.channelSource)
		if err != nil {
			return nil, err
		}
		plans = append(plans, *plan)
	}
	if len(model.assignments) > 0 {
		plan, err := c.compileFabricPortAssignmentReport(model.assignmentSource)
		if err != nil {
			return nil, err
		}
		plans = append(plans, *plan)
	}
	count := 0
	for _, plan := range plans {
		count += plan.count
	}
	return &sectionReportPlan{name: "fabric", count: count, note: "Native channel and access-port drift. Each family requires stable repeated observations and converged base prerequisites. Update is classification only; no ownership or mutation authority is inferred.", observe: func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
		out := []configapi.NetworkControllerDriftEntry{}
		for _, plan := range plans {
			drift, err := plan.observe(ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, drift...)
		}
		return out, nil
	}}, nil
}
