// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const configurationAccessControllerUID = "configuration.cisco.vk/controller-uid"
const configurationAccessControllerNamespace = "configuration.cisco.vk/controller-namespace"

func configurationAccessName(nc *cvk.NetworkController) string {
	hash := sha256.Sum256([]byte(nc.Namespace + "\x00" + string(nc.UID)))
	return fmt.Sprintf("cvk-configuration-read-%x", hash[:12])
}
func configurationAccessOwned(obj client.Object, nc *cvk.NetworkController) bool {
	return obj.GetName() == configurationAccessName(nc) && len(obj.GetOwnerReferences()) == 0 && obj.GetLabels()[configurationAccessControllerUID] == string(nc.UID) && obj.GetAnnotations()[configurationAccessControllerNamespace] == nc.Namespace && isNetworkControllerWorkerObject(obj, nc)
}

// Reuse existing observation-only roles. Controller workers never receive the
// network mutation role or Lease writes. Account and worker retirement precede
// removal of these grants, as for the normal connector namespace binding.
func (r *NetworkControllerReconciler) reconcileConfigurationReadAccess(ctx context.Context, nc *cvk.NetworkController) error {
	if r.ConfigurationAdmissionBinding == nil || nc.Spec.Type != "catalyst-center" {
		return nil
	}
	if r.APIReader == nil || nc.UID == "" {
		return fmt.Errorf("configuration read access requires authoritative controller identity")
	}
	if err := r.ConfigurationAdmissionBinding.Validate(); err != nil {
		return err
	}
	roles := []string{managedprotocol.NetworkManagementGlobalReadClusterRole}
	leaseNamespace := r.ConfigurationAdmissionBinding.LeaseNamespace
	if leaseNamespace != "" && leaseNamespace != nc.Namespace {
		roles = append(roles, managedprotocol.NetworkManagementLeaseReadOnlyClusterRole)
	}
	for _, name := range roles {
		var role rbac.ClusterRole
		if err := r.APIReader.Get(ctx, client.ObjectKey{Name: name}, &role); err != nil {
			return err
		}
		if role.DeletionTimestamp != nil || managedprotocol.ValidateWorkerClusterRole(&role) != nil {
			return fmt.Errorf("configuration observation role differs from compiled contract")
		}
	}
	name := configurationAccessName(nc)
	subjects := []rbac.Subject{{Kind: rbac.ServiceAccountKind, Namespace: nc.Namespace, Name: networkControllerWorkerName(nc.Name)}}
	metadata := metav1.ObjectMeta{Name: name, Labels: map[string]string{configurationAccessControllerUID: string(nc.UID)}, Annotations: map[string]string{configurationAccessControllerNamespace: nc.Namespace, networkControllerNameAnnotation: nc.Name, networkControllerUIDAnnotation: string(nc.UID)}}
	global := &rbac.ClusterRoleBinding{ObjectMeta: metadata, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: roles[0]}, Subjects: subjects}
	if err := r.ensureConfigurationReadBinding(ctx, nc, global); err != nil {
		return err
	}
	if len(roles) == 2 {
		metadata = *metadata.DeepCopy()
		metadata.Namespace = leaseNamespace
		lease := &rbac.RoleBinding{ObjectMeta: metadata, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: roles[1]}, Subjects: subjects}
		if err := r.ensureConfigurationReadBinding(ctx, nc, lease); err != nil {
			return err
		}
	}
	return nil
}
func (r *NetworkControllerReconciler) ensureConfigurationReadBinding(ctx context.Context, nc *cvk.NetworkController, desired client.Object) error {
	current := desired.DeepCopyObject().(client.Object)
	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(desired), current)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if !configurationAccessOwned(current, nc) || current.GetDeletionTimestamp() != nil {
		return fmt.Errorf("configuration observation binding collision")
	}
	switch got := current.(type) {
	case *rbac.ClusterRoleBinding:
		want := desired.(*rbac.ClusterRoleBinding)
		if got.RoleRef != want.RoleRef || !reflect.DeepEqual(got.Subjects, want.Subjects) {
			return fmt.Errorf("configuration global observation grant differs")
		}
	case *rbac.RoleBinding:
		want := desired.(*rbac.RoleBinding)
		if got.RoleRef != want.RoleRef || !reflect.DeepEqual(got.Subjects, want.Subjects) {
			return fmt.Errorf("configuration Lease observation grant differs")
		}
	default:
		return fmt.Errorf("unsupported configuration observation grant")
	}
	return nil
}

// Called only after foreground worker deletion has completed. List across
// namespaces to retire previous custom Lease-namespace bindings after a policy
// change. UID-scoped names and metadata prevent adopting another controller.
func (r *NetworkControllerReconciler) retireConfigurationReadAccess(ctx context.Context, nc *cvk.NetworkController) (bool, error) {
	if !r.ManageConfigurationReservations {
		return false, nil
	}
	if r.APIReader == nil {
		return true, fmt.Errorf("configuration access retirement requires uncached reads")
	}
	selector := client.MatchingLabels{configurationAccessControllerUID: string(nc.UID)}
	var globals rbac.ClusterRoleBindingList
	var leases rbac.RoleBindingList
	if err := r.APIReader.List(ctx, &globals, selector); err != nil {
		return true, err
	}
	if err := r.APIReader.List(ctx, &leases, selector); err != nil {
		return true, err
	}
	objects := []client.Object{}
	for i := range globals.Items {
		objects = append(objects, &globals.Items[i])
	}
	for i := range leases.Items {
		objects = append(objects, &leases.Items[i])
	}
	for _, object := range objects {
		if !configurationAccessOwned(object, nc) {
			return true, fmt.Errorf("configuration read access has ambiguous ownership")
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, object); err != nil && !apierrors.IsNotFound(err) {
			return true, err
		}
	}
	return len(objects) != 0, nil
}
