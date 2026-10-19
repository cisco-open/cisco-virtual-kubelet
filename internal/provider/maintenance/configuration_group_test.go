// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package maintenance

import (
	"context"
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGroupMaintenanceRequiresCompleteAuthority(t *testing.T) {
	for _, mode := range []string{"missing-authorizer", "missing-controller", "v1-entry", "wrong-protocol"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, device, node, _, lease := managedCoordinatorFixture(t)
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "group-member", Namespace: device.Namespace, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.GroupReservationProtocol, Controller: ops.ConfigurationReservationReference{Name: "cc", UID: "controller"}, Device: ops.ConfigurationReservationReference{Name: device.Name, UID: string(device.UID)}, DeviceGeneration: device.Generation, Node: ops.ConfigurationReservationReference{Name: node.Name, UID: string(node.UID)}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: c.WorkerPodName, UID: c.WorkerPodUID}, DeviceWorkerUsername: c.ExpectedWorkerUsername, ConfigGeneration: 1}}
			if mode == "wrong-protocol" {
				r.Spec.Protocol = contract.Protocol
			}
			if err := c.Client.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			var before coordination.Lease
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &before); err != nil {
				t.Fatal(err)
			}
			authorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
			if mode == "missing-authorizer" {
				authorize = nil
			}
			var err error
			if mode == "v1-entry" {
				err = c.PrepareConfigurationMaintenance(ctx, r)
			} else {
				err = c.PrepareGroupConfigurationMaintenance(ctx, r, authorize)
			}
			if err == nil {
				t.Fatal("prepared without complete group authority")
			}
			var after coordination.Lease
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unauthorized maintenance request changed lease")
			}
		})
	}
}
