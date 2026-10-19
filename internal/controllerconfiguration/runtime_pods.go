// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadRuntimeInstallationBinding checks both operation-bound Pod incarnations
// against the caller's installation identities. It prevents mixed deployments
// from granting new work while one worker still uses an older installation.
// It does not replace the worker ownership/readiness or admission-policy checks.
func ReadRuntimeInstallationBinding(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, binding RuntimeAdmissionBinding) error {
	reject := errors.New("configuration workers do not share the current installation binding")
	if reader == nil || r == nil || r.Namespace == "" || binding.Validate() != nil || r.Spec.Protocol != binding.Protocol || r.Spec.DeviceWorkerUsername != "system:serviceaccount:"+r.Namespace+":"+binding.NetworkManagementServiceAccount {
		return reject
	}
	for _, worker := range []struct {
		ref       ops.ConfigurationReservationReference
		container string
	}{{r.Spec.DeviceWorkerPod, "cisco-vk"}, {r.Spec.ControllerWorkerPod, "controller-worker"}} {
		ref := worker.ref
		if ref.Name == "" || ref.UID == "" {
			return reject
		}
		var pod core.Pod
		if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: ref.Name}, &pod); err != nil {
			return err
		}
		if string(pod.UID) != ref.UID || pod.DeletionTimestamp != nil {
			return reject
		}
		matches := 0
		for _, container := range pod.Spec.Containers {
			if container.Name != worker.container {
				continue
			}
			for _, env := range container.Env {
				if env.Name != RuntimeAdmissionEnvironment {
					continue
				}
				matches++
				if env.ValueFrom != nil {
					return reject
				}
				actual, err := ParseRuntimeAdmissionBinding(env.Value)
				if err != nil || actual == nil || *actual != binding {
					return reject
				}
			}
		}
		if matches != 1 {
			return reject
		}
	}
	return nil
}
