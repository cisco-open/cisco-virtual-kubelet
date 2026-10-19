// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	reservationworker "github.com/cisco/virtual-kubelet-cisco/internal/provider/configurationreservation"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Actual shared reservation-worker release and retained-lease operations against
// fake Kubernetes. The maintenance manager's completed device/session cleanup is
// explicit fixture evidence, then validated by the real settlement publisher.
func settleLANDeleteWithWorkers(t *testing.T, a *adapter) {
	settleLANGroupWithWorkers(t, a, 2)
}

func settleLANGroupWithWorkers(t *testing.T, a *adapter, applicationCount int) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members, err := contract.ReadVerifiedGroupReservations(ctx, a.statusReader, nc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.completeConfigurationGroup(ctx, nc); err == nil {
		t.Fatal("completion skipped worker release")
	}
	assertLANClaimsRetained(t, a, true)
	var leases coordination.LeaseList
	if err := a.statusReader.List(ctx, &leases); err != nil {
		t.Fatal(err)
	}
	for i := range leases.Items {
		lease := &leases.Items[i]
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		lease.Annotations[devicecoordination.RetainLeaseAnnotation] = "true"
		if err := a.statusWriter.Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
	}
	leaser := &engine.FamilyLeaser{Client: a.statusWriter, Namespace: "leases", TTL: time.Minute, RequireExisting: true}
	for i, member := range members {
		worker := &reservationworker.Reconciler{Reader: a.statusReader, Client: a.statusWriter, Leaser: leaser, Namespace: member.Namespace, DeviceName: member.Spec.Device.Name, DeviceUID: member.Spec.Device.UID, PodUID: member.Spec.DeviceWorkerPod.UID, ControllerType: cvk.NetworkControllerType(TypeName), Authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
		for range 2 {
			if _, err := worker.Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(member)}); err != nil {
				t.Fatal("actual device-worker release", err)
			}
		}
		var fresh ops.ControllerConfigurationReservation
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), &fresh); err != nil {
			t.Fatal(err)
		}
		if fresh.Status.Grant.Phase != "Released" || fresh.Status.Completion == nil || fresh.Status.ManagerSettledAt != nil {
			t.Fatal("worker skipped release or fabricated manager evidence")
		}
		var lease coordination.Lease
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: "leases", Name: fresh.Status.Grant.Lease.Name}, &lease); err != nil {
			t.Fatal(err)
		}
		if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
			t.Fatal("canonical holder not released")
		}
		if err := a.completeConfigurationGroup(ctx, nc); err == nil {
			t.Fatal("released member cleared unsettled journal")
		}
		// Explicit fixture cleanup by the maintenance manager, not by the connector.
		var device cvk.CiscoDevice
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Device.Name}, &device); err != nil {
			t.Fatal(err)
		}
		device.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
		if err := a.statusWriter.Update(ctx, &device); err != nil {
			t.Fatal(err)
		}
		var node core.Node
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: member.Spec.Node.Name}, &node); err != nil {
			t.Fatal(err)
		}
		kept := node.Spec.Taints[:0]
		for _, taint := range node.Spec.Taints {
			if taint.Key != managedprotocol.MaintenanceTaintKey {
				kept = append(kept, taint)
			}
		}
		node.Spec.Taints = kept
		if err := a.statusWriter.Update(ctx, &node); err != nil {
			t.Fatal(err)
		}
		if err := contract.PublishGroupManagerSettlement(ctx, a.statusReader, a.statusWriter, nc, members, &fresh, "leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
			t.Fatal("manager settlement publication", err)
		}
		// Update the expected member snapshot, preserving immutable identities.
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
			t.Fatal(err)
		}
		err := a.completeConfigurationGroup(ctx, nc)
		if (err == nil) != (i == len(members)-1) {
			t.Fatal("incorrect all-member retirement", i, err)
		}
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.ActionApplications) != applicationCount {
		t.Fatal("terminal release lost effect history")
	}
	for _, member := range members {
		if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, member, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
			t.Fatal("retire settled member", err)
		}
	}
	var rows ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows.Items) != 0 {
		t.Fatal("settled original member remains")
	}
}
