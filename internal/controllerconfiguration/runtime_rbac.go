// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadNetworkMutationRole corroborates the actual namespace-scoped grant to
// the bound device worker, using the same compiled role contract as managed
// topology. Readiness alone does not distinguish a read-only worker. This is
// one creation gate, not an admission-policy, namespace-delegation, Lease or
// controller-worker authorization audit. Existing reservation recovery must
// not depend on this check: access can be revoked while remote work continues.
func ReadNetworkMutationRole(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation) error {
	reject := errors.New("configuration reservation requires the current network read-write role binding")
	if reader == nil || r == nil || r.Namespace == "" || r.Spec.DeviceWorkerPod.Name == "" || r.Spec.DeviceWorkerPod.UID == "" {
		return reject
	}
	var pod core.Pod
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.DeviceWorkerPod.Name}, &pod); err != nil {
		return err
	}
	account := pod.Spec.ServiceAccountName
	if string(pod.UID) != r.Spec.DeviceWorkerPod.UID || pod.DeletionTimestamp != nil || account == "" || r.Spec.DeviceWorkerUsername != "system:serviceaccount:"+r.Namespace+":"+account {
		return reject
	}
	// Managed network access uses a reusable binding named after its account in
	// the device namespace. A different grant is not a substitute for that profile.
	var binding rbac.RoleBinding
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: account}, &binding); err != nil {
		return err
	}
	if binding.UID == "" || binding.DeletionTimestamp != nil || len(binding.OwnerReferences) != 0 ||
		binding.RoleRef != (rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadWriteClusterRole}) ||
		!reflect.DeepEqual(binding.Subjects, []rbac.Subject{{APIGroup: "", Kind: rbac.ServiceAccountKind, Namespace: r.Namespace, Name: account}}) {
		return reject
	}
	var role rbac.ClusterRole
	if err := reader.Get(ctx, client.ObjectKey{Name: binding.RoleRef.Name}, &role); err != nil {
		return err
	}
	if role.UID == "" || role.DeletionTimestamp != nil || managedprotocol.ValidateWorkerClusterRole(&role) != nil {
		return reject
	}
	// Detect changes while collecting evidence. The creation caller separately
	// rechecks the candidate; workers must re-authorize at their own boundaries.
	for _, original := range []client.Object{&pod, &binding, &role} {
		fresh := original.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(original), fresh); err != nil {
			return err
		}
		if original.GetUID() != fresh.GetUID() || original.GetResourceVersion() != fresh.GetResourceVersion() {
			return reject
		}
	}
	return nil
}
