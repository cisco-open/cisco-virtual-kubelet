// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package configurationreservation

import (
	"context"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestGroupInitialGrantUsesMaintenanceCoordinator(t *testing.T) {
	testInitialGrantUsesWorkerAndJournalEvidence(t, false, true)
}

// Reuse the complete live intent/connector/device fixture from the single-device
// lifecycle. Manager acknowledgement below is fixture evidence; the device's
// coordinator, strict scanner and reservation reconciler are real components.
func testGroupPreparationFromLiveFixture(t *testing.T, r *Reconciler, old *ops.ControllerConfigurationReservation, nc *cvk.NetworkController, device *cvk.CiscoDevice, node *core.Node) {
	t.Helper()
	ctx := context.Background()
	if err := r.Client.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	op.TargetSerial = ""
	var err error
	op, err = contract.WithGroupIdentity(op, []contract.GroupTargetIdentity{{InventoryID: "inventory-id", PhysicalIdentity: device.Spec.PhysicalIdentity, Device: old.Spec.Device, Node: old.Spec.Node}})
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation = op
	if err := r.Client.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	rows, err := contract.ReadGroupReservationCandidates(ctx, r.Reader, nc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	member := rows[0]
	member.UID = "group-member-uid"
	if err := r.Client.Create(ctx, member); err != nil {
		t.Fatal(err)
	}
	node.Annotations[managedprotocol.AnnotationAppWorkerUsername] = "system:serviceaccount:lab:app"
	node.Annotations[managedprotocol.AnnotationWorkerUsername] = "system:serviceaccount:lab:app"
	if err := r.Client.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	var lease coordination.Lease
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: "leases", Name: engine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily)}, &lease); err != nil {
		t.Fatal(err)
	}
	for k, v := range node.Annotations {
		lease.Annotations[k] = v
	}
	lease.Annotations[managedprotocol.AnnotationNodeName] = node.Name
	lease.Annotations[managedprotocol.AnnotationWorkerUsername] = old.Spec.DeviceWorkerUsername
	lease.Annotations[managedprotocol.AnnotationLeasePurpose] = managedprotocol.LeasePurposeDeviceMutation
	lease.Labels = map[string]string{"cisco.vk/device": devicecoordination.DeviceKey(device.Namespace, device.Name), "cisco.vk/family": devicecoordination.MutationLeaseFamily}
	if err := r.Client.Update(ctx, &lease); err != nil {
		t.Fatal(err)
	}
	coordinator := &maintenance.Coordinator{Client: r.Client, Namespace: device.Namespace, DeviceName: device.Name, DeviceUID: string(device.UID), NodeName: node.Name, LeaseNamespace: "leases", ManagedTopology: true, WorkerMode: managedprotocol.WorkerModeNetworkManagement, WorkerPodName: old.Spec.DeviceWorkerPod.Name, WorkerPodUID: r.PodUID, ExpectedWorkerUsername: old.Spec.DeviceWorkerUsername}
	preparations, scans := 0, 0
	r.Authorize = func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
	r.Prepare = func(ctx context.Context, row *ops.ControllerConfigurationReservation) error {
		preparations++
		return coordinator.PrepareGroupConfigurationMaintenance(ctx, row, r.Authorize)
	}
	r.StrictDevicePodLister = func(context.Context) ([]*core.Pod, error) { scans++; return nil, nil }
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(member)}
	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("grant before manager acknowledgement")
	}
	if err := r.Client.Get(ctx, req.NamespacedName, member); err != nil {
		t.Fatal(err)
	}
	if member.Status.Grant != nil || scans != 0 {
		t.Fatal("unacknowledged preparation reached grant scan")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(&lease), &lease); err != nil {
		t.Fatal(err)
	}
	requested, err := time.Parse(time.RFC3339Nano, lease.Annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
	if err != nil {
		t.Fatal("maintenance request was not published", err)
	}
	at := metav1.NewTime(requested)
	ack := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	device.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionAcknowledged, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken], DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID), ControlRevision: member.Spec.ConfigGeneration, RequestedAt: at, AcknowledgedAt: &ack, Operation: cvk.DeviceMaintenanceObjectReference{Namespace: member.Namespace, Name: member.Name, UID: string(member.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID)}, Holder: *lease.Spec.HolderIdentity}}
	if err := r.Client.Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = []core.Taint{{Key: managedprotocol.MaintenanceTaintKey, Value: managedprotocol.MaintenanceTaintValue, Effect: core.TaintEffectNoSchedule}}
	if err := r.Client.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal("acknowledged group preparation", err)
	}
	if err := r.Client.Get(ctx, req.NamespacedName, member); err != nil {
		t.Fatal(err)
	}
	if member.Status.Grant == nil || member.Status.Grant.Phase != "Held" || scans != 1 || preparations != 2 {
		t.Fatal("group grant pipeline incomplete", preparations, scans)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal("group renewal", err)
	}
	if preparations != 2 || scans != 1 {
		t.Fatal("renewal repeated maintenance or native scan")
	}
}
