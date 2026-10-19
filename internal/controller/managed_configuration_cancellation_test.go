// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func TestUnacknowledgedConfigurationCancellationSettlement(t *testing.T) {
	for _, scenario := range []string{"idle", "old settled session", "active session", "same session", "busy lease", "request remains", "timing remains", "node guard", "replacement node", "wrong lease binding", "future cancellation", "claim", "foreign device"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, d, n, row, lease := configurationMaintenanceFixture(t)
			d.Status.MaintenanceSession = nil
			row.Status = ops.ControllerConfigurationReservationStatus{PreparationCancelledAt: &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}}
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
				row.Status.PreparationCancelledAt.Time = time.Now().Add(time.Hour)
			case "claim":
				row.Status.Claim = &ops.ConfigurationReservationClaim{}
			case "foreign device":
				row.Spec.Device.UID = "other"
			}
			c := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(d, row).WithObjects(d, n, row, lease).Build()
			r.Client, r.APIReader = c, c
			err := r.settleUnacknowledgedConfigurationCancellations(ctx, d)
			success := scenario == "idle" || scenario == "old settled session"
			if success && err != nil {
				t.Fatal(err)
			}
			var actual ops.ControllerConfigurationReservation
			if err := c.Get(ctx, client.ObjectKeyFromObject(row), &actual); err != nil {
				t.Fatal(err)
			}
			if (actual.Status.ManagerSettledAt != nil) != success {
				t.Fatalf("unexpected settlement: %v (error %v)", actual.Status.ManagerSettledAt, err)
			}
			if success {
				stamp := actual.Status.ManagerSettledAt.DeepCopy()
				if err := r.settleUnacknowledgedConfigurationCancellations(ctx, d); err != nil {
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

func TestConfigurationReservationDeviceEvent(t *testing.T) {
	row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab"}, Spec: ops.ControllerConfigurationReservationSpec{Device: ops.ConfigurationReservationReference{Name: "switch"}}}
	requests := configurationReservationDeviceRequests(context.Background(), row)
	if len(requests) != 1 || requests[0].Namespace != "lab" || requests[0].Name != "switch" {
		t.Fatalf("wrong device event: %v", requests)
	}
	row.Spec.Device.Name = ""
	if len(configurationReservationDeviceRequests(context.Background(), row)) != 0 {
		t.Fatal("empty device queued")
	}
}
