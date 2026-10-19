// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func TestUnacknowledgedGroupCancellationSettlement(t *testing.T) {
	for _, scenario := range []string{"idle", "old settled session", "active session", "same session", "busy lease", "request remains", "timing remains", "node guard", "replacement node", "wrong lease binding", "future cancellation", "claim", "foreign device", "missing fence", "wrong ack", "denied", "changed node", "lost response"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, d, n, row, lease := configurationMaintenanceFixture(t)
			d.Status.MaintenanceSession = nil
			now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: row.Spec.OperationID, ConfigName: "config", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Resource: "multicast", Action: "Update", Phase: "Prepared"}, []contract.GroupTargetIdentity{{PhysicalIdentity: "serial-switch", InventoryID: "inventory-id", Device: row.Spec.Device, Node: row.Spec.Node}})
			if err != nil {
				t.Fatal(err)
			}
			op.Phase = "Cancelling"
			op.CancellationRequestedAt = &now
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: row.Namespace, Name: row.Spec.Controller.Name, UID: "controller-uid"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op}}}
			row.Spec.Protocol = contract.GroupReservationProtocol
			row.Spec.OperationDigest = op.PhysicalGroup.Digest
			row.Spec.Config = ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}
			row.Spec.ControllerGeneration = 1
			row.Spec.InventoryID = "inventory-id"
			row.Spec.PhysicalIdentity = "serial-switch"
			row.Spec.ControllerWorkerPod = ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}
			row.Spec.ControllerUsername = "connector-user"
			row.Finalizers = []string{contract.Finalizer}
			raw, _ := json.Marshal([]string{contract.GroupReservationProtocol, string(nc.UID), op.ID, op.PhysicalGroup.Digest, row.Spec.PhysicalIdentity})
			hash := sha256.Sum256(raw)
			row.Name = fmt.Sprintf("config-group-%x", hash[:16])
			row.Status = ops.ControllerConfigurationReservationStatus{GroupCancellation: &ops.ConfigurationReservationGroupCancellation{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, RequestedAt: now, AcknowledgedAt: now}}
			lease.Spec.HolderIdentity = nil
			lease.Spec.AcquireTime, lease.Spec.RenewTime, lease.Spec.LeaseDurationSeconds = nil, nil, nil
			for key := range lease.Annotations {
				if strings.Contains(key, "/maintenance-") {
					delete(lease.Annotations, key)
				}
			}
			switch scenario {
			case "old settled session", "active session", "same session":
				d.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionSettled, Operation: cvk.DeviceMaintenanceObjectReference{UID: "older"}}
				if scenario == "active session" {
					d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionActive
				}
				if scenario == "same session" {
					d.Status.MaintenanceSession.Operation.UID = string(row.UID)
				}
			case "busy lease":
				holder := "foreign"
				lease.Spec.HolderIdentity = &holder
			case "request remains":
				lease.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = string(row.UID)
			case "timing remains":
				lease.Spec.RenewTime = &metav1.MicroTime{Time: time.Now()}
			case "node guard":
				n.Spec.Taints = append(n.Spec.Taints, maintenanceGuardTaint())
			case "replacement node":
				n.UID = "replacement"
			case "wrong lease binding":
				lease.Annotations[managedprotocol.AnnotationDeviceUID] = "other"
			case "future cancellation":
				row.Status.GroupCancellation.AcknowledgedAt.Time = time.Now().Add(time.Hour)
			case "missing fence":
				nc.Status.Configuration.Operation = nil
			case "wrong ack":
				row.Status.GroupCancellation.OperationID = "wrong"
			case "claim":
				row.Status.Claim = &ops.ConfigurationReservationClaim{}
			case "foreign device":
				row.Spec.Device.UID = "other"
			}
			c := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(d, row).WithObjects(d, n, row, lease, nc).Build()
			r.Client, r.APIReader = c, c
			if scenario == "changed node" {
				r.APIReader = &groupCancellationChangedReader{Reader: c, nodeName: n.Name}
			}
			if scenario == "lost response" {
				r.Client = groupCancellationLostClient{Client: c}
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(row), row); err != nil {
				t.Fatal(err)
			}
			authorize := func(context.Context, *ops.ControllerConfigurationReservation) error {
				if scenario == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			err = r.settleUnacknowledgedGroupCancellation(ctx, row, authorize)
			success := scenario == "idle" || scenario == "old settled session"
			if (err == nil) != success {
				t.Fatal("unexpected result", err)
			}
			var actual ops.ControllerConfigurationReservation
			if err := c.Get(ctx, client.ObjectKeyFromObject(row), &actual); err != nil {
				t.Fatal(err)
			}
			if (actual.Status.ManagerSettledAt != nil) != (success || scenario == "lost response") {
				t.Fatalf("unexpected settlement: %v (error %v)", actual.Status.ManagerSettledAt, err)
			}
			if success || scenario == "lost response" {
				r.Client = c
				stamp := actual.Status.ManagerSettledAt.DeepCopy()
				if err := r.settleUnacknowledgedGroupCancellation(ctx, &actual, authorize); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(row), &actual); err != nil {
					t.Fatal(err)
				}
				if !actual.Status.ManagerSettledAt.Equal(stamp) {
					t.Fatal("rewrote settlement")
				}
			}
		})
	}
}

// Simulate a fresh second node observation changing during the manager proof.
type groupCancellationChangedReader struct {
	client.Reader
	nodeName string
	reads    int
}

func (r *groupCancellationChangedReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if obj.GetName() == r.nodeName && obj.GetNamespace() == "" {
		r.reads++
		if r.reads > 1 {
			obj.SetResourceVersion("changed")
		}
	}
	return nil
}

type groupCancellationLostClient struct{ client.Client }

func (c groupCancellationLostClient) Status() client.SubResourceWriter {
	return groupCancellationLostStatus{SubResourceWriter: c.Client.Status()}
}

type groupCancellationLostStatus struct{ client.SubResourceWriter }

func (w groupCancellationLostStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("response lost after durable write")
}
