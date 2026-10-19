// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

func validateConfigurationReservationProtocol(protocol string, managed bool, networkAccess string) error {
	switch protocol {
	case contract.Protocol:
		return nil
	case contract.GroupReservationProtocol:
		if managed && networkAccess == managedprotocol.WorkerAccessReadWrite {
			return nil
		}
		return fmt.Errorf("group configuration protocol requires managed topology and readWrite network-management access")
	default:
		return fmt.Errorf("unsupported configuration-reservation-protocol")
	}
}

// This read-only startup check rejects observed incompatible retained evidence.
// It is not a distributed rollout lock: installed handoff qualification must
// also cover an old worker starting work concurrently with manager replacement.
func verifyConfigurationProtocolRetainedState(ctx context.Context, reader client.Reader, protocol string) error {
	if reader == nil || (protocol != contract.Protocol && protocol != contract.GroupReservationProtocol) {
		return fmt.Errorf("configuration protocol verification is incomplete")
	}
	var controllers cvk.NetworkControllerList
	if err := reader.List(ctx, &controllers); err != nil {
		return err
	}
	for _, nc := range controllers.Items {
		if nc.Spec.Type != "catalyst-center" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
			continue
		}
		op := nc.Status.Configuration.Operation
		retained := contract.Protocol
		groupResource := false
		if op.Section == "fabric" {
			for _, prefix := range []string{"extranet-create:", "extranet-update:", "extranet-update-v2:", "fabric-replication-update:", "fabric-multicast-vn:", "fabric-pending-event:", "fabric-l2-handoff-create:", "fabric-l3-ip-handoff-create:", "fabric-sda-handoff-create:", "fabric-port-channel-create:", "fabric-port-assignment-create:", "fabric-wireless-multicast-update:", "fabric-vlan-ssid-update:"} {
				groupResource = groupResource || strings.HasPrefix(op.Resource, prefix)
			}
		}
		groupResource = groupResource || op.Section == "inventory" && strings.HasPrefix(op.Resource, "fabric-ewlc-update:")
		if op.PhysicalGroup != nil || groupResource {
			retained = contract.GroupReservationProtocol
		} else if op.TargetSerial == "" {
			continue
		}
		if retained != protocol {
			return fmt.Errorf("configuration protocol change conflicts with retained operation on %s/%s", nc.Namespace, nc.Name)
		}
	}
	var reservations ops.ControllerConfigurationReservationList
	if err := reader.List(ctx, &reservations); err != nil {
		return err
	}
	for _, row := range reservations.Items {
		if row.Spec.Protocol != protocol {
			return fmt.Errorf("configuration protocol change requires retirement of reservation %s/%s", row.Namespace, row.Name)
		}
	}
	return nil
}
