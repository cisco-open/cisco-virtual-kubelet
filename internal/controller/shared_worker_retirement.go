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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	ctrlmanager "sigs.k8s.io/controller-runtime/pkg/manager"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	sharedWorkerRetirementAnnotation = "topology.cisco.vk/retire-shared-worker-access"
	sharedWorkerRetirementVersion    = "rollout-v1"
	sharedWorkerRetirementInterval   = 5 * time.Second
)

type sharedWorkerRetirementRunnable struct {
	reconciler *CiscoDeviceReconciler
}

func (r *sharedWorkerRetirementRunnable) NeedLeaderElection() bool { return true }

func (r *sharedWorkerRetirementRunnable) Start(ctx context.Context) error {
	ticker := time.NewTicker(sharedWorkerRetirementInterval)
	defer ticker.Stop()
	for {
		retired, err := r.reconciler.retireSharedWorkerAccessIfSafe(ctx)
		if err != nil {
			log.FromContext(ctx).Error(err, "legacy shared-worker retirement is blocked")
		} else if retired {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

var _ ctrlmanager.Runnable = (*sharedWorkerRetirementRunnable)(nil)
var _ ctrlmanager.LeaderElectionRunnable = (*sharedWorkerRetirementRunnable)(nil)

// retirePriorTopologyWorkerAccessIfSafe completes the second identity handoff:
// an unselected topology-legacy worker has the broad compatibility role,
// while its selected managed replacement receives the narrow status-only
// role. Recreate changes the Deployment first; only after every old Pod/RS is
// gone may the legacy binding and SA be revoked.
func (r *CiscoDeviceReconciler) retirePriorTopologyWorkerAccessIfSafe(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	currentSA string,
) (bool, error) {
	if !r.ManagedTopology || device.Status.NodeIdentity == nil || currentSA != managedWorkerServiceAccountName(device) {
		return true, nil
	}
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Deployments before topology-legacy identity retirement: %w", err)
	}
	deploymentsByUID := make(map[types.UID]*appsv1.Deployment, len(deployments.Items))
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.UID != "" {
			deploymentsByUID[deployment.UID] = deployment
		}
		if deployment.Spec.Template.Spec.ServiceAccountName == legacySA {
			return false, nil
		}
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Pods before topology-legacy identity retirement: %w", err)
	}
	for i := range pods.Items {
		if pods.Items[i].Spec.ServiceAccountName == legacySA {
			return false, nil
		}
	}
	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list ReplicaSets before topology-legacy identity retirement: %w", err)
	}
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if replicaSet.Spec.Template.Spec.ServiceAccountName != legacySA {
			continue
		}
		if replicaSet.Spec.Replicas == nil || *replicaSet.Spec.Replicas != 0 || replicaSet.Status.Replicas != 0 {
			return false, nil
		}
		owner := metav1.GetControllerOf(replicaSet)
		deployment := deploymentsByUID[ownerUID(owner)]
		if owner == nil || deployment == nil || owner.Name != deployment.Name || !controllerOwnedPerDeviceDeployment(deployment) {
			return false, fmt.Errorf("drained topology-legacy ReplicaSet %s/%s has no exact live CiscoDevice Deployment owner", replicaSet.Namespace, replicaSet.Name)
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, replicaSet); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete drained topology-legacy ReplicaSet %s/%s: %w", replicaSet.Namespace, replicaSet.Name, err)
		}
		return false, nil
	}
	if err := r.cleanupGeneratedWorkerAccess(ctx, device, legacySA, false); err != nil {
		return false, fmt.Errorf("retire topology-legacy worker identity: %w", err)
	}
	return true, nil
}

// sharedWorkerAuthorityPresent reports whether the former shared worker can
// still receive any authority through a RoleBinding or ClusterRoleBinding.
// Managed admission is deliberately deferred while this is true: migrating
// every worker to an incarnation-bound legacy identity first avoids a window
// in which an old shared token can bypass the per-node admission boundary.
func (r *CiscoDeviceReconciler) sharedWorkerAuthorityPresent(ctx context.Context) (bool, error) {
	sharedSA := r.vkServiceAccountName()
	inventory, err := r.inspectLegacySharedWorker(ctx, sharedSA)
	if err != nil {
		return false, err
	}
	return inventory.authorityPresent(), nil
}

// legacySharedWorkerInventory distinguishes a Kubernetes ServiceAccount
// identity (namespace/name) from a merely equal name elsewhere in the
// cluster. A namespace is admitted to the inventory only through durable CVK
// provenance: an exact legacy binding, the chart-owned ServiceAccount, or a
// CiscoDevice-owned worker Deployment that still names the shared account.
// Once admitted, every additive grant to that exact identity is considered so
// suspicious RBAC cannot be hidden beside a canonical binding.
type legacySharedWorkerInventory struct {
	sharedSA            string
	identities          map[types.NamespacedName]struct{}
	deployments         []appsv1.Deployment
	roleBindings        []rbacv1.RoleBinding
	clusterRoleBindings []rbacv1.ClusterRoleBinding
}

func (r *CiscoDeviceReconciler) inspectLegacySharedWorker(
	ctx context.Context,
	sharedSA string,
) (*legacySharedWorkerInventory, error) {
	inventory := &legacySharedWorkerInventory{
		sharedSA:   sharedSA,
		identities: make(map[types.NamespacedName]struct{}),
	}

	var serviceAccounts corev1.ServiceAccountList
	if err := r.reader().List(ctx, &serviceAccounts); err != nil {
		return nil, fmt.Errorf("list ServiceAccounts for legacy shared-worker inventory: %w", err)
	}
	for i := range serviceAccounts.Items {
		sa := &serviceAccounts.Items[i]
		if sa.Name == sharedSA && chartOwnedSharedWorkerServiceAccount(sa) {
			inventory.addIdentity(sa.Namespace)
		}
	}

	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments); err != nil {
		return nil, fmt.Errorf("list Deployments for legacy shared-worker inventory: %w", err)
	}
	inventory.deployments = deployments.Items
	for i := range inventory.deployments {
		deployment := &inventory.deployments[i]
		if deployment.Spec.Template.Spec.ServiceAccountName == sharedSA && controllerOwnedPerDeviceDeployment(deployment) {
			inventory.addIdentity(deployment.Namespace)
		}
	}

	var roleBindings rbacv1.RoleBindingList
	if err := r.reader().List(ctx, &roleBindings); err != nil {
		return nil, fmt.Errorf("list legacy shared-worker RoleBindings: %w", err)
	}
	inventory.roleBindings = roleBindings.Items
	for i := range inventory.roleBindings {
		binding := &inventory.roleBindings[i]
		for _, namespace := range legacyRoleBindingIdentityNamespaces(binding, sharedSA) {
			inventory.addIdentity(namespace)
		}
	}

	var clusterRoleBindings rbacv1.ClusterRoleBindingList
	if err := r.reader().List(ctx, &clusterRoleBindings); err != nil {
		return nil, fmt.Errorf("list legacy shared-worker ClusterRoleBindings: %w", err)
	}
	inventory.clusterRoleBindings = clusterRoleBindings.Items
	for i := range inventory.clusterRoleBindings {
		binding := &inventory.clusterRoleBindings[i]
		for _, namespace := range legacyClusterRoleBindingIdentityNamespaces(binding, sharedSA) {
			inventory.addIdentity(namespace)
		}
	}
	return inventory, nil
}

func (i *legacySharedWorkerInventory) addIdentity(namespace string) {
	if namespace != "" {
		i.identities[types.NamespacedName{Namespace: namespace, Name: i.sharedSA}] = struct{}{}
	}
}

func (i *legacySharedWorkerInventory) contains(namespace string) bool {
	_, found := i.identities[types.NamespacedName{Namespace: namespace, Name: i.sharedSA}]
	return found
}

func (i *legacySharedWorkerInventory) authorityPresent() bool {
	for j := range i.roleBindings {
		if len(sharedWorkerSubjectsForIdentities(i.roleBindings[j].Subjects, i.identities)) != 0 {
			return true
		}
	}
	for j := range i.clusterRoleBindings {
		if len(sharedWorkerSubjectsForIdentities(i.clusterRoleBindings[j].Subjects, i.identities)) != 0 {
			return true
		}
	}
	return false
}

func chartOwnedSharedWorkerServiceAccount(sa *corev1.ServiceAccount) bool {
	if sa == nil {
		return false
	}
	return strings.HasPrefix(sa.Labels["helm.sh/chart"], "cisco-virtual-kubelet-") &&
		strings.EqualFold(sa.Labels["app.kubernetes.io/managed-by"], "helm")
}

func legacyRoleBindingIdentityNamespaces(binding *rbacv1.RoleBinding, sharedSA string) []string {
	if binding == nil {
		return nil
	}
	dynamic := binding.Name == sharedSA &&
		binding.RoleRef == (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole})
	chartBridge := binding.Name == sharedSA+"-device" &&
		binding.Annotations[sharedWorkerRetirementAnnotation] == sharedWorkerRetirementVersion
	if !dynamic && !chartBridge {
		return nil
	}
	return sharedServiceAccountSubjectNamespaces(binding.Subjects, sharedSA)
}

func legacyClusterRoleBindingIdentityNamespaces(binding *rbacv1.ClusterRoleBinding, sharedSA string) []string {
	if binding == nil {
		return nil
	}
	chartBridge := binding.Name == sharedSA &&
		binding.Annotations[sharedWorkerRetirementAnnotation] == sharedWorkerRetirementVersion
	if chartBridge {
		return sharedServiceAccountSubjectNamespaces(binding.Subjects, sharedSA)
	}
	if binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole}) {
		return nil
	}
	for _, subject := range binding.Subjects {
		if subject.Kind == rbacv1.ServiceAccountKind && subject.Name == sharedSA && subject.Namespace != "" &&
			binding.Name == vkAccessClusterRoleBindingName(subject.Namespace, sharedSA) {
			// The deterministic name authenticates the namespace encoded by the
			// old controller. Return every same-named SA subject so an additive
			// subject makes the binding fail closed during validation.
			return sharedServiceAccountSubjectNamespaces(binding.Subjects, sharedSA)
		}
	}
	return nil
}

func sharedServiceAccountSubjectNamespaces(subjects []rbacv1.Subject, sharedSA string) []string {
	namespaces := make([]string, 0, 1)
	seen := make(map[string]struct{}, 1)
	for _, subject := range subjects {
		if subject.Kind != rbacv1.ServiceAccountKind || subject.Name != sharedSA || subject.Namespace == "" {
			continue
		}
		if _, found := seen[subject.Namespace]; found {
			continue
		}
		seen[subject.Namespace] = struct{}{}
		namespaces = append(namespaces, subject.Namespace)
	}
	return namespaces
}

func exactServiceAccountSubject(subject rbacv1.Subject, identity types.NamespacedName) bool {
	return subject.APIGroup == "" && subject.Kind == rbacv1.ServiceAccountKind &&
		subject.Name == identity.Name && subject.Namespace == identity.Namespace
}

// retireSharedWorkerAccessIfSafe removes the broad pre-topology bindings only
// after a cluster-wide proof that no Deployment, ReplicaSet, or Pod still uses
// the shared identity. It recognizes both the chart-seeded release bindings
// and the old controller's per-namespace dynamic bindings. Any other additive
// grant fails closed and is preserved for administrator review.
func (r *CiscoDeviceReconciler) retireSharedWorkerAccessIfSafe(ctx context.Context) (bool, error) {
	if !r.ManagedTopology {
		return true, nil
	}
	sharedSA := r.vkServiceAccountName()
	inventory, err := r.inspectLegacySharedWorker(ctx, sharedSA)
	if err != nil {
		return false, err
	}

	// Only exact CiscoDevice-owned generations participate in the handoff.
	// Another application may legitimately use the configured account name in
	// another namespace (or even own an unrelated workload in the chart
	// namespace); neither case is a CVK generation and must not stall or be
	// deleted by this migration.
	deploymentsByUID := make(map[types.UID]*appsv1.Deployment, len(inventory.deployments))
	for i := range inventory.deployments {
		deployment := &inventory.deployments[i]
		if deployment.UID != "" && controllerOwnedPerDeviceDeployment(deployment) {
			deploymentsByUID[deployment.UID] = deployment
		}
		if inventory.contains(deployment.Namespace) &&
			controllerOwnedPerDeviceDeployment(deployment) &&
			deployment.Spec.Template.Spec.ServiceAccountName == sharedSA {
			return false, nil
		}
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets); err != nil {
		return false, fmt.Errorf("list ReplicaSets before shared-worker retirement: %w", err)
	}
	replicaSetsByUID := make(map[types.UID]*appsv1.ReplicaSet, len(replicaSets.Items))
	replicaSetsWithLiveOwner := make(map[types.UID]struct{}, len(replicaSets.Items))
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		owner := metav1.GetControllerOf(replicaSet)
		deployment := deploymentsByUID[ownerUID(owner)]
		liveOwner := owner != nil && owner.APIVersion == appsv1.SchemeGroupVersion.String() && owner.Kind == "Deployment" &&
			deployment != nil && deployment.Namespace == replicaSet.Namespace && deployment.Name == owner.Name
		if !liveOwner && !recognizablePerDeviceReplicaSet(replicaSet) {
			continue
		}
		if replicaSet.UID != "" {
			replicaSetsByUID[replicaSet.UID] = replicaSet
			if liveOwner {
				replicaSetsWithLiveOwner[replicaSet.UID] = struct{}{}
			}
		}
	}

	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods); err != nil {
		return false, fmt.Errorf("list Pods before shared-worker retirement: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !inventory.contains(pod.Namespace) || pod.Spec.ServiceAccountName != sharedSA {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		replicaSet := replicaSetsByUID[ownerUID(owner)]
		if owner != nil && owner.APIVersion == appsv1.SchemeGroupVersion.String() && owner.Kind == "ReplicaSet" &&
			replicaSet != nil && replicaSet.Namespace == pod.Namespace && replicaSet.Name == owner.Name {
			return false, nil
		}
		if recognizablePerDevicePod(pod) {
			return false, nil
		}
	}

	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if !inventory.contains(replicaSet.Namespace) || replicaSet.Spec.Template.Spec.ServiceAccountName != sharedSA {
			continue
		}
		if _, recognized := replicaSetsByUID[replicaSet.UID]; !recognized && !recognizablePerDeviceReplicaSet(replicaSet) {
			continue
		}
		if replicaSet.Spec.Replicas == nil || *replicaSet.Spec.Replicas != 0 ||
			replicaSet.Status.Replicas != 0 || replicaSet.Status.ReadyReplicas != 0 || replicaSet.Status.AvailableReplicas != 0 {
			return false, nil
		}
		if _, liveOwner := replicaSetsWithLiveOwner[replicaSet.UID]; !liveOwner {
			return false, fmt.Errorf("drained shared-worker ReplicaSet %s/%s has no exact live CiscoDevice Deployment owner", replicaSet.Namespace, replicaSet.Name)
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, replicaSet); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete drained shared-worker ReplicaSet %s/%s: %w", replicaSet.Namespace, replicaSet.Name, err)
		}
		return false, nil
	}

	bindingsDeleted, err := r.deleteExactSharedWorkerBindings(ctx, inventory)
	if err != nil {
		return false, err
	}
	if bindingsDeleted {
		return false, nil
	}
	present, err := r.sharedWorkerAuthorityPresent(ctx)
	if err != nil {
		return false, err
	}
	return !present, nil
}

func ownerUID(owner *metav1.OwnerReference) types.UID {
	if owner == nil {
		return ""
	}
	return owner.UID
}

func controllerOwnedPerDeviceDeployment(deployment *appsv1.Deployment) bool {
	if deployment == nil {
		return false
	}
	owner := metav1.GetControllerOf(deployment)
	return owner != nil && owner.APIVersion == "cisco.vk/v1alpha1" && owner.Kind == "CiscoDevice" && owner.UID != "" &&
		deployment.Name == owner.Name+deploymentSuffix
}

func recognizablePerDeviceReplicaSet(replicaSet *appsv1.ReplicaSet) bool {
	if replicaSet == nil {
		return false
	}
	deviceName, labelsMatch := perDeviceWorkerLabelsMatch(replicaSet.Labels)
	owner := metav1.GetControllerOf(replicaSet)
	return labelsMatch && owner != nil && owner.APIVersion == appsv1.SchemeGroupVersion.String() &&
		owner.Kind == "Deployment" && owner.UID != "" && owner.Name == deviceName+deploymentSuffix
}

func recognizablePerDevicePod(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	_, labelsMatch := perDeviceWorkerLabelsMatch(pod.Labels)
	owner := metav1.GetControllerOf(pod)
	if !labelsMatch || owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() ||
		owner.Kind != "ReplicaSet" || owner.UID == "" {
		return false
	}
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == "cisco-vk" {
			return true
		}
	}
	return false
}

func perDeviceWorkerLabelsMatch(objectLabels map[string]string) (string, bool) {
	deviceName := objectLabels["app.kubernetes.io/instance"]
	return deviceName, deviceName != "" && objectLabels["app.kubernetes.io/name"] == "cisco-vk" &&
		objectLabels["app.kubernetes.io/managed-by"] == "ciscodevice-controller"
}

func (r *CiscoDeviceReconciler) deleteExactSharedWorkerBindings(
	ctx context.Context,
	inventory *legacySharedWorkerInventory,
) (bool, error) {
	// Validate the complete grant set before deleting anything. This keeps the
	// canonical bindings intact when an additive grant needs administrator
	// review and makes retry behavior independent of API list ordering.
	bindings := make([]client.Object, 0, len(inventory.roleBindings)+len(inventory.clusterRoleBindings))
	for i := range inventory.roleBindings {
		binding := &inventory.roleBindings[i]
		matchingSubjects := sharedWorkerSubjectsForIdentities(binding.Subjects, inventory.identities)
		if len(matchingSubjects) == 0 {
			continue
		}
		identity := matchingSubjects[0]
		if len(binding.Subjects) != 1 || len(matchingSubjects) != 1 ||
			!exactServiceAccountSubject(binding.Subjects[0], identity) || identity.Namespace != binding.Namespace ||
			binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole}) {
			return false, fmt.Errorf("unexpected RoleBinding %s/%s still grants the shared worker; refusing automatic retirement", binding.Namespace, binding.Name)
		}
		dynamic := binding.Name == inventory.sharedSA
		chartBridge := binding.Name == inventory.sharedSA+"-device" &&
			binding.Annotations[sharedWorkerRetirementAnnotation] == sharedWorkerRetirementVersion
		if !dynamic && !chartBridge {
			return false, fmt.Errorf("unrecognized RoleBinding %s/%s still grants the shared worker; refusing automatic retirement", binding.Namespace, binding.Name)
		}
		bindings = append(bindings, binding)
	}

	for i := range inventory.clusterRoleBindings {
		binding := &inventory.clusterRoleBindings[i]
		matchingSubjects := sharedWorkerSubjectsForIdentities(binding.Subjects, inventory.identities)
		if len(matchingSubjects) == 0 {
			continue
		}
		identity := matchingSubjects[0]
		if len(binding.Subjects) != 1 || len(matchingSubjects) != 1 ||
			!exactServiceAccountSubject(binding.Subjects[0], identity) ||
			binding.RoleRef != (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole}) {
			return false, fmt.Errorf("unexpected ClusterRoleBinding %s still grants the shared worker; refusing automatic retirement", binding.Name)
		}
		dynamic := binding.Name == vkAccessClusterRoleBindingName(identity.Namespace, inventory.sharedSA)
		chartBridge := binding.Name == inventory.sharedSA &&
			binding.Annotations[sharedWorkerRetirementAnnotation] == sharedWorkerRetirementVersion
		if !dynamic && !chartBridge {
			return false, fmt.Errorf("unrecognized ClusterRoleBinding %s still grants the shared worker; refusing automatic retirement", binding.Name)
		}
		bindings = append(bindings, binding)
	}

	for _, binding := range bindings {
		if err := deleteWithUIDPrecondition(ctx, r.Client, binding); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete retired shared-worker %T %s: %w", binding, client.ObjectKeyFromObject(binding), err)
		}
	}
	return len(bindings) != 0, nil
}

func sharedWorkerSubjectsForIdentities(
	subjects []rbacv1.Subject,
	identities map[types.NamespacedName]struct{},
) []types.NamespacedName {
	matching := make([]types.NamespacedName, 0, 1)
	for i := range subjects {
		subject := subjects[i]
		identity := types.NamespacedName{Namespace: subject.Namespace, Name: subject.Name}
		if subject.Kind == rbacv1.ServiceAccountKind && subject.Namespace != "" {
			if _, found := identities[identity]; found {
				matching = append(matching, identity)
			}
		}
	}
	return matching
}
