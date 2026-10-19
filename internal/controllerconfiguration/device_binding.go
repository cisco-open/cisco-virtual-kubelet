// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	core "k8s.io/api/core/v1"
	"net/netip"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// ValidateDeviceWorkerBinding checks the dedicated network worker and managed
// device/Node incarnations. It grants no mutation authority. Runtime callers
// must also match their own downward-API Pod UID to the reservation's bound Pod.
func ValidateDeviceWorkerBinding(r *ops.ControllerConfigurationReservation, d *cvk.CiscoDevice, n *core.Node, p *core.Pod, now time.Time) error {
	if r == nil || r.UID == "" {
		return errors.New("configuration reservation incarnation is unavailable")
	}
	return validateDeviceWorkerSnapshot(r, d, n, p, now)
}

// Candidate construction uses the same device checks before Kubernetes assigns
// the reservation UID. Runtime admission always uses the public UID-bound gate.
func validateDeviceWorkerSnapshot(r *ops.ControllerConfigurationReservation, d *cvk.CiscoDevice, n *core.Node, p *core.Pod, now time.Time) error {
	if r == nil || r.Spec.Protocol != Protocol {
		return errors.New("configuration reservation protocol is not supported")
	}
	return validateDeviceWorkerObjects(r, d, n, p, now)
}

// Object identity validation is shared by candidate formats. Runtime callers
// must still pass the protocol-specific admission and reservation UID gates.
func validateDeviceWorkerObjects(r *ops.ControllerConfigurationReservation, d *cvk.CiscoDevice, n *core.Node, p *core.Pod, now time.Time) error {
	reject := errors.New("configuration reservation device-worker binding is not current")
	if r == nil || d == nil || n == nil || p == nil || now.IsZero() || r.Namespace == "" || r.DeletionTimestamp != nil || d.DeletionTimestamp != nil || n.DeletionTimestamp != nil || p.DeletionTimestamp != nil {
		return reject
	}
	s := &r.Spec
	if d.Namespace != r.Namespace || d.Name != s.Device.Name || string(d.UID) != s.Device.UID || d.UID == "" || d.Generation != s.DeviceGeneration || d.Spec.Driver != cvk.DeviceDriverXE || n.Name != s.Node.Name || string(n.UID) != s.Node.UID || n.UID == "" {
		return reject
	}
	declaredNode := d.Spec.NodeName
	if declaredNode == "" {
		declaredNode = d.Name
	}
	if declaredNode != n.Name {
		return reject
	}
	address, err := netip.ParseAddr(d.Spec.Address)
	if err != nil || address.Zone() != "" {
		return reject
	}
	expected, err := netip.ParseAddr(s.ManagementAddress)
	if err != nil || expected.Zone() != "" || expected.Unmap() != address.Unmap() {
		return reject
	}
	physical, err := topologyidentity.ObservedPhysicalIdentity(d.Spec.PhysicalIdentity, n.Status.NodeInfo.MachineID, n.Status.NodeInfo.SystemUUID)
	if err != nil || physical != s.PhysicalIdentity {
		return reject
	}
	identity := d.Status.NodeIdentity
	if identity == nil || identity.DeviceUID != s.Device.UID || identity.NodeUID != s.Node.UID || identity.NodeName != s.Node.Name || identity.PhysicalIdentity != physical {
		return reject
	}
	worker := d.Status.NetworkWorkerRevision
	if worker == nil || s.DeviceWorkerRevision == "" || worker.DesiredRevision != s.DeviceWorkerRevision || worker.ObservedRevision != s.DeviceWorkerRevision || worker.PodUID != s.DeviceWorkerPod.UID || worker.PodStartTime == nil || worker.PodReadyTime == nil || worker.PodReadyTime.Before(worker.PodStartTime) || worker.PodReadyTime.After(now) {
		return reject
	}
	if p.Namespace != r.Namespace || p.Name != s.DeviceWorkerPod.Name || string(p.UID) != s.DeviceWorkerPod.UID || p.UID == "" || p.Spec.ServiceAccountName == "" || s.DeviceWorkerUsername != "system:serviceaccount:"+p.Namespace+":"+p.Spec.ServiceAccountName || p.Status.Phase != core.PodRunning || p.Annotations[managedprotocol.AnnotationWorkerConfigRevision] != s.DeviceWorkerRevision {
		return reject
	}
	ready := false
	for _, condition := range p.Status.Conditions {
		if condition.Type == core.PodReady && condition.Status == core.ConditionTrue && !condition.LastTransitionTime.IsZero() && !condition.LastTransitionTime.After(now) {
			ready = true
		}
	}
	if !ready {
		return reject
	}
	for key, value := range map[string]string{
		managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: r.Namespace,
		managedprotocol.AnnotationDeviceName: s.Device.Name, managedprotocol.AnnotationDeviceUID: s.Device.UID,
		managedprotocol.AnnotationNodeUID: s.Node.UID, managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version,
		managedprotocol.AnnotationNetworkWorkerUsername: s.DeviceWorkerUsername,
	} {
		if n.Annotations[key] != value {
			return reject
		}
	}
	return nil
}

// ReadDeviceWorkerBinding uses an uncached reader and rechecks the device
// resourceVersion after collecting Node and Pod evidence. The caller must still
// revalidate at dispatch boundaries; no multi-object atomicity is implied.
func ReadDeviceWorkerBinding(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, now time.Time) error {
	if r == nil || r.Spec.Protocol != Protocol {
		return errors.New("configuration reservation protocol is not supported")
	}
	return readDeviceWorkerObjects(ctx, reader, r, now)
}

func readDeviceWorkerObjects(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, now time.Time) error {
	if reader == nil || r == nil || r.UID == "" {
		return errors.New("configuration device binding reader unavailable")
	}
	var d cvk.CiscoDevice
	var n core.Node
	var p core.Pod
	key := client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}
	if err := reader.Get(ctx, key, &d); err != nil {
		return err
	}
	if err := reader.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &n); err != nil {
		return err
	}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.DeviceWorkerPod.Name}, &p); err != nil {
		return err
	}
	if err := validateDeviceWorkerObjects(r, &d, &n, &p, now); err != nil {
		return err
	}
	var current cvk.CiscoDevice
	if err := reader.Get(ctx, key, &current); err != nil {
		return err
	}
	if current.UID != d.UID || current.ResourceVersion != d.ResourceVersion {
		return errors.New("configuration device changed during binding reads")
	}
	return nil
}
