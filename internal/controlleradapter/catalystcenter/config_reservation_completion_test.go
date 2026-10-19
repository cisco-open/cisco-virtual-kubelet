// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Fixture models retained API evidence after worker release and manager
// settlement. It does not simulate physical device execution.
func settledConfigurationFixture(t *testing.T, a *adapter) (*ops.ControllerConfigurationReservation, *cvk.CiscoDevice) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := nc.Status.Configuration.Operation
	name, err := contract.ReservationName(string(nc.UID), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contract.OperationDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: name, UID: types.UID(name + "-uid"), Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device-uid"}, Node: ops.ConfigurationReservationReference{Name: "switch", UID: "node-uid"}, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}, ControllerGeneration: op.ControllerGeneration, ConfigGeneration: op.ConfigGeneration, OperationID: op.ID, OperationDigest: digest, InventoryID: op.TargetID}}
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Second))
	if op.SubmittedAt != nil {
		at = metav1.NewTime(op.SubmittedAt.Add(-time.Second))
	}
	holder, _ := contract.Holder(r.Namespace, r)
	g := &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily), UID: "lease-uid"}, Holder: holder, GrantedAt: at}
	r.Status.Grant = g
	if op.Phase == "Verified" {
		r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: digest, ClaimedAt: at}
		r.Status.Completion, err = contract.CompletionForReceipt(r, nc, time.Now().UTC().Truncate(time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	g.Phase = "Released"
	r.Status.ManagerSettledAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}
	if err := a.statusWriter.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	session := &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionSettled, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: "11111111-1111-4111-8111-111111111111", DeviceUID: r.Spec.Device.UID, NodeName: r.Spec.Node.Name, NodeUID: r.Spec.Node.UID, ControlRevision: r.Spec.ConfigGeneration, RequestedAt: at, AcknowledgedAt: at.DeepCopy(), Operation: cvk.DeviceMaintenanceObjectReference{Namespace: r.Namespace, Name: r.Name, UID: string(r.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: g.LeaseNamespace, Name: g.Lease.Name, UID: g.Lease.UID}, Holder: g.Holder}}
	var d cvk.CiscoDevice
	err = a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &d)
	if apierrors.IsNotFound(err) {
		d.ObjectMeta = metav1.ObjectMeta{Namespace: r.Namespace, Name: r.Spec.Device.Name, UID: types.UID(r.Spec.Device.UID)}
		d.Status.MaintenanceSession = session
		if err := a.statusWriter.Create(ctx, &d); err != nil {
			t.Fatal(err)
		}
	} else {
		if err != nil {
			t.Fatal(err)
		}
		d.Status.MaintenanceSession = session
		if err := a.statusWriter.Update(ctx, &d); err != nil {
			t.Fatal(err)
		}
	}
	return r, &d
}

func TestPhysicalCompletionWaitsForWorkerAndManager(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device", "Update", configurationResourceIdentity{target: "device-id", serial: "serial"})
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	verified := nc.Status.Configuration.Operation
	verified.Phase = "Verified"
	verified.TaskID = "task"
	submitted := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	verified.SubmittedAt = &submitted
	nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: verified.Section, Resource: verified.Resource, Operation: *verified.DeepCopy()}}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("missing reservation released journal")
	}
	r, d := settledConfigurationFixture(t, a)
	r.Status.Grant.Phase = "Held"
	r.Status.ManagerSettledAt = nil
	r.Status.Completion = nil
	if err := a.statusWriter.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionActive
	if err := a.statusWriter.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	a.workerPodName, a.workerPodUID = r.Spec.ControllerWorkerPod.Name, "replacement"
	if err := a.completeConfigurationReservation(ctx, nc); err == nil {
		t.Fatal("replacement worker published completion")
	}
	a.workerPodUID = r.Spec.ControllerWorkerPod.UID
	if err := a.completeConfigurationReservation(ctx, nc); err == nil {
		t.Fatal("held reservation released journal")
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Completion == nil || journalState(t, a).Operation == nil {
		t.Fatal("completion not retained while waiting")
	}
	r.Status.Grant.Phase = "Released"
	if err := a.statusWriter.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := a.completeConfigurationReservation(ctx, nc); err == nil {
		t.Fatal("unsettled manager released journal")
	}
	if err := contract.PublishManagerSettlement(ctx, a.statusReader, a.statusWriter, r, time.Now()); err == nil {
		t.Fatal("unsettled session recorded manager proof")
	}
	d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
	if err := a.statusWriter.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := a.completeConfigurationReservation(ctx, nc); err == nil {
		t.Fatal("session alone bypassed retained manager evidence")
	}
	if err := contract.PublishManagerSettlement(ctx, a.statusReader, a.statusWriter, r, time.Now()); err != nil {
		t.Fatal("manager settlement", err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if err := contract.PublishManagerSettlement(ctx, a.statusReader, a.statusWriter, r, time.Now()); err != nil {
		t.Fatal("manager settlement retry", err)
	}
	// A later session cannot erase this reservation's durable settlement proof.
	d.Status.MaintenanceSession.Operation.UID = "later-operation"
	d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionActive
	if err := a.statusWriter.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := a.completeConfigurationReservation(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if state := journalState(t, a); state.Operation != nil || len(state.ActionApplications) != 1 {
		t.Fatal("settlement lost action receipt")
	}
}

func TestCancelledPreparationJournalWaitsForManager(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device", "Update", configurationResourceIdentity{target: "device-id", serial: "serial"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := settledConfigurationFixture(t, a)
	r.Status.Grant = nil
	r.Status.PreparationCancelledAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}
	r.Status.ManagerSettledAt = nil
	if err := a.statusWriter.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("cancel marker alone released journal")
	}
	if journalState(t, a).Operation == nil {
		t.Fatal("unsettled journal lost")
	}
	if err := contract.PublishManagerSettlement(ctx, a.statusReader, a.statusWriter, r, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	if journalState(t, a).Operation != nil {
		t.Fatal("settled cancellation retained journal")
	}
}
