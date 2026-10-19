// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"time"
)

func (a *adapter) claimConfigurationGroup(ctx context.Context) (*contract.GroupSubmissionPermit, error) {
	if !a.configurationGroupAdmissionReady() {
		return nil, errConfigurationDeviceAdmission
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	gate := a.configurationAdmission
	authorize := func(ctx context.Context, members []*ops.ControllerConfigurationReservation) error {
		for _, member := range members {
			if err := a.authorizeConfigurationGroupMember(ctx, member); err != nil {
				return err
			}
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		return contract.ReadGroupAuthority(ctx, a.statusReader, current, members, ops.ConfigurationReservationReference{Name: a.workerPodName, UID: a.workerPodUID}, gate.leaseNamespace, time.Now())
	}
	return contract.ClaimJournalGroupForController(ctx, a.statusReader, a.statusWriter, nc, gate.leaseNamespace, authorize, time.Now())
}
