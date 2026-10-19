// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestManagementAddressRetainedBindingKeepsOldWorkerIncarnation(t *testing.T) {
	for _, mode := range []string{"valid", "address-changed", "generation-changed", "worker-replaced", "worker-not-ready", "node-replaced", "alias", "reservation-replaced", "node-read-race", "late-alias", "lease-valid", "lease-foreign", "lease-expired", "lease-wrong-claim", "lease-controller-replaced", "lease-task-changed"} {
		t.Run(mode, func(t *testing.T) {
			ref, _, op, reservation := managementAddressBaselineFixture(t)
			now := time.Now()
			started, ready := metav1.NewTime(now.Add(-2*time.Minute)), metav1.NewTime(now.Add(-time.Minute))
			reservation.Spec.DeviceWorkerPod = ops.ConfigurationReservationReference{Name: "worker", UID: "worker"}
			reservation.Spec.DeviceWorkerUsername = "system:serviceaccount:lab:network"
			reservation.Spec.DeviceWorkerRevision = "revision"
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: reservation.Spec.ManagementAddress, PhysicalIdentity: "serial"}}
			device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeUID: "node", NodeName: "switch", PhysicalIdentity: "serial"}
			device.Status.NetworkWorkerRevision = &cvk.DeviceNetworkWorkerRevisionStatus{DesiredRevision: "revision", ObservedRevision: "revision", PodUID: "worker", PodStartTime: &started, PodReadyTime: &ready}
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node", Annotations: map[string]string{managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: "lab", managedprotocol.AnnotationDeviceName: "switch", managedprotocol.AnnotationDeviceUID: "device", managedprotocol.AnnotationNodeUID: "node", managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version, managedprotocol.AnnotationNetworkWorkerUsername: reservation.Spec.DeviceWorkerUsername}}}
			node.Status.NodeInfo.MachineID = "serial"
			worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "worker", UID: "worker", Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: "revision"}}, Spec: corev1.PodSpec{ServiceAccountName: "network"}, Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: ready}}}}
			expected := reservation.DeepCopy()
			switch mode {
			case "address-changed":
				device.Spec.Address = ref.IP
			case "generation-changed":
				device.Generation++
			case "worker-replaced":
				worker.UID = "new-worker"
			case "worker-not-ready":
				worker.Status.Conditions = nil
			case "node-replaced":
				node.UID = "new-node"
			case "reservation-replaced":
				reservation.UID = "new-reservation"
			}
			objects := []ctrlclient.Object{reservation, device, node, worker}
			leaseMode := strings.HasPrefix(mode, "lease-")
			if leaseMode {
				nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "catc", UID: "controller", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: TypeName}}
				nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy()}
				if mode == "lease-controller-replaced" {
					nc.UID = "replacement"
				}
				if mode == "lease-task-changed" {
					nc.Status.Configuration.Operation.TaskID = "different-task"
				}
				holder, err := contract.Holder("lab", reservation)
				if err != nil {
					t.Fatal(err)
				}
				name := engine.LeaseName(devicecoordination.DeviceKey("lab", "switch"), devicecoordination.MutationLeaseFamily)
				duration, transitions := int32(60), int32(0)
				acquired := metav1.NewMicroTime(now.Add(-30 * time.Second))
				renewed := metav1.NewMicroTime(now.Add(-time.Second))
				grant := metav1.NewTime(acquired.Time.Truncate(time.Second))
				reservation.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: name, UID: "lease"}, Holder: holder, LeaseTransitions: transitions, GrantedAt: grant}
				reservation.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: reservation.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
				if mode == "lease-wrong-claim" {
					reservation.Status.Claim.OperationID = "wrong"
				}
				lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: name, UID: "lease"}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, AcquireTime: &acquired, RenewTime: &renewed, LeaseTransitions: &transitions}}
				if mode == "lease-foreign" {
					lease.UID = "foreign"
				}
				if mode == "lease-expired" {
					lease.Spec.RenewTime = &metav1.MicroTime{Time: now.Add(-2 * time.Minute)}
				}
				objects = append(objects, nc, lease)
				expected = reservation.DeepCopy()
			}
			if mode == "alias" {
				alias := device.DeepCopy()
				alias.Namespace, alias.Name, alias.UID = "other", "alias", "alias"
				objects = append(objects, alias)
			}
			scheme := runtime.NewScheme()
			if err := cvk.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := ops.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := coordination.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			if leaseMode {
				var nc cvk.NetworkController
				if err := reader.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "lab", Name: "catc"}, &nc); err != nil {
					t.Fatal(err)
				}
				actualOperation := nc.Status.Configuration.Operation.DeepCopy()
				if mode == "lease-task-changed" {
					actualOperation.TaskID = op.TaskID
				}
				baseline, oldDevice, oldNode, err := readRetainedManagementAddressLease(context.Background(), reader, "lab", ref, actualOperation, expected, "leases", now)
				if mode != "lease-valid" {
					if err == nil || baseline != nil || oldDevice != nil || oldNode != nil {
						t.Fatal("uncorrelated retained lease returned evidence")
					}
					return
				}
				if err != nil || baseline == nil || baseline.before != reservation.Spec.ManagementAddress || oldDevice.UID != "device" || oldNode.UID != "node" {
					t.Fatal("retained worker/lease integration failed", err)
				}
				return
			}
			var apiReader ctrlclient.Reader = reader
			if mode == "node-read-race" || mode == "late-alias" {
				apiReader = &managementBindingRaceReader{Client: reader, mode: mode}
			}
			baseline, oldDevice, oldNode, err := readRetainedManagementAddressBinding(context.Background(), apiReader, "lab", ref, op, expected, now)
			if mode != "valid" {
				if err == nil || baseline != nil || oldDevice != nil || oldNode != nil {
					t.Fatal("stale retained worker binding returned partial evidence")
				}
				return
			}
			if err != nil || baseline == nil || baseline.before != reservation.Spec.ManagementAddress || baseline.after != ref.IP || oldDevice.UID != "device" || oldNode.UID != "node" || oldDevice.Spec.Address != baseline.before {
				t.Fatal("retained admitted endpoint lost", err)
			}
		})
	}
}

type managementBindingRaceReader struct {
	ctrlclient.Client
	mode    string
	changed bool
}

func (r *managementBindingRaceReader) Get(ctx context.Context, key ctrlclient.ObjectKey, object ctrlclient.Object, options ...ctrlclient.GetOption) error {
	if err := r.Client.Get(ctx, key, object, options...); err != nil {
		return err
	}
	if _, worker := object.(*corev1.Pod); !worker || key.Name != "worker" || r.changed {
		return nil
	}
	r.changed = true
	if r.mode == "node-read-race" {
		var node corev1.Node
		if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Name: "switch"}, &node); err != nil {
			return err
		}
		node.Annotations["test.cisco.vk/changed-after-worker-read"] = "true"
		return r.Client.Update(ctx, &node)
	}
	var device cvk.CiscoDevice
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: "switch"}, &device); err != nil {
		return err
	}
	device.Namespace, device.Name, device.UID, device.ResourceVersion = "other", "late-alias", "alias", ""
	return r.Client.Create(ctx, &device)
}
