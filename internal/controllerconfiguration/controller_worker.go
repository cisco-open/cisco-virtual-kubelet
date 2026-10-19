// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
	"time"
)

// ReadControllerWorkerBinding verifies the manager's retained Deployment
// annotations (controller workers intentionally have no controller ownerRef),
// the ReplicaSet/Pod UID chain and sole active worker. It checks current
// incarnation evidence, not controller credential values or mutation authority.
func ReadControllerWorkerBinding(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, now time.Time) error {
	if r == nil {
		return errors.New("configuration controller worker binding is absent")
	}
	return readControllerWorkerBinding(ctx, reader, r.Namespace, r.Spec.Controller, r.Spec.ControllerGeneration, r.Spec.ControllerWorkerPod, r.Spec.ControllerUsername, now)
}

// Shared actor validation; bootstrap callers have no CiscoDevice/Node and must
// not fabricate a managed reservation to reuse controller-worker checks.
func readControllerWorkerBinding(ctx context.Context, reader client.Reader, namespace string, controller ops.ConfigurationReservationReference, generation int64, worker ops.ConfigurationReservationReference, username string, now time.Time) error {
	reject := errors.New("configuration controller worker is not the current isolated connector")
	if reader == nil || namespace == "" || now.IsZero() || worker.UID == "" || worker.Name == "" {
		return reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: controller.Name}, &nc); err != nil {
		return err
	}
	if nc.UID == "" || string(nc.UID) != controller.UID || nc.Generation != generation || nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Status.Worker == nil || nc.Status.Worker.DeploymentName == "" {
		return reject
	}
	var deployment apps.Deployment
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: nc.Status.Worker.DeploymentName}, &deployment); err != nil {
		return err
	}
	if deployment.UID == "" || deployment.DeletionTimestamp != nil || len(deployment.OwnerReferences) != 0 || deployment.Annotations["cisco.vk/network-controller-name"] != nc.Name || deployment.Annotations["cisco.vk/network-controller-uid"] != string(nc.UID) || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 1 || deployment.Spec.Strategy.Type != apps.RecreateDeploymentStrategyType || deployment.Status.ObservedGeneration != deployment.Generation || deployment.Status.Replicas != 1 || deployment.Status.UpdatedReplicas != 1 || deployment.Status.ReadyReplicas != 1 {
		return reject
	}
	hash := deployment.Spec.Template.Annotations["cisco.vk/controller-contract-hash"]
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" || deployment.Spec.Selector == nil {
		return reject
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil || selector.Empty() {
		return reject
	}
	var pods core.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return err
	}
	var pod *core.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == core.PodSucceeded || p.Status.Phase == core.PodFailed {
			continue
		}
		if pod != nil {
			return reject
		}
		pod = p
	}
	if pod == nil || pod.Name != worker.Name || string(pod.UID) != worker.UID || pod.DeletionTimestamp != nil || pod.Status.Phase != core.PodRunning || pod.Annotations["cisco.vk/controller-contract-hash"] != hash || pod.Spec.ServiceAccountName == "" || pod.Spec.ServiceAccountName != deployment.Spec.Template.Spec.ServiceAccountName || username != "system:serviceaccount:"+namespace+":"+pod.Spec.ServiceAccountName {
		return reject
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == core.PodReady && condition.Status == core.ConditionTrue && !condition.LastTransitionTime.IsZero() && !condition.LastTransitionTime.After(now) {
			ready = true
		}
	}
	if !ready {
		return reject
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != apps.SchemeGroupVersion.String() || owner.Kind != "ReplicaSet" || owner.UID == "" {
		return reject
	}
	var rs apps.ReplicaSet
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, &rs); err != nil {
		return err
	}
	parent := metav1.GetControllerOf(&rs)
	if rs.UID != owner.UID || rs.DeletionTimestamp != nil || parent == nil || parent.APIVersion != apps.SchemeGroupVersion.String() || parent.Kind != "Deployment" || parent.Name != deployment.Name || parent.UID != deployment.UID || rs.Spec.Template.Annotations["cisco.vk/controller-contract-hash"] != hash {
		return reject
	}
	for _, spec := range []*core.PodSpec{&deployment.Spec.Template.Spec, &rs.Spec.Template.Spec, &pod.Spec} {
		var worker *core.Container
		for i := range spec.Containers {
			if spec.Containers[i].Name == "controller-worker" {
				if worker != nil {
					return reject
				}
				worker = &spec.Containers[i]
			}
		}
		if worker == nil || len(worker.Args) == 0 || worker.Args[0] != "controller-worker" {
			return reject
		}
		for flag, want := range map[string]string{"--controller-namespace=": namespace, "--controller-name=": nc.Name, "--controller-uid=": string(nc.UID), "--controller-generation=": strconv.FormatInt(nc.Generation, 10), "--controller-type=": string(nc.Spec.Type)} {
			found := 0
			for _, arg := range worker.Args {
				if arg == strings.TrimSuffix(flag, "=") {
					return reject
				}
				if strings.HasPrefix(arg, flag) {
					if arg != flag+want {
						return reject
					}
					found++
				}
			}
			if found != 1 {
				return reject
			}
		}
	}
	var fresh apps.Deployment
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&deployment), &fresh); err != nil {
		return err
	}
	if fresh.UID != deployment.UID || fresh.ResourceVersion != deployment.ResourceVersion {
		return reject
	}
	var current cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&nc), &current); err != nil {
		return err
	}
	if current.UID != nc.UID || current.ResourceVersion != nc.ResourceVersion {
		return reject
	}
	return nil
}
