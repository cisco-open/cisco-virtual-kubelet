// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configengine "github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

const (
	managedNodeHeartbeatFamily = "node-heartbeat"
	managedNodeLeaseDuration   = int32(40)
)

// ensureManagedWorkerLeases creates every Lease identity a managed worker may
// update before that worker receives credentials. Create and delete are absent
// from managed-worker RBAC; admission binds every later update to the exact
// worker username and CiscoDevice/Node incarnation recorded here.
func (r *CiscoDeviceReconciler) ensureManagedWorkerLeases(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
) error {
	if err := r.ensureManagedMutationLease(ctx, device, node); err != nil {
		return err
	}
	if err := r.ensureManagedNodeHeartbeatLease(ctx, device, node); err != nil {
		return err
	}
	families, err := drivers.ConfigLeaseFamilies(device.Spec.Driver)
	if err != nil {
		return err
	}
	for _, family := range families {
		if family == managedNodeHeartbeatFamily || family == devicecoordination.MutationLeaseFamily {
			return fmt.Errorf("config driver %q declared reserved Lease family %q", device.Spec.Driver, family)
		}
		if err := r.ensureManagedConfigLease(ctx, device, node, family); err != nil {
			return err
		}
	}
	return nil
}

func managedLeaseBindingAnnotations(
	device *ciskov1.CiscoDevice,
	nodeName, nodeUID, worker, purpose string,
) map[string]string {
	return map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
		managedprotocol.AnnotationNodeName:        nodeName,
		managedprotocol.AnnotationNodeUID:         nodeUID,
		managedprotocol.AnnotationWorkerUsername:  worker,
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		managedprotocol.AnnotationLeasePurpose:    purpose,
		devicecoordination.RetainLeaseAnnotation:  "true",
	}
}

func managedLeaseLabels(deviceKey, family string) map[string]string {
	return map[string]string{
		"cisco.vk/device": deviceKey,
		"cisco.vk/family": family,
	}
}

func (r *CiscoDeviceReconciler) ensureManagedNodeHeartbeatLease(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
) error {
	worker := node.Annotations[managedprotocol.AnnotationWorkerUsername]
	if device.UID == "" || node.UID == "" || worker == "" {
		return fmt.Errorf("managed Node identity/worker binding is incomplete before heartbeat Lease creation")
	}
	desired := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: corev1.NamespaceNodeLease,
			Name:      node.Name,
			Annotations: managedLeaseBindingAnnotations(
				device, node.Name, string(node.UID), worker, managedprotocol.LeasePurposeNodeHeartbeat,
			),
			Labels: managedLeaseLabels(devicecoordination.DeviceKey(device.Namespace, device.Name), managedNodeHeartbeatFamily),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Node", Name: node.Name, UID: node.UID,
			}},
		},
		// Upstream virtual-kubelet preserves an existing holder/duration and
		// supplies renewTime on its first update. Seed the immutable identity
		// without falsely publishing a manager-authored heartbeat.
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       ptr.To(node.Name),
			LeaseDurationSeconds: ptr.To(managedNodeLeaseDuration),
		},
	}
	return r.ensureManagedBoundLease(ctx, desired, validateLegacyManagedHeartbeatLease)
}

func (r *CiscoDeviceReconciler) ensureManagedConfigLease(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	family string,
) error {
	namespace := r.LeaseNamespace
	if namespace == "" {
		namespace = device.Namespace
	}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	worker := node.Annotations[managedprotocol.AnnotationWorkerUsername]
	desired := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace,
		Name:      configengine.LeaseName(deviceKey, family),
		Annotations: managedLeaseBindingAnnotations(
			device, node.Name, string(node.UID), worker, managedprotocol.LeasePurposeConfigFamily,
		),
		Labels: managedLeaseLabels(deviceKey, family),
	}}
	if device.UID == "" || node.UID == "" || worker == "" {
		return fmt.Errorf("managed Node identity/worker binding is incomplete before config Lease creation")
	}
	if err := r.ensureManagedBoundLease(ctx, desired, validateLegacyManagedConfigLease); err != nil {
		return fmt.Errorf("ensure managed config Lease for family %q: %w", family, err)
	}
	return nil
}

type legacyManagedLeaseValidator func(existing, desired *coordv1.Lease) error

func (r *CiscoDeviceReconciler) ensureManagedBoundLease(
	ctx context.Context,
	desired *coordv1.Lease,
	validateLegacy legacyManagedLeaseValidator,
) error {
	if err := r.Create(ctx, desired); err == nil {
		return validateManagedBoundLeaseMetadata(desired, desired.Annotations, desired.Labels, desired.OwnerReferences)
	} else if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("pre-create managed Lease %s/%s: %w", desired.Namespace, desired.Name, err)
	}

	var existing coordv1.Lease
	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	if err := r.reader().Get(ctx, key, &existing); err != nil {
		return fmt.Errorf("read managed Lease %s: %w", key, err)
	}
	if existing.Annotations[managedprotocol.AnnotationManaged] == "true" {
		if err := validateManagedBoundLeaseMetadata(&existing, desired.Annotations, desired.Labels, desired.OwnerReferences); err != nil {
			return fmt.Errorf("managed Lease %s is unsafe to use: %w", key, err)
		}
		return nil
	}
	if err := validateLegacy(&existing, desired); err != nil {
		return fmt.Errorf("legacy Lease %s cannot be adopted: %w", key, err)
	}
	before := existing.DeepCopy()
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	for annotation, value := range desired.Annotations {
		existing.Annotations[annotation] = value
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	for label, value := range desired.Labels {
		existing.Labels[label] = value
	}
	existing.OwnerReferences = append([]metav1.OwnerReference(nil), desired.OwnerReferences...)
	// A legacy VK may have created the canonical heartbeat Lease without ever
	// publishing its first heartbeat. Adopt that empty object and seed the
	// immutable holder/duration in the same optimistic-lock patch as its Node
	// owner and worker binding. Otherwise the status-only worker's first
	// empty->held update would be rejected by admission, leaving the Node
	// permanently uninitialized.
	if desired.Annotations[managedprotocol.AnnotationLeasePurpose] == managedprotocol.LeasePurposeNodeHeartbeat &&
		existing.Spec.HolderIdentity == nil {
		existing.Spec.HolderIdentity = ptr.To(desired.Name)
		existing.Spec.LeaseDurationSeconds = ptr.To(managedNodeLeaseDuration)
	}
	if err := r.Patch(ctx, &existing, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("adopt managed Lease %s: %w", key, err)
	}
	if err := validateManagedBoundLeaseMetadata(&existing, desired.Annotations, desired.Labels, desired.OwnerReferences); err != nil {
		return fmt.Errorf("adopted managed Lease %s is unsafe to use: %w", key, err)
	}
	return nil
}

func validateManagedBoundLeaseMetadata(
	lease *coordv1.Lease,
	expectedAnnotations, expectedLabels map[string]string,
	expectedOwners []metav1.OwnerReference,
) error {
	if lease == nil || lease.UID == "" {
		return fmt.Errorf("persistent UID is missing")
	}
	if !lease.DeletionTimestamp.IsZero() || len(lease.Finalizers) != 0 {
		return fmt.Errorf("deletion/finalizer metadata is present")
	}
	if !reflect.DeepEqual(lease.OwnerReferences, expectedOwners) {
		return fmt.Errorf("ownerReferences do not match the bound Lease purpose")
	}
	for annotation, expected := range expectedAnnotations {
		if actual := lease.Annotations[annotation]; actual != expected {
			return fmt.Errorf("annotation %s=%q, want %q", annotation, actual, expected)
		}
	}
	for label, expected := range expectedLabels {
		if actual := lease.Labels[label]; actual != expected {
			return fmt.Errorf("label %s=%q, want %q", label, actual, expected)
		}
	}
	return nil
}

func validateLegacyManagedLeaseBindings(existing, desired *coordv1.Lease) error {
	if existing == nil || existing.UID == "" {
		return fmt.Errorf("persistent UID is missing")
	}
	if !existing.DeletionTimestamp.IsZero() || len(existing.Finalizers) != 0 {
		return fmt.Errorf("deletion/finalizer metadata is present")
	}
	for annotation, expected := range desired.Annotations {
		if actual, present := existing.Annotations[annotation]; present && actual != expected {
			return fmt.Errorf("annotation %s is already bound to %q", annotation, actual)
		}
	}
	for label, expected := range desired.Labels {
		if actual, present := existing.Labels[label]; present && actual != expected {
			return fmt.Errorf("label %s is already bound to %q", label, actual)
		}
	}
	for _, annotation := range []string{
		managedprotocol.AnnotationMaintenanceRequestVersion,
		managedprotocol.AnnotationMaintenanceSessionToken,
		managedprotocol.AnnotationMaintenanceRequestedAt,
		managedprotocol.AnnotationMaintenanceOperationNS,
		managedprotocol.AnnotationMaintenanceOperationName,
		managedprotocol.AnnotationMaintenanceOperationUID,
		managedprotocol.AnnotationMaintenanceControlRevision,
	} {
		if _, present := existing.Annotations[annotation]; present {
			return fmt.Errorf("maintenance request annotation %s is present", annotation)
		}
	}
	return nil
}

func validateLegacyManagedConfigLease(existing, desired *coordv1.Lease) error {
	if err := validateLegacyManagedLeaseBindings(existing, desired); err != nil {
		return err
	}
	if len(existing.OwnerReferences) != 0 {
		return fmt.Errorf("ownerReferences are present")
	}
	return validateManagedConfigLeaseSpec(&existing.Spec)
}

func validateManagedConfigLeaseSpec(spec *coordv1.LeaseSpec) error {
	if spec == nil || spec.Strategy != nil || spec.PreferredHolder != nil {
		return fmt.Errorf("unsupported Lease strategy metadata is present")
	}
	held := spec.HolderIdentity != nil && strings.TrimSpace(*spec.HolderIdentity) != ""
	if !held {
		if spec.LeaseDurationSeconds != nil || spec.AcquireTime != nil || spec.RenewTime != nil || spec.LeaseTransitions != nil {
			return fmt.Errorf("idle Lease retains holder timing metadata")
		}
		return nil
	}
	if strings.TrimSpace(*spec.HolderIdentity) != *spec.HolderIdentity || len(*spec.HolderIdentity) > 512 ||
		strings.ContainsAny(*spec.HolderIdentity, "\r\n\x00") {
		return fmt.Errorf("holderIdentity is malformed")
	}
	if spec.LeaseDurationSeconds == nil || *spec.LeaseDurationSeconds <= 0 || *spec.LeaseDurationSeconds > 3600 ||
		spec.AcquireTime == nil || spec.RenewTime == nil || spec.AcquireTime.After(spec.RenewTime.Time) ||
		spec.LeaseTransitions == nil || *spec.LeaseTransitions <= 0 {
		return fmt.Errorf("held Lease timing/transition metadata is malformed")
	}
	return nil
}

func validateLegacyManagedHeartbeatLease(existing, desired *coordv1.Lease) error {
	if err := validateLegacyManagedLeaseBindings(existing, desired); err != nil {
		return err
	}
	if len(existing.OwnerReferences) == 0 {
		if existing.Spec.HolderIdentity != nil || existing.Spec.LeaseDurationSeconds != nil || existing.Spec.RenewTime != nil {
			return fmt.Errorf("active heartbeat Lease has no exact Node owner")
		}
	} else if !reflect.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
		return fmt.Errorf("heartbeat Lease owner does not match the bound Node UID")
	}
	if existing.Spec.Strategy != nil || existing.Spec.PreferredHolder != nil || existing.Spec.AcquireTime != nil || existing.Spec.LeaseTransitions != nil {
		return fmt.Errorf("heartbeat Lease contains unsupported spec fields")
	}
	if existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity != desired.Name {
		return fmt.Errorf("heartbeat holderIdentity does not match the bound Node")
	}
	if existing.Spec.LeaseDurationSeconds != nil && *existing.Spec.LeaseDurationSeconds != managedNodeLeaseDuration {
		return fmt.Errorf("heartbeat leaseDurationSeconds is not %d", managedNodeLeaseDuration)
	}
	return nil
}

// cleanupManagedWorkerLeases removes only exact, UID-bound Lease identities
// after the worker's RBAC bindings have been revoked. Listing by the
// namespace-aware device key also finds retained objects if an administrator
// moved CONFIG_LEASE_NAMESPACE between controller releases.
func (r *CiscoDeviceReconciler) cleanupManagedWorkerLeases(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) error {
	bound := device.Status.NodeIdentity
	if bound == nil {
		return nil
	}
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: bound.NodeName, UID: types.UID(bound.NodeUID)}}
	if err := r.reader().Get(ctx, types.NamespacedName{Name: bound.NodeName}, &node); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("read bound Node before managed Lease cleanup: %w", err)
	}
	if node.UID == "" || string(node.UID) != bound.NodeUID ||
		(node.ResourceVersion != "" && !managedNodeMatchesDevice(&node, device)) {
		return fmt.Errorf("bound Node identity changed before managed Lease cleanup")
	}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	worker := "system:serviceaccount:" + device.Namespace + ":" + managedWorkerServiceAccountName(device)
	var leases coordv1.LeaseList
	if err := r.reader().List(ctx, &leases, client.MatchingLabels{"cisco.vk/device": deviceKey}); err != nil {
		return fmt.Errorf("list managed worker Leases before cleanup: %w", err)
	}
	candidates := make([]coordv1.Lease, 0, len(leases.Items))
	for i := range leases.Items {
		lease := &leases.Items[i]
		if lease.Annotations[managedprotocol.AnnotationManaged] != "true" ||
			lease.Annotations[managedprotocol.AnnotationDeviceUID] != string(device.UID) {
			continue
		}
		family := lease.Labels["cisco.vk/family"]
		purpose := lease.Annotations[managedprotocol.AnnotationLeasePurpose]
		expectedAnnotations := managedLeaseBindingAnnotations(device, node.Name, string(node.UID), worker, purpose)
		expectedLabels := managedLeaseLabels(deviceKey, family)
		var expectedOwners []metav1.OwnerReference
		switch purpose {
		case managedprotocol.LeasePurposeNodeHeartbeat:
			if lease.Namespace != corev1.NamespaceNodeLease || lease.Name != node.Name || family != managedNodeHeartbeatFamily {
				return fmt.Errorf("managed heartbeat Lease %s/%s has a noncanonical identity", lease.Namespace, lease.Name)
			}
			expectedOwners = []metav1.OwnerReference{{
				APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Node", Name: node.Name, UID: node.UID,
			}}
		case managedprotocol.LeasePurposeDeviceMutation:
			if family != devicecoordination.MutationLeaseFamily ||
				lease.Name != configengine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily) {
				return fmt.Errorf("managed mutation Lease %s/%s has a noncanonical identity", lease.Namespace, lease.Name)
			}
		case managedprotocol.LeasePurposeConfigFamily:
			if family == "" || family == managedNodeHeartbeatFamily || family == devicecoordination.MutationLeaseFamily ||
				lease.Name != configengine.LeaseName(deviceKey, family) {
				return fmt.Errorf("managed config Lease %s/%s has a noncanonical identity", lease.Namespace, lease.Name)
			}
		default:
			return fmt.Errorf("managed Lease %s/%s has unknown purpose %q", lease.Namespace, lease.Name, purpose)
		}
		if err := validateManagedBoundLeaseMetadata(lease, expectedAnnotations, expectedLabels, expectedOwners); err != nil {
			return fmt.Errorf("refuse unsafe managed Lease cleanup for %s/%s: %w", lease.Namespace, lease.Name, err)
		}
		if lease.ResourceVersion == "" {
			return fmt.Errorf("managed Lease %s/%s has no persistent resourceVersion", lease.Namespace, lease.Name)
		}
		candidates = append(candidates, *lease.DeepCopy())
	}
	// Validate the complete set before deleting any member, then remove the
	// mutation fence last. This minimizes partial cleanup and preserves the
	// strongest proof for as long as possible. A subsequent retry remains safe
	// because the worker bindings were revoked before this function is called.
	slices.SortFunc(candidates, func(a, b coordv1.Lease) int {
		aMutation := a.Annotations[managedprotocol.AnnotationLeasePurpose] == managedprotocol.LeasePurposeDeviceMutation
		bMutation := b.Annotations[managedprotocol.AnnotationLeasePurpose] == managedprotocol.LeasePurposeDeviceMutation
		if aMutation != bMutation {
			if aMutation {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Namespace+"\x00"+a.Name, b.Namespace+"\x00"+b.Name)
	})
	for i := range candidates {
		lease := &candidates[i]
		uid, resourceVersion := lease.UID, lease.ResourceVersion
		if err := r.Delete(ctx, lease, &client.DeleteOptions{Preconditions: &metav1.Preconditions{
			UID: &uid, ResourceVersion: &resourceVersion,
		}}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete managed Lease %s/%s: %w", lease.Namespace, lease.Name, err)
		}
	}
	return nil
}
