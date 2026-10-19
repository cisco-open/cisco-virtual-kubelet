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
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// ReadReservationCandidate resolves the sole declared physical device and live
// workers without controller API calls or writes. Reader must be uncached with
// cluster-wide CiscoDevice visibility: a namespace-only list cannot establish
// exclusive physical identity. The manager must still check deployed admission
// and rollout compatibility before creating the candidate.
func ReadReservationCandidate(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time) (*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("configuration reservation candidate cannot bind current exclusive device and controller state")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || now.IsZero() {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Spec.Type == "" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Worker == nil || nc.Status.Worker.DeploymentName == "" {
		return nil, reject
	}
	physical, err := topologyidentity.CanonicalPhysicalIdentity(nc.Status.Configuration.Operation.TargetSerial)
	if err != nil {
		return nil, err
	}
	var devices cvk.CiscoDeviceList
	if err := reader.List(ctx, &devices); err != nil {
		return nil, err
	}
	var device *cvk.CiscoDevice
	for i := range devices.Items {
		d := &devices.Items[i]
		serial, err := topologyidentity.CanonicalPhysicalIdentity(d.Spec.PhysicalIdentity)
		if err != nil || serial != physical {
			continue
		}
		if device != nil {
			return nil, reject
		}
		device = d
	}
	if device == nil || device.Namespace != nc.Namespace {
		return nil, reject
	}
	nodeName := device.Spec.NodeName
	if nodeName == "" {
		nodeName = device.Name
	}
	var node core.Node
	if err := reader.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
		return nil, err
	}
	podName := node.Annotations[managedprotocol.AnnotationNetworkWorkerPodName]
	if podName == "" {
		return nil, reject
	}
	var networkPod core.Pod
	if err := reader.Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: podName}, &networkPod); err != nil {
		return nil, err
	}
	if string(networkPod.UID) != node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] {
		return nil, reject
	}
	deployment, connector, err := readCandidateConnector(ctx, reader, &nc)
	if err != nil {
		return nil, err
	}
	candidate, err := ReservationCandidate(&nc, device, &node, &networkPod, connector, now)
	if err != nil {
		return nil, err
	}
	if err := readCandidateIntentBinding(ctx, reader, candidate, nc.Spec.Type); err != nil {
		return nil, err
	}
	if err := ReadControllerWorkerBinding(ctx, reader, candidate, now); err != nil {
		return nil, err
	}
	// Corroborate the observations used to construct the candidate after gathering
	// source and ownership evidence. Workers repeat these checks before grants.
	for _, original := range []client.Object{&nc, device, &node, &networkPod, deployment, connector} {
		fresh := original.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(original), fresh); err != nil {
			return nil, err
		}
		if fresh.GetUID() != original.GetUID() || fresh.GetResourceVersion() != original.GetResourceVersion() {
			return nil, reject
		}
	}
	return candidate, nil
}

// readCandidateConnector selects the sole live connector; callers separately
// verify its Deployment/ReplicaSet ownership and readiness contract.
func readCandidateConnector(ctx context.Context, reader client.Reader, nc *cvk.NetworkController) (*apps.Deployment, *core.Pod, error) {
	reject := errors.New("configuration connector selection is ambiguous")
	if nc == nil || nc.Status.Worker == nil || nc.Status.Worker.DeploymentName == "" {
		return nil, nil, reject
	}
	var deployment apps.Deployment
	if err := reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: nc.Status.Worker.DeploymentName}, &deployment); err != nil {
		return nil, nil, err
	}
	if deployment.Spec.Selector == nil {
		return nil, nil, reject
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil || selector.Empty() {
		return nil, nil, reject
	}
	var pods core.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(nc.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, nil, err
	}
	var connector *core.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == core.PodSucceeded || p.Status.Phase == core.PodFailed {
			continue
		}
		if connector != nil {
			return nil, nil, reject
		}
		connector = p
	}
	if connector == nil {
		return nil, nil, reject
	}
	return &deployment, connector, nil
}
