// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Supply the persisted evidence checked by ReadGroupAuthority. Native workload
// preparation and manager/device status writes are simulated, not lab-qualified.
func installFabricGroupAuthorityFixture(t *testing.T, a *adapter, reuse ...bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	persist := func(obj ctrlclient.Object) {
		t.Helper()
		if len(reuse) > 0 && reuse[0] {
			current := obj.DeepCopyObject().(ctrlclient.Object)
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), current); err != nil {
				t.Fatal(err)
			}
			if current.GetUID() != obj.GetUID() {
				t.Fatal("fixture cannot replace an actor incarnation")
			}
			obj.SetResourceVersion(current.GetResourceVersion())
			if err := a.statusWriter.Update(ctx, obj); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := a.statusWriter.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}

	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	// Historical settled rows must not overwrite current actor/session evidence.
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		t.Fatal("missing current group operation")
	}
	current := list.Items[:0]
	for _, row := range list.Items {
		if row.Spec.Controller.UID == string(nc.UID) && row.Spec.OperationID == nc.Status.Configuration.Operation.ID {
			current = append(current, row)
		}
	}
	list.Items = current
	if len(list.Items) == 0 {
		t.Fatal("missing reservations")
	}
	if err := apps.AddToScheme(a.statusWriter.Scheme()); err != nil {
		t.Fatal(err)
	}
	first := &list.Items[0]
	one, controlled := int32(1), true
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"worker": "connector"}, Annotations: map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Args: []string{"controller-worker", "--controller-namespace=" + nc.Namespace, "--controller-name=" + nc.Name, "--controller-uid=" + string(nc.UID), "--controller-generation=" + strconv.FormatInt(nc.Generation, 10), "--controller-type=catalyst-center"}}}}}
	deployment := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: nc.Namespace, UID: "deployment", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": nc.Name, "cisco.vk/network-controller-uid": string(nc.UID)}}, Spec: apps.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: *template.DeepCopy()}, Status: apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "connector-rs", Namespace: nc.Namespace, UID: "replica", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: deployment.Name, UID: deployment.UID, Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: *template.DeepCopy()}}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: first.Spec.ControllerWorkerPod.Name, Namespace: nc.Namespace, UID: types.UID(first.Spec.ControllerWorkerPod.UID), Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controlled}}}, Spec: *template.Spec.DeepCopy(), Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: metav1.NewTime(now)}}}}
	for _, obj := range []ctrlclient.Object{deployment, rs, pod} {
		persist(obj)
	}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: deployment.Name, DeploymentName: deployment.Name, ReadyReplicas: 1}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for i := range list.Items {
		r := &list.Items[i]
		var d cvk.CiscoDevice
		var n core.Node
		var lease coordination.Lease
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &d); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: r.Spec.Node.Name}, &n); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: "leases", Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
			t.Fatal(err)
		}
		started, ready := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(-time.Second))
		requested, ack := metav1.NewTime(now.Add(-29*time.Second)), metav1.NewTime(now.Add(-25*time.Second))
		token := "11111111-1111-4111-8111-111111111111"
		d.Generation = r.Spec.DeviceGeneration
		d.Status.NetworkWorkerRevision = &cvk.DeviceNetworkWorkerRevisionStatus{DesiredRevision: r.Spec.DeviceWorkerRevision, ObservedRevision: r.Spec.DeviceWorkerRevision, PodUID: r.Spec.DeviceWorkerPod.UID, PodStartTime: &started, PodReadyTime: &ready}
		d.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionActive, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: token, DeviceUID: string(d.UID), NodeName: n.Name, NodeUID: string(n.UID), ControlRevision: r.Spec.ConfigGeneration, RequestedAt: requested, AcknowledgedAt: &ack, Operation: cvk.DeviceMaintenanceObjectReference{Namespace: r.Namespace, Name: r.Name, UID: string(r.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: "leases", Name: lease.Name, UID: string(lease.UID)}, Holder: r.Status.Grant.Holder}}
		if err := a.statusWriter.Update(ctx, &d); err != nil {
			t.Fatal(err)
		}
		n.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] = r.Spec.DeviceWorkerPod.Name
		n.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = r.Spec.DeviceWorkerPod.UID
		if err := a.statusWriter.Update(ctx, &n); err != nil {
			t.Fatal(err)
		}
		worker := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: r.Spec.DeviceWorkerPod.Name, Namespace: r.Namespace, UID: types.UID(r.Spec.DeviceWorkerPod.UID), Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: r.Spec.DeviceWorkerRevision}}, Spec: core.PodSpec{ServiceAccountName: "network"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
		persist(worker)
		lease.Annotations = map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: contract.Protocol, managedprotocol.AnnotationMaintenancePurpose: managedprotocol.MaintenancePurposeConfigurationMutation, managedprotocol.AnnotationMaintenanceSessionToken: token, managedprotocol.AnnotationMaintenanceRequestedAt: requested.Format(time.RFC3339Nano), managedprotocol.AnnotationMaintenanceOperationNS: r.Namespace, managedprotocol.AnnotationMaintenanceOperationName: r.Name, managedprotocol.AnnotationMaintenanceOperationUID: string(r.UID), managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(r.Spec.ConfigGeneration, 10)}
		if err := a.statusWriter.Update(ctx, &lease); err != nil {
			t.Fatal(err)
		}
	}
	a.workerPodName, a.workerPodUID = pod.Name, string(pod.UID)
	if a.configurationAdmission != nil {
		if !a.configurationGroupAdmissionReady() || a.configurationAdmission.leaseNamespace != "leases" {
			t.Fatal("fixture cannot replace an existing reservation runtime")
		}
	} else if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
