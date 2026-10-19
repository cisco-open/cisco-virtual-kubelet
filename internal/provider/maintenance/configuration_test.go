// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package maintenance

import (
	"context"
	"encoding/json"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func TestConfigurationMaintenanceRequest(t *testing.T) {
	c, device, node, _, lease := managedCoordinatorFixture(t)
	ctx := context.Background()
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "configuration", Namespace: device.Namespace, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Device: ops.ConfigurationReservationReference{Name: device.Name, UID: string(device.UID)}, Node: ops.ConfigurationReservationReference{Name: node.Name, UID: string(node.UID)}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: c.WorkerPodName, UID: c.WorkerPodUID}, DeviceWorkerUsername: c.ExpectedWorkerUsername, ConfigGeneration: 1}}
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	holder, _ := contract.Holder(r.Namespace, r)
	lease.Spec.HolderIdentity = &holder
	if err := c.Client.Update(ctx, lease); err != nil {
		t.Fatal(err)
	}
	request, err := c.publishConfigurationMaintenanceRequest(ctx, r, node)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.publishConfigurationMaintenanceRequest(ctx, r, node)
	if err != nil || request != again {
		t.Fatalf("request changed on retry: %v", err)
	}
	if request.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || request.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation {
		t.Fatal("configuration impersonated upgrade protocol")
	}
	session := acknowledgement(request, c, node)
	session.ProtocolVersion = request.ProtocolVersion
	session.Purpose = request.Purpose
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	if err = validateMaintenanceAcknowledgement(session, request, node); err != nil {
		t.Fatal(err)
	}
	session.Purpose = cvk.DeviceMaintenancePurposeSoftwareMutation
	if validateMaintenanceAcknowledgement(session, request, node) == nil {
		t.Fatal("upgrade acknowledgement accepted")
	}
	var current coordination.Lease
	if err = c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
		t.Fatal(err)
	}
	current.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = "other"
	if err = c.Client.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if _, err = c.publishConfigurationMaintenanceRequest(ctx, r, node); err == nil {
		t.Fatal("foreign session overwritten")
	}
	current.Annotations = lease.Annotations
	expired := metav1.NewMicroTime(time.Now().Add(-2 * time.Hour))
	current.Spec.RenewTime = &expired
	current.Spec.AcquireTime = &expired
	if err = c.Client.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if _, err = c.publishConfigurationMaintenanceRequest(ctx, r, node); err == nil {
		t.Fatal("expired lease authorized request")
	}
}

func TestPrepareConfigurationMaintenance(t *testing.T) {
	for _, scenario := range []string{"ready", "missing acknowledgement", "missing guard", "stale reservation", "wrong device generation", "wrong session", "claimed", "cancelled", "foreign lease"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			c, device, node, _, lease := managedCoordinatorFixture(t)
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "configuration", Namespace: device.Namespace, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Device: ops.ConfigurationReservationReference{Name: device.Name, UID: string(device.UID)}, DeviceGeneration: device.Generation, Node: ops.ConfigurationReservationReference{Name: node.Name, UID: string(node.UID)}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: c.WorkerPodName, UID: c.WorkerPodUID}, DeviceWorkerUsername: c.ExpectedWorkerUsername, ConfigGeneration: 1}}
			if err := c.Client.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			holder, _ := contract.Holder(r.Namespace, r)
			lease.Spec.HolderIdentity = &holder
			if err := c.Client.Update(ctx, lease); err != nil {
				t.Fatal(err)
			}
			if err := c.PrepareConfigurationMaintenance(ctx, r); err == nil {
				t.Fatal("prepared without acknowledgement")
			}
			request, err := c.publishConfigurationMaintenanceRequest(ctx, r, node)
			if err != nil {
				t.Fatal(err)
			}
			device.Status.MaintenanceSession = acknowledgement(request, c, node)
			device.Status.MaintenanceSession.ProtocolVersion = request.ProtocolVersion
			device.Status.MaintenanceSession.Purpose = request.Purpose
			node.Spec.Taints = []core.Taint{{Key: TaintKey, Value: TaintValue, Effect: core.TaintEffectNoSchedule}}
			switch scenario {
			case "missing acknowledgement":
				device.Status.MaintenanceSession = nil
			case "missing guard":
				node.Spec.Taints = nil
			case "stale reservation":
				r.ResourceVersion = "0"
			case "wrong device generation":
				device.Generation++
				if err := c.Client.Update(ctx, device); err != nil {
					t.Fatal(err)
				}
			case "wrong session":
				device.Status.MaintenanceSession.SessionToken = "22222222-2222-4222-8222-222222222222"
			case "claimed":
				changed := r.DeepCopy()
				changed.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: "claimed"}
				if err := c.Client.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				r.Status.PreparationCancelledAt = &metav1.Time{Time: time.Now().UTC()}
				if err := c.Client.Update(ctx, r); err != nil {
					t.Fatal(err)
				}
				if _, err := c.publishConfigurationMaintenanceRequest(ctx, r, node); err == nil {
					t.Fatal("cancelled reservation published maintenance request")
				}
			case "foreign lease":
				if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					t.Fatal(err)
				}
				other := "operational-action/foreign"
				lease.Spec.HolderIdentity = &other
				if err := c.Client.Update(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Client.Status().Update(ctx, device); err != nil {
				t.Fatal(err)
			}
			if err := c.Client.Update(ctx, node); err != nil {
				t.Fatal(err)
			}
			err = c.PrepareConfigurationMaintenance(ctx, r)
			if scenario == "ready" {
				if err != nil {
					t.Fatal(err)
				}
				if err := c.PrepareConfigurationMaintenance(ctx, r); err != nil {
					t.Fatal("retry", err)
				}
			} else if err == nil {
				t.Fatal("unsafe preparation succeeded")
			}
			var observed ops.ControllerConfigurationReservation
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(r), &observed); err != nil {
				t.Fatal(err)
			}
			if observed.Status.Grant != nil {
				t.Fatal("preparation published grant")
			}
		})
	}
}
