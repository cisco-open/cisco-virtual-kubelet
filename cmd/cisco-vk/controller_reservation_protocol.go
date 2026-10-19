// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Select the exact installation protocol without falling back to a legacy
// adapter. The callback still performs live per-operation authorization.
func configureControllerReservationProtocol(participant controlleradapter.ConfigurationReservationAdapter, protocol, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	if participant == nil || namespace == "" || authorize == nil {
		return fmt.Errorf("configuration reservation runtime is incomplete")
	}
	switch protocol {
	case contract.Protocol:
		return participant.ConfigureConfigurationReservations(namespace, authorize)
	case contract.GroupReservationProtocol:
		group, ok := participant.(controlleradapter.ConfigurationGroupReservationAdapter)
		if !ok {
			return fmt.Errorf("controller adapter does not implement group configuration reservations")
		}
		return group.ConfigureConfigurationGroupReservations(namespace, authorize)
	default:
		return fmt.Errorf("unsupported controller configuration reservation protocol")
	}
}
