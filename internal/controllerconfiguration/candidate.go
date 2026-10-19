// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

// ReservationCandidate constructs the immutable manager-owned binding from
// observed objects. It carries no UID, grant or execution authority. Before
// creating it, the manager must verify exclusive physical-device selection,
// live intent, connector ownership and deployed admission/rollout compatibility.
// Workers independently repeat live bindings after Kubernetes assigns its UID.
func ReservationCandidate(nc *cvk.NetworkController, device *cvk.CiscoDevice, node *core.Node, networkPod, controllerPod *core.Pod, now time.Time) (*ops.ControllerConfigurationReservation, error) {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return nil, errors.New("configuration operation is unavailable")
	}
	op := nc.Status.Configuration.Operation
	digest, err := OperationDigest(op)
	if err != nil {
		return nil, err
	}
	physical, err := topologyidentity.CanonicalPhysicalIdentity(op.TargetSerial)
	if err != nil {
		return nil, err
	}
	name, err := ReservationName(string(nc.UID), op.ID)
	if err != nil {
		return nil, err
	}
	return reservationCandidateForTarget(nc, device, node, networkPod, controllerPod, now, Protocol, name, digest, physical, op.TargetID, false)
}

// Protocol-specific wrappers validate the journal before calling this shared
// snapshot constructor. The returned object carries no grant or API identity.
func reservationCandidateForTarget(nc *cvk.NetworkController, device *cvk.CiscoDevice, node *core.Node, networkPod, controllerPod *core.Pod, now time.Time, protocol, name, digest, physical, inventory string, cancelling bool) (*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("configuration reservation candidate requires a prepared physical operation and current worker identities")
	if nc == nil || device == nil || node == nil || networkPod == nil || controllerPod == nil || nc.Namespace == "" || nc.Name == "" || nc.UID == "" || nc.Generation < 1 || nc.DeletionTimestamp != nil || nc.Spec.Paused || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || device.Namespace != nc.Namespace || device.Generation < 1 || device.Status.NetworkWorkerRevision == nil || controllerPod.Namespace != nc.Namespace || controllerPod.Name == "" || controllerPod.UID == "" || controllerPod.Spec.ServiceAccountName == "" || controllerPod.DeletionTimestamp != nil || controllerPod.Status.Phase != core.PodRunning {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	phase := "Prepared"
	if cancelling {
		if protocol != GroupReservationProtocol || ValidateGroupCancellationFence(nc, now) != nil {
			return nil, reject
		}
		phase = "Cancelling"
	}
	if op.Phase != phase || op.ControllerGeneration != nc.Generation || op.SubmittedAt != nil || op.TaskID != "" {
		return nil, reject
	}
	ready := false
	for _, c := range controllerPod.Status.Conditions {
		if c.Type == core.PodReady && c.Status == core.ConditionTrue && !c.LastTransitionTime.IsZero() && !c.LastTransitionTime.After(now) {
			ready = true
		}
	}
	if !ready {
		return nil, reject
	}
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: name, Finalizers: []string{Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{
		Protocol: protocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, Device: ops.ConfigurationReservationReference{Name: device.Name, UID: string(device.UID)}, Node: ops.ConfigurationReservationReference{Name: node.Name, UID: string(node.UID)}, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: controllerPod.Name, UID: string(controllerPod.UID)}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: networkPod.Name, UID: string(networkPod.UID)}, ControllerUsername: "system:serviceaccount:" + nc.Namespace + ":" + controllerPod.Spec.ServiceAccountName, DeviceWorkerUsername: "system:serviceaccount:" + nc.Namespace + ":" + networkPod.Spec.ServiceAccountName, ControllerGeneration: nc.Generation, ConfigGeneration: op.ConfigGeneration, DeviceGeneration: device.Generation, OperationID: op.ID, OperationDigest: digest, PhysicalIdentity: physical, InventoryID: inventory, ManagementAddress: device.Spec.Address, DeviceWorkerRevision: device.Status.NetworkWorkerRevision.ObservedRevision,
	}}
	if err := validateDeviceWorkerObjects(r, device, node, networkPod, now); err != nil {
		return nil, err
	}
	return r, nil
}
