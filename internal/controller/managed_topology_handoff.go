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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	configprovider "github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
)

const (
	legacyHandoffRequeueInterval = 5 * time.Second
	legacyHandoffFutureSkew      = 30 * time.Second
)

// reconcileLegacyWriterHandoff transfers a Node from the managed workers to
// standalone compatibility mode. A UID-derived worker first proves that the
// released Node is healthy without ever overlapping the managed writer. It is
// then replaced, under a second Recreate barrier, by the pre-topology
// namespace-shared compatibility identity. The UID-derived identity is a
// bounded transition credential and is never retained as steady state.
func (r *CiscoDeviceReconciler) reconcileLegacyWriterHandoff(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	reason string,
) (managedTopologyResult, error) {
	bound := device.Status.NodeIdentity
	result := managedTopologyResult{Managed: true, NodeName: resolvedNodeName(device), RequeueAfter: legacyHandoffRequeueInterval}
	if bound == nil {
		return managedTopologyResult{}, fmt.Errorf("legacy writer handoff requires a managed Node identity")
	}
	result.NodeName = bound.NodeName
	if bound.DeviceUID != string(device.UID) || bound.NodeName == "" || bound.NodeUID == "" {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict,
			"LegacyHandoffIdentityInvalid",
			"managed Node identity is incomplete or belongs to a different CiscoDevice incarnation",
		)
	}
	projection := device.Status.TopologyProjection
	if projection == nil || projection.EffectiveLabelHash == "" {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict,
			"LegacyHandoffProjectionMissing",
			"managed topology projection is missing; repair managed ownership before requesting handoff",
		)
	}
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	legacyUsername := "system:serviceaccount:" + device.Namespace + ":" + legacySA
	handoff := device.Status.LegacyHandoff
	if handoff != nil {
		if err := validateLegacyHandoffStatus(device, handoff, projection.EffectiveLabelHash, legacyUsername); err != nil {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"LegacyHandoffStateInvalid", err.Error())
		}
	}
	if handoff == nil {
		request := strings.TrimSpace(device.Annotations[managedprotocol.AnnotationRequestLegacyHandoff])
		if request != bound.NodeUID {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"LegacyHandoffAuthorizationRequired",
				fmt.Sprintf("set topology-authorized annotation %s to the current status.nodeIdentity.nodeUID %q", managedprotocol.AnnotationRequestLegacyHandoff, bound.NodeUID),
			)
		}
	}

	switch {
	case handoff == nil:
		if err := r.legacyHandoffSafetyReady(ctx, device); err != nil {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"LegacyHandoffBlocked", err.Error())
		}
		// Provision broad-but-admission-confined legacy authority while the Node
		// is still managed. Native admission prevents this identity from writing
		// the Node until the manager completes the release transition.
		if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"LegacyHandoffAccessFailed", err.Error())
		}
		// Record the durable marker only after the complete isolated identity is
		// auditable. A crash can therefore leave recoverable access-without-marker,
		// never a marker that recovery would have to trust without RBAC evidence.
		if err := r.ensureIsolatedLegacyWorkerMarker(ctx, device); err != nil {
			return result, err
		}
		now := metav1.NewTime(r.now())
		if err := r.patchLegacyHandoffStatus(ctx, device, &ciskov1.DeviceLegacyHandoffStatus{
			Phase:                ciskov1.DeviceLegacyHandoffPreparing,
			DeviceUID:            string(device.UID),
			NodeName:             bound.NodeName,
			NodeUID:              bound.NodeUID,
			ProjectionHash:       projection.EffectiveLabelHash,
			LegacyWorkerUsername: legacyUsername,
			RequestedAt:          now,
		}, reason, "isolated legacy worker identity is provisioned; waiting for the managed worker to stop"); err != nil {
			return result, err
		}
		result.LegacyWorker = true
		return result, nil

	case handoff.Phase == ciskov1.DeviceLegacyHandoffPreparing:
		if err := r.legacyHandoffSafetyReady(ctx, device); err != nil {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"LegacyHandoffBlocked", err.Error())
		}
		if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
			return result, fmt.Errorf("reconcile isolated legacy worker access: %w", err)
		}
		result.LegacyWorker = true
		stopped, err := r.managedWriterWorkloadsStopped(ctx, device)
		if err != nil {
			return result, err
		}
		if !stopped {
			return result, nil
		}
		// Revoke the former identity before unmarking the Node. Even a retained
		// projected token then has no RBAC authority during the writer transition.
		if err := r.cleanupGeneratedWorkerAccess(ctx, device, managedWorkerServiceAccountName(device), true); err != nil {
			return result, fmt.Errorf("revoke managed worker access for legacy handoff: %w", err)
		}
		if err := r.cleanupManagedWorkerLeases(ctx, device); err != nil {
			return result, fmt.Errorf("remove managed worker Leases for legacy handoff: %w", err)
		}
		revoked, err := r.managedWorkerAccessRevoked(ctx, device)
		if err != nil {
			return result, err
		}
		if !revoked {
			return result, fmt.Errorf("managed worker API authority remains after revocation")
		}
		releasedAt := metav1.NewTime(r.now())
		next := copyLegacyHandoff(handoff)
		next.Phase = ciskov1.DeviceLegacyHandoffLegacyWriterPending
		next.NodeReleasedAt = &releasedAt
		if err := r.patchLegacyHandoffStatus(ctx, device, next, reason,
			"managed writer authority is revoked; waiting for a post-release legacy Node heartbeat"); err != nil {
			return result, err
		}
		handoff = device.Status.LegacyHandoff
		fallthrough

	case handoff.Phase == ciskov1.DeviceLegacyHandoffLegacyWriterPending:
		result.LegacyWorker = true
		if handoff.NodeReleasedAt == nil || handoff.NodeReleasedAt.IsZero() {
			return result, fmt.Errorf("legacy handoff release timestamp is absent")
		}
		if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
			return result, fmt.Errorf("reconcile isolated legacy worker access: %w", err)
		}
		revoked, err := r.managedWorkerAccessRevoked(ctx, device)
		if err != nil {
			return result, err
		}
		if !revoked {
			return result, fmt.Errorf("managed worker API authority was recreated during legacy handoff")
		}
		node, desiredLabels, err := r.releaseManagedNodeToLegacy(ctx, device, handoff)
		if err != nil {
			return result, err
		}
		ready, err := r.legacyWriterReady(ctx, device, node, desiredLabels, handoff.NodeReleasedAt.Time)
		if err != nil {
			return result, err
		}
		if !ready {
			return result, nil
		}
		isolatedReadyAt := metav1.NewTime(r.now())
		sharedPending := copyLegacyHandoff(handoff)
		sharedPending.Phase = ciskov1.DeviceLegacyHandoffSharedWriterPending
		sharedPending.IsolatedReadyAt = &isolatedReadyAt
		if err := r.beginSharedLegacyHandoffStatus(ctx, device, sharedPending, reason); err != nil {
			return result, err
		}
		if err := r.removeLegacyHandoffGuard(ctx, sharedPending); err != nil {
			return sharedPendingLegacyHandoffResult(device, legacySA, true), err
		}
		// A released reverse handoff no longer consumes either namespace-shared
		// functional identity. Retire them when this was the final managed
		// consumer; durable peers keep the accounts in place.
		if err := r.cleanupManagedSharedWorkerAccess(ctx, device); err != nil {
			return sharedPendingLegacyHandoffResult(device, legacySA, true), fmt.Errorf("retire unused shared worker access after legacy handoff: %w", err)
		}
		return r.reconcileCompletedLegacyHandoff(ctx, device)

	case handoff.Phase == ciskov1.DeviceLegacyHandoffSharedWriterPending:
		return r.reconcileCompletedLegacyHandoff(ctx, device)
	case handoff.Phase == ciskov1.DeviceLegacyHandoffComplete:
		return r.reconcileCompletedLegacyHandoff(ctx, device)
	default:
		return result, fmt.Errorf("unsupported legacy handoff phase %q", handoff.Phase)
	}
}

func (r *CiscoDeviceReconciler) completedLegacyHandoffResult(device *ciskov1.CiscoDevice) managedTopologyResult {
	if device == nil || device.Status.LegacyHandoff == nil ||
		device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
		return managedTopologyResult{}
	}
	return managedTopologyResult{
		LegacyWorker:         true,
		WorkerServiceAccount: r.vkServiceAccountName(),
		NodeName:             device.Status.LegacyHandoff.NodeName,
	}
}

func sharedPendingLegacyHandoffResult(device *ciskov1.CiscoDevice, serviceAccount string, hold bool) managedTopologyResult {
	result := managedTopologyResult{
		LegacyWorker:         true,
		WorkerServiceAccount: serviceAccount,
		HoldWorker:           hold,
		RequeueAfter:         legacyHandoffRequeueInterval,
	}
	if device != nil && device.Status.LegacyHandoff != nil {
		result.NodeName = device.Status.LegacyHandoff.NodeName
	}
	return result
}

// ensureIsolatedLegacyWorkerMarker preserves the Phase-0 shared-to-isolated
// worker migration across a later global topology disable, including for a
// device that never entered the managed fleet selector. The exact Device UID
// prevents a delete/recreate incarnation from inheriting this decision.
func (r *CiscoDeviceReconciler) ensureIsolatedLegacyWorkerMarker(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) error {
	if device == nil || device.UID == "" {
		return fmt.Errorf("cannot bind isolated legacy worker without a CiscoDevice UID")
	}
	value := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]
	if value != "" && value != string(device.UID) {
		return fmt.Errorf("isolated legacy worker marker belongs to a different CiscoDevice incarnation")
	}
	if value == string(device.UID) {
		return nil
	}
	before := device.DeepCopy()
	if device.Annotations == nil {
		device.Annotations = map[string]string{}
	}
	device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker] = string(device.UID)
	if err := r.Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("record isolated legacy worker identity: %w", err)
	}
	return nil
}

// recoverIsolatedLegacyWorker recognizes a UID-bound legacy identity created
// by an earlier topology-enabled manager even if global topology was disabled
// before the durable marker could be written. It never adopts an unowned or
// incompletely bound ServiceAccount.
func (r *CiscoDeviceReconciler) recoverIsolatedLegacyWorker(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (bool, error) {
	if device == nil || device.UID == "" {
		return false, nil
	}
	marker := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]
	if marker != "" && marker != string(device.UID) {
		return false, fmt.Errorf("isolated legacy worker marker belongs to a different CiscoDevice incarnation")
	}
	present, err := r.isolatedLegacyWorkerAccessPresent(ctx, device)
	if err != nil {
		return false, err
	}
	if !present {
		if marker != "" {
			return false, fmt.Errorf("isolated legacy worker marker has no exact owned ServiceAccount evidence")
		}
		return false, nil
	}
	if err := r.ensureIsolatedLegacyWorkerMarker(ctx, device); err != nil {
		return false, err
	}
	return true, nil
}

// isolatedLegacyWorkerAccessPresent distinguishes complete, exact transition
// authority from absence. A partial or drifted identity is never treated as
// recoverable absence because doing so could mint a second broad writer.
func (r *CiscoDeviceReconciler) isolatedLegacyWorkerAccessPresent(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (bool, error) {
	if device == nil || device.UID == "" || r.reader() == nil {
		return false, fmt.Errorf("cannot verify isolated legacy worker without an API reader and CiscoDevice UID")
	}
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	present := 0
	var serviceAccount corev1.ServiceAccount
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: legacySA}, &serviceAccount); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("inspect isolated legacy ServiceAccount: %w", err)
		}
	} else {
		present++
		if !managedServiceAccountOwnedByDevice(&serviceAccount, device) ||
			!workerAnnotationsMatch(serviceAccount.Annotations, workerServiceAccountAnnotations(device, false)) {
			return false, fmt.Errorf("existing isolated legacy ServiceAccount is not exactly bound to this CiscoDevice incarnation")
		}
	}
	var roleBinding rbacv1.RoleBinding
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: legacySA}, &roleBinding); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("inspect isolated legacy RoleBinding: %w", err)
		}
	} else {
		present++
	}
	var clusterRoleBinding rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, legacySA)}, &clusterRoleBinding); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("inspect isolated legacy ClusterRoleBinding: %w", err)
		}
	} else {
		present++
	}
	if present == 0 {
		return false, nil
	}
	if present != 3 {
		return false, fmt.Errorf("isolated legacy worker access is only partially present")
	}
	if err := r.auditGeneratedWorkerBindings(ctx, device, legacySA, vkSharedClusterRole); err != nil {
		return false, fmt.Errorf("audit recovered isolated legacy worker: %w", err)
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) verifySharedLegacyAccess(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) error {
	if device == nil || r.reader() == nil {
		return fmt.Errorf("cannot verify shared compatibility access without a CiscoDevice and API reader")
	}
	serviceAccount := r.vkServiceAccountName()
	identity := types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}
	var sa corev1.ServiceAccount
	if err := r.reader().Get(ctx, identity, &sa); err != nil {
		return fmt.Errorf("read shared compatibility ServiceAccount %s: %w", identity, err)
	}
	if !sa.DeletionTimestamp.IsZero() || metav1.GetControllerOf(&sa) != nil {
		return fmt.Errorf("shared compatibility ServiceAccount %s is deleting or unexpectedly controller-owned", identity)
	}
	var roleBinding rbacv1.RoleBinding
	if err := r.reader().Get(ctx, identity, &roleBinding); err != nil {
		return fmt.Errorf("read shared compatibility RoleBinding %s: %w", identity, err)
	}
	expectedDeviceRole := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkDeviceClusterRole}
	if !roleBinding.DeletionTimestamp.IsZero() || roleBinding.RoleRef != expectedDeviceRole ||
		len(roleBinding.Subjects) != 1 || !exactServiceAccountSubject(roleBinding.Subjects[0], identity) ||
		metav1.GetControllerOf(&roleBinding) != nil {
		return fmt.Errorf("shared compatibility RoleBinding %s is not the exact canonical binding", identity)
	}
	clusterBindingName := vkAccessClusterRoleBindingName(device.Namespace, serviceAccount)
	var clusterRoleBinding rbacv1.ClusterRoleBinding
	if err := r.reader().Get(ctx, types.NamespacedName{Name: clusterBindingName}, &clusterRoleBinding); err != nil {
		return fmt.Errorf("read shared compatibility ClusterRoleBinding %s: %w", clusterBindingName, err)
	}
	expectedWorkerRole := rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: vkSharedClusterRole}
	if !clusterRoleBinding.DeletionTimestamp.IsZero() || clusterRoleBinding.RoleRef != expectedWorkerRole ||
		len(clusterRoleBinding.Subjects) != 1 || !exactServiceAccountSubject(clusterRoleBinding.Subjects[0], identity) ||
		len(clusterRoleBinding.OwnerReferences) != 0 {
		return fmt.Errorf("shared compatibility ClusterRoleBinding %s is not the exact canonical binding", clusterBindingName)
	}
	return nil
}

// drainIsolatedLegacyWorker is the Recreate half of the isolated-to-shared
// transition. It deletes only the exact CiscoDevice-controlled Deployment and
// waits for every workload carrying the UID-scoped ServiceAccount to vanish;
// it never deletes an unproven dependent directly.
func (r *CiscoDeviceReconciler) drainIsolatedLegacyWorker(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	isolatedServiceAccount, sharedServiceAccount string,
) (bool, error) {
	expectedName := device.Name + deploymentSuffix
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Deployments for isolated legacy drain: %w", err)
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Spec.Template.Spec.ServiceAccountName != isolatedServiceAccount {
			continue
		}
		if deployment.Name != expectedName || !metav1.IsControlledBy(deployment, device) {
			return false, fmt.Errorf("isolated legacy ServiceAccount is used by unproven Deployment %s/%s", deployment.Namespace, deployment.Name)
		}
		foreground := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, deployment, &client.DeleteOptions{PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete isolated legacy Deployment %s/%s: %w", deployment.Namespace, deployment.Name, err)
		}
		return false, nil
	}
	// A shared replacement may exist only after an earlier drain completed.
	// Reject any third identity at the canonical name rather than overwriting it.
	var canonical appsv1.Deployment
	key := types.NamespacedName{Namespace: device.Namespace, Name: expectedName}
	if err := r.reader().Get(ctx, key, &canonical); err == nil {
		if !metav1.IsControlledBy(&canonical, device) || canonical.Spec.Template.Spec.ServiceAccountName != sharedServiceAccount {
			return false, fmt.Errorf("legacy Deployment %s is not the exact shared compatibility replacement", key)
		}
	} else if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("read legacy Deployment during isolated drain: %w", err)
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list ReplicaSets for isolated legacy drain: %w", err)
	}
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if replicaSet.Spec.Template.Spec.ServiceAccountName != isolatedServiceAccount {
			continue
		}
		owner := metav1.GetControllerOf(replicaSet)
		if !labelsContain(replicaSet.Labels, perDeviceDeploymentLabels(device.Name)) || owner == nil ||
			owner.APIVersion != appsv1.SchemeGroupVersion.String() || owner.Kind != "Deployment" || owner.Name != expectedName {
			return false, fmt.Errorf("isolated legacy ServiceAccount is retained by unproven ReplicaSet %s/%s", replicaSet.Namespace, replicaSet.Name)
		}
		return false, nil
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Pods for isolated legacy drain: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.ServiceAccountName != isolatedServiceAccount {
			continue
		}
		if !labelsContain(pod.Labels, perDeviceDeploymentLabels(device.Name)) {
			return false, fmt.Errorf("isolated legacy ServiceAccount is retained by unproven Pod %s/%s", pod.Namespace, pod.Name)
		}
		return false, nil
	}
	return true, nil
}

// prepareCompletedLegacyHandoffForEnrollment safely removes the shared
// compatibility writer before a completed reverse handoff is enrolled again.
// This preserves the phase-zero invariant: no shared legacy token retains API
// authority when a Node becomes managed.
func (r *CiscoDeviceReconciler) prepareCompletedLegacyHandoffForEnrollment(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (bool, error) {
	handoff := device.Status.LegacyHandoff
	if err := validateCompletedLegacyHandoffStatus(device, handoff); err != nil {
		return false, err
	}
	if device.Status.NodeIdentity != nil || device.Status.TopologyProjection != nil ||
		device.Status.HealthObservation != nil || device.Status.WorkerRevision != nil ||
		device.Status.NetworkWorkerRevision != nil {
		return false, fmt.Errorf("completed legacy handoff retained managed binding status")
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return false, fmt.Errorf("verify completed legacy Node before re-enrollment: %w", err)
	}
	if !legacyHandoffNodeMatches(&node, handoff) {
		return false, fmt.Errorf("completed legacy handoff does not match the exact released Node before re-enrollment")
	}
	if marker := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; marker != "" {
		return false, fmt.Errorf("completed legacy handoff retained isolated worker marker before re-enrollment")
	}
	isolatedPresent, err := r.isolatedLegacyWorkerAccessPresent(ctx, device)
	if err != nil {
		return false, err
	}
	if isolatedPresent {
		return false, fmt.Errorf("completed legacy handoff retained isolated worker access before re-enrollment")
	}

	if err := r.verifySharedLegacyAccess(ctx, device); err == nil {
		stopped, err := r.drainSharedLegacyWorker(ctx, device)
		if err != nil || !stopped {
			return false, err
		}
		retired, err := r.retireSharedWorkerAccessIfSafe(ctx)
		if err != nil {
			return false, fmt.Errorf("retire shared compatibility access before managed re-enrollment: %w", err)
		}
		if !retired {
			return false, nil
		}
	} else {
		// A crash may occur after the exact shared bindings were removed but
		// before the managed reservation. Absence is recoverable only when no
		// shared authority remains anywhere in the cluster.
		present, inspectErr := r.sharedWorkerAuthorityPresent(ctx)
		if inspectErr != nil {
			return false, fmt.Errorf("verify shared compatibility retirement before re-enrollment: %w", inspectErr)
		}
		if present {
			return false, fmt.Errorf("shared compatibility access is incomplete while legacy shared authority remains")
		}
	}
	present, err := r.sharedWorkerAuthorityPresent(ctx)
	if err != nil {
		return false, fmt.Errorf("verify final shared compatibility retirement: %w", err)
	}
	return !present, nil
}

func (r *CiscoDeviceReconciler) drainSharedLegacyWorker(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (bool, error) {
	sharedServiceAccount := r.vkServiceAccountName()
	expectedName := device.Name + deploymentSuffix
	key := types.NamespacedName{Namespace: device.Namespace, Name: expectedName}
	var deployment appsv1.Deployment
	if err := r.reader().Get(ctx, key, &deployment); err == nil {
		if !metav1.IsControlledBy(&deployment, device) ||
			deployment.Spec.Template.Spec.ServiceAccountName != sharedServiceAccount {
			return false, fmt.Errorf("shared compatibility Deployment %s is not exactly controlled by this CiscoDevice", key)
		}
		foreground := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, &deployment, &client.DeleteOptions{PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete shared compatibility Deployment %s: %w", key, err)
		}
		return false, nil
	} else if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("read shared compatibility Deployment %s: %w", key, err)
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list shared compatibility ReplicaSets: %w", err)
	}
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if replicaSet.Spec.Template.Spec.ServiceAccountName != sharedServiceAccount ||
			!labelsContain(replicaSet.Labels, perDeviceDeploymentLabels(device.Name)) {
			continue
		}
		owner := metav1.GetControllerOf(replicaSet)
		if owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() ||
			owner.Kind != "Deployment" || owner.Name != expectedName {
			return false, fmt.Errorf("shared compatibility ReplicaSet %s/%s has no exact Deployment ownership", replicaSet.Namespace, replicaSet.Name)
		}
		return false, nil
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list shared compatibility Pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.ServiceAccountName == sharedServiceAccount &&
			labelsContain(pod.Labels, perDeviceDeploymentLabels(device.Name)) {
			return false, nil
		}
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) drainLegacyDeviceWorkerForDeletion(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (bool, error) {
	isolatedServiceAccount := topologyLegacyWorkerServiceAccountName(device)
	sharedServiceAccount := r.vkServiceAccountName()
	allowed := map[string]struct{}{isolatedServiceAccount: {}, sharedServiceAccount: {}}
	expectedName := device.Name + deploymentSuffix
	key := types.NamespacedName{Namespace: device.Namespace, Name: expectedName}
	var deployment appsv1.Deployment
	if err := r.reader().Get(ctx, key, &deployment); err == nil {
		if !metav1.IsControlledBy(&deployment, device) {
			return false, fmt.Errorf("refusing legacy device deletion: Deployment %s is not controlled by this CiscoDevice", key)
		}
		if _, ok := allowed[deployment.Spec.Template.Spec.ServiceAccountName]; !ok {
			return false, fmt.Errorf("refusing legacy device deletion: Deployment %s uses unexpected ServiceAccount %q", key, deployment.Spec.Template.Spec.ServiceAccountName)
		}
		foreground := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, &deployment, &client.DeleteOptions{PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete legacy worker Deployment %s: %w", key, err)
		}
		return false, nil
	} else if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("read legacy worker Deployment during deletion: %w", err)
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list legacy worker ReplicaSets during deletion: %w", err)
	}
	for i := range replicaSets.Items {
		replicaSet := &replicaSets.Items[i]
		if _, ok := allowed[replicaSet.Spec.Template.Spec.ServiceAccountName]; !ok ||
			!labelsContain(replicaSet.Labels, perDeviceDeploymentLabels(device.Name)) {
			continue
		}
		owner := metav1.GetControllerOf(replicaSet)
		if owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() ||
			owner.Kind != "Deployment" || owner.Name != expectedName {
			return false, fmt.Errorf("legacy worker ReplicaSet %s/%s has no exact Deployment ownership", replicaSet.Namespace, replicaSet.Name)
		}
		return false, nil
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list legacy worker Pods during deletion: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if _, ok := allowed[pod.Spec.ServiceAccountName]; ok &&
			labelsContain(pod.Labels, perDeviceDeploymentLabels(device.Name)) {
			return false, nil
		}
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) removeIsolatedLegacyWorkerMarker(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) error {
	marker := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]
	if marker == "" {
		return nil
	}
	if marker != string(device.UID) {
		return fmt.Errorf("refusing to remove isolated legacy worker marker for another CiscoDevice incarnation")
	}
	before := device.DeepCopy()
	delete(device.Annotations, managedprotocol.AnnotationIsolatedLegacyWorker)
	if err := r.Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("remove retired isolated legacy worker marker: %w", err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) reconcileCompletedLegacyHandoff(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (managedTopologyResult, error) {
	handoff := device.Status.LegacyHandoff
	if err := validateReleasedLegacyHandoffStatus(device, handoff); err != nil {
		return managedTopologyResult{}, err
	}
	if device.Status.NodeIdentity != nil || device.Status.TopologyProjection != nil || device.Status.HealthObservation != nil ||
		device.Status.WorkerRevision != nil || device.Status.NetworkWorkerRevision != nil {
		return managedTopologyResult{}, fmt.Errorf("released legacy handoff retained managed binding status")
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return managedTopologyResult{}, fmt.Errorf("verify released legacy Node: %w", err)
	}
	if !legacyHandoffNodeMatches(&node, handoff) {
		return managedTopologyResult{}, fmt.Errorf("released legacy handoff does not match the exact Node and audit marker")
	}
	if handoff.Phase == ciskov1.DeviceLegacyHandoffComplete {
		if marker := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; marker != "" {
			return managedTopologyResult{}, fmt.Errorf("completed legacy handoff retained isolated worker marker")
		}
		isolatedPresent, err := r.isolatedLegacyWorkerAccessPresent(ctx, device)
		if err != nil {
			return managedTopologyResult{}, err
		}
		if isolatedPresent {
			return managedTopologyResult{}, fmt.Errorf("completed legacy handoff retained isolated worker access")
		}
		revoked, err := r.managedWorkerAccessRevoked(ctx, device)
		if err != nil {
			return managedTopologyResult{}, err
		}
		if !revoked {
			return managedTopologyResult{}, fmt.Errorf("completed legacy handoff retained per-device managed worker authority")
		}
		if err := r.verifySharedLegacyAccess(ctx, device); err != nil {
			return managedTopologyResult{}, fmt.Errorf("completed legacy handoff has no exact shared compatibility access evidence: %w", err)
		}
		return r.completedLegacyHandoffResult(device), nil
	}

	isolatedPresent, err := r.isolatedLegacyWorkerAccessPresent(ctx, device)
	if err != nil {
		return managedTopologyResult{}, err
	}
	marker := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]
	if marker != "" && marker != string(device.UID) {
		return managedTopologyResult{}, fmt.Errorf("isolated legacy worker marker belongs to a different CiscoDevice incarnation")
	}
	if isolatedPresent && marker == "" {
		return managedTopologyResult{}, fmt.Errorf("shared-writer transition retained isolated access without its UID-bound marker")
	}
	revoked, err := r.managedWorkerAccessRevoked(ctx, device)
	if err != nil {
		return managedTopologyResult{}, err
	}
	if !revoked {
		return managedTopologyResult{}, fmt.Errorf("managed worker authority exists during shared-writer transition")
	}
	if err := r.removeLegacyHandoffGuard(ctx, handoff); err != nil {
		return managedTopologyResult{}, err
	}
	if err := r.cleanupManagedSharedWorkerAccess(ctx, device); err != nil {
		return managedTopologyResult{}, fmt.Errorf("retire unused functional worker access after legacy handoff: %w", err)
	}

	legacySA := topologyLegacyWorkerServiceAccountName(device)
	sharedSA := r.vkServiceAccountName()
	if isolatedPresent {
		stopped, err := r.drainIsolatedLegacyWorker(ctx, device, legacySA, sharedSA)
		if err != nil {
			return managedTopologyResult{}, err
		}
		if !stopped {
			return sharedPendingLegacyHandoffResult(device, legacySA, true), nil
		}
		if err := r.ensureVKAccess(ctx, device, sharedSA, false); err != nil {
			return managedTopologyResult{}, fmt.Errorf("establish shared compatibility worker access: %w", err)
		}
		if err := r.verifySharedLegacyAccess(ctx, device); err != nil {
			return managedTopologyResult{}, fmt.Errorf("verify shared compatibility worker access: %w", err)
		}
	} else if err := r.verifySharedLegacyAccess(ctx, device); err != nil {
		// Absence of both identities cannot be repaired from status alone. This
		// distinguishes an interrupted, already-ready cleanup from forged status.
		return managedTopologyResult{}, fmt.Errorf("shared-writer transition has neither isolated nor exact shared access evidence: %w", err)
	}

	ready, err := r.legacyWriterReadyForServiceAccount(ctx, device, &node, sharedSA, handoff.IsolatedReadyAt.Time)
	if err != nil {
		return managedTopologyResult{}, err
	}
	if !ready {
		return sharedPendingLegacyHandoffResult(device, sharedSA, false), nil
	}
	if isolatedPresent {
		if err := r.cleanupGeneratedWorkerAccess(ctx, device, legacySA, false); err != nil {
			return sharedPendingLegacyHandoffResult(device, sharedSA, false), fmt.Errorf("retire isolated legacy worker access: %w", err)
		}
		isolatedPresent, err = r.isolatedLegacyWorkerAccessPresent(ctx, device)
		if err != nil {
			return sharedPendingLegacyHandoffResult(device, sharedSA, false), err
		}
		if isolatedPresent {
			return sharedPendingLegacyHandoffResult(device, sharedSA, false), nil
		}
	}
	if err := r.removeIsolatedLegacyWorkerMarker(ctx, device); err != nil {
		return sharedPendingLegacyHandoffResult(device, sharedSA, false), err
	}
	completedAt := metav1.NewTime(r.now())
	complete := copyLegacyHandoff(handoff)
	complete.Phase = ciskov1.DeviceLegacyHandoffComplete
	complete.CompletedAt = &completedAt
	if err := r.completeLegacyHandoffStatus(ctx, device, complete, "SharedLegacyWriterReady"); err != nil {
		return sharedPendingLegacyHandoffResult(device, sharedSA, false), err
	}
	return r.completedLegacyHandoffResult(device), nil
}

func validateCompletedLegacyHandoffStatus(device *ciskov1.CiscoDevice, handoff *ciskov1.DeviceLegacyHandoffStatus) error {
	if device == nil || device.UID == "" || handoff == nil ||
		handoff.Phase != ciskov1.DeviceLegacyHandoffComplete ||
		handoff.DeviceUID != string(device.UID) || handoff.NodeName != resolvedNodeName(device) || handoff.NodeUID == "" ||
		handoff.LegacyWorkerUsername != "system:serviceaccount:"+device.Namespace+":"+topologyLegacyWorkerServiceAccountName(device) ||
		handoff.RequestedAt.IsZero() || handoff.NodeReleasedAt == nil || handoff.NodeReleasedAt.IsZero() ||
		handoff.IsolatedReadyAt == nil || handoff.IsolatedReadyAt.IsZero() || handoff.CompletedAt == nil || handoff.CompletedAt.IsZero() {
		return fmt.Errorf("completed legacy handoff record is incomplete or does not match this CiscoDevice incarnation")
	}
	if handoff.NodeReleasedAt.Time.Before(handoff.RequestedAt.Time) ||
		handoff.IsolatedReadyAt.Time.Before(handoff.NodeReleasedAt.Time) ||
		handoff.CompletedAt.Time.Before(handoff.IsolatedReadyAt.Time) {
		return fmt.Errorf("completed legacy handoff timestamps are out of order")
	}
	return nil
}

func validateReleasedLegacyHandoffStatus(device *ciskov1.CiscoDevice, handoff *ciskov1.DeviceLegacyHandoffStatus) error {
	if handoff == nil || (handoff.Phase != ciskov1.DeviceLegacyHandoffSharedWriterPending &&
		handoff.Phase != ciskov1.DeviceLegacyHandoffComplete) {
		return fmt.Errorf("released legacy handoff is not in shared-writer transition or complete phase")
	}
	if handoff.Phase == ciskov1.DeviceLegacyHandoffComplete {
		return validateCompletedLegacyHandoffStatus(device, handoff)
	}
	if device == nil || device.UID == "" || handoff.DeviceUID != string(device.UID) ||
		handoff.NodeName != resolvedNodeName(device) || handoff.NodeUID == "" ||
		handoff.LegacyWorkerUsername != "system:serviceaccount:"+device.Namespace+":"+topologyLegacyWorkerServiceAccountName(device) ||
		handoff.RequestedAt.IsZero() || handoff.NodeReleasedAt == nil || handoff.NodeReleasedAt.IsZero() ||
		handoff.IsolatedReadyAt == nil || handoff.IsolatedReadyAt.IsZero() || handoff.CompletedAt != nil {
		return fmt.Errorf("shared-writer transition record is incomplete or does not match this CiscoDevice incarnation")
	}
	if handoff.NodeReleasedAt.Time.Before(handoff.RequestedAt.Time) ||
		handoff.IsolatedReadyAt.Time.Before(handoff.NodeReleasedAt.Time) {
		return fmt.Errorf("shared-writer transition timestamps are out of order")
	}
	return nil
}

func validateLegacyHandoffStatus(
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
	projectionHash, legacyUsername string,
) error {
	bound := device.Status.NodeIdentity
	if bound == nil || handoff == nil || handoff.DeviceUID != string(device.UID) ||
		handoff.NodeName != bound.NodeName || handoff.NodeUID != bound.NodeUID ||
		handoff.ProjectionHash != projectionHash || handoff.LegacyWorkerUsername != legacyUsername ||
		handoff.RequestedAt.IsZero() {
		return fmt.Errorf("legacy handoff status does not match the current device, Node, projection, and isolated worker identity")
	}
	if handoff.Phase == ciskov1.DeviceLegacyHandoffSharedWriterPending ||
		handoff.Phase == ciskov1.DeviceLegacyHandoffComplete {
		return fmt.Errorf("released legacy handoff cannot retain a managed Node identity")
	}
	return nil
}

func (r *CiscoDeviceReconciler) legacyHandoffSafetyReady(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if lock := device.Status.TopologyLock; lock != nil {
		return fmt.Errorf("topology lock %q is %q", lock.ReservationID, lock.State)
	}
	if err := r.ensureManagedDeviceAuthoritiesSettled(ctx, device); err != nil {
		return err
	}
	return nil
}

func (r *CiscoDeviceReconciler) patchLegacyHandoffStatus(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
	reason, message string,
) error {
	before := device.DeepCopy()
	device.Status.LegacyHandoff = copyLegacyHandoff(handoff)
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionLegacyHandoffReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            truncateTopologyMessage(message),
		ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionTopologyReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            "managed topology is leaving this device under an explicit reverse writer handoff",
		ObservedGeneration: device.Generation,
	})
	if err := r.Status().Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("record legacy writer handoff: %w", err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) beginSharedLegacyHandoffStatus(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
	reason string,
) error {
	before := device.DeepCopy()
	device.Status.LegacyHandoff = copyLegacyHandoff(handoff)
	device.Status.NodeIdentity = nil
	device.Status.TopologyProjection = nil
	device.Status.HealthObservation = nil
	device.Status.WorkerRevision = nil
	device.Status.NetworkWorkerRevision = nil
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionLegacyHandoffReady,
		Status:             metav1.ConditionFalse,
		Reason:             "SharedLegacyWriterPending",
		Message:            "isolated handoff worker is ready; replacing it with the namespace-shared compatibility worker",
		ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionNodeIdentityReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            "managed Node identity was released after legacy writer readiness",
		ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionTopologyReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            "managed topology projection is disabled for this device",
		ObservedGeneration: device.Generation,
	})
	if err := r.Status().Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("begin shared legacy writer transition: %w", err)
	}
	return nil
}

func (r *CiscoDeviceReconciler) completeLegacyHandoffStatus(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
	reason string,
) error {
	before := device.DeepCopy()
	device.Status.LegacyHandoff = copyLegacyHandoff(handoff)
	device.Status.NodeIdentity = nil
	device.Status.TopologyProjection = nil
	device.Status.HealthObservation = nil
	device.Status.WorkerRevision = nil
	device.Status.NetworkWorkerRevision = nil
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionLegacyHandoffReady,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            "namespace-shared compatibility worker owns the released Node; isolated handoff credentials are retired",
		ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionNodeIdentityReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            "managed Node identity remains released in topology-disabled compatibility mode",
		ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionTopologyReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            "managed topology projection is disabled for this device",
		ObservedGeneration: device.Generation,
	})
	if err := r.Status().Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("complete shared legacy writer handoff status: %w", err)
	}
	return nil
}

// managedWriterWorkloadsStopped proves that no Pod can still be running with
// a former managed ServiceAccount before per-device authority and Leases are
// removed.
func (r *CiscoDeviceReconciler) quiesceManagedDeviceWorkersForDeletion(ctx context.Context,
	device *ciskov1.CiscoDevice) (bool, error) {
	deleting := false
	for _, name := range []string{device.Name + deploymentSuffix, networkDeploymentName(device.Name, string(device.UID))} {
		var deployment appsv1.Deployment
		key := types.NamespacedName{Namespace: device.Namespace, Name: name}
		if err := r.reader().Get(ctx, key, &deployment); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, fmt.Errorf("read managed worker Deployment %s during deletion: %w", key, err)
		}
		if !metav1.IsControlledBy(&deployment, device) {
			return false, fmt.Errorf("refusing device deletion: managed worker Deployment %s is not controlled by CiscoDevice UID %s", key, device.UID)
		}
		foreground := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, &deployment, &client.DeleteOptions{PropagationPolicy: &foreground}); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete managed worker Deployment %s: %w", key, err)
		}
		deleting = true
	}
	if deleting {
		return false, nil
	}
	stopped, err := r.managedWriterWorkloadsStopped(ctx, device)
	if err != nil || !stopped {
		return stopped, err
	}
	if err := r.reconcileManagedWorkerObjectBindings(ctx, device, nil, nil, true, true); err != nil {
		return false, fmt.Errorf("clear managed worker Pod bindings during device deletion: %w", err)
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) managedDeviceWorkerBindingsCleared(ctx context.Context,
	device *ciskov1.CiscoDevice) (bool, error) {
	stopped, err := r.managedWriterWorkloadsStopped(ctx, device)
	if err != nil || !stopped {
		return stopped, err
	}
	if device.Status.NodeIdentity == nil {
		return true, nil
	}
	var node corev1.Node
	key := types.NamespacedName{Name: device.Status.NodeIdentity.NodeName}
	if err := r.reader().Get(ctx, key, &node); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("read managed Node before deletion retry: %w", err)
	}
	if string(node.UID) != device.Status.NodeIdentity.NodeUID || !managedNodeMatchesDevice(&node, device) {
		return false, fmt.Errorf("managed Node identity changed before deletion retry")
	}
	for _, annotation := range []string{
		managedprotocol.AnnotationAppWorkerPodName,
		managedprotocol.AnnotationAppWorkerPodUID,
		managedprotocol.AnnotationNetworkWorkerPodName,
		managedprotocol.AnnotationNetworkWorkerPodUID,
	} {
		if strings.TrimSpace(node.Annotations[annotation]) != "" {
			return false, nil
		}
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) managedWriterWorkloadsStopped(ctx context.Context, device *ciskov1.CiscoDevice) (bool, error) {
	appAccount := r.appHostingServiceAccountName()
	networkAccount := r.networkManagementServiceAccountName()
	priorDeviceAccount := managedWorkerServiceAccountName(device)
	managedAccounts := map[string]struct{}{appAccount: {}, networkAccount: {}, priorDeviceAccount: {}}
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Deployments before managed writer revocation: %w", err)
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if _, managed := managedAccounts[deployment.Spec.Template.Spec.ServiceAccountName]; managed &&
			metav1.IsControlledBy(deployment, device) {
			return false, nil
		}
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Pods before managed writer revocation: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		account := pod.Spec.ServiceAccountName
		if account == priorDeviceAccount {
			return false, nil
		}
		if account != appAccount && account != networkAccount {
			continue
		}
		workerDeployment := ""
		switch {
		case labelsContain(pod.Labels, perDeviceDeploymentLabels(device.Name)):
			workerDeployment = device.Name + deploymentSuffix
		case labelsContain(pod.Labels, perDeviceNetworkDeploymentLabels(device.Name)):
			workerDeployment = networkDeploymentName(device.Name, string(device.UID))
		default:
			// The shared accounts are intentionally reused by other devices. Their
			// Pods must not block this device's bounded reverse handoff.
			continue
		}
		owned, err := podOwnedByNamedDeviceDeployment(ctx, r.reader(), pod, device, workerDeployment)
		if err != nil {
			return false, err
		}
		if !owned {
			return false, fmt.Errorf("shared worker Pod %s/%s carries exact labels for CiscoDevice %s but lacks its controller ownership chain",
				pod.Namespace, pod.Name, device.Name)
		}
		return false, nil
	}
	return true, nil
}

func labelsContain(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func podOwnedByNamedDeviceDeployment(ctx context.Context, reader client.Reader, pod *corev1.Pod,
	device *ciskov1.CiscoDevice, deploymentName string) (bool, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.APIVersion != appsv1.SchemeGroupVersion.String() ||
		owner.Kind != "ReplicaSet" || owner.UID == "" {
		return false, nil
	}
	var replicaSet appsv1.ReplicaSet
	key := types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}
	if err := reader.Get(ctx, key, &replicaSet); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read shared worker ReplicaSet ownership: %w", err)
	}
	if replicaSet.UID != owner.UID {
		return false, nil
	}
	deploymentOwner := metav1.GetControllerOf(&replicaSet)
	if deploymentOwner == nil || deploymentOwner.APIVersion != appsv1.SchemeGroupVersion.String() ||
		deploymentOwner.Kind != "Deployment" || deploymentOwner.Name != deploymentName || deploymentOwner.UID == "" {
		return false, nil
	}
	var deployment appsv1.Deployment
	key = types.NamespacedName{Namespace: pod.Namespace, Name: deploymentName}
	if err := reader.Get(ctx, key, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			// Foreground deletion may remove the Deployment API object before its
			// Pod. The exact labels plus retained RS controller UID still bind the
			// terminating workload to this device name.
			return true, nil
		}
		return false, fmt.Errorf("read shared worker Deployment ownership: %w", err)
	}
	return deployment.UID == deploymentOwner.UID && metav1.IsControlledBy(&deployment, device), nil
}

func copyLegacyHandoff(in *ciskov1.DeviceLegacyHandoffStatus) *ciskov1.DeviceLegacyHandoffStatus {
	if in == nil {
		return nil
	}
	out := *in
	if in.NodeReleasedAt != nil {
		value := in.NodeReleasedAt.DeepCopy()
		out.NodeReleasedAt = value
	}
	if in.IsolatedReadyAt != nil {
		value := in.IsolatedReadyAt.DeepCopy()
		out.IsolatedReadyAt = value
	}
	if in.CompletedAt != nil {
		value := in.CompletedAt.DeepCopy()
		out.CompletedAt = value
	}
	return &out
}

func (r *CiscoDeviceReconciler) releaseManagedNodeToLegacy(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
) (*corev1.Node, map[string]string, error) {
	desired, err := configprovider.GetInitialNodeSpecWithTopologyMode(
		handoff.NodeName, device.Spec.DeepCopy(), topology.ProjectionModeStandaloneCompatibility)
	if err != nil {
		return nil, nil, fmt.Errorf("build standalone Node projection for legacy handoff: %w", err)
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return nil, nil, fmt.Errorf("read Node for legacy handoff: %w", err)
	}
	if string(node.UID) != handoff.NodeUID {
		return nil, nil, fmt.Errorf("Node %q UID changed during legacy handoff", handoff.NodeName)
	}
	if node.Annotations[managedprotocol.AnnotationManaged] != "true" {
		if !legacyHandoffNodeMatches(&node, handoff) {
			return nil, nil, fmt.Errorf("Node %q is neither the exact managed binding nor the exact released legacy Node", node.Name)
		}
		return &node, desired.Labels, nil
	}
	if !managedNodeMatchesLegacyReleaseBinding(&node, device, handoff) {
		return nil, nil, fmt.Errorf("managed Node binding changed before legacy release")
	}

	before := node.DeepCopy()
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	ownedLabels := decodeStringSet(node.Annotations[managedprotocol.AnnotationProjectedKeys])
	for _, key := range []string{
		corev1.LabelHostname,
		corev1.LabelTopologyRegion,
		corev1.LabelTopologyZone,
		topology.LabelPlatform,
		topology.LabelProvider,
		topology.LabelType,
	} {
		ownedLabels[key] = struct{}{}
	}
	for key := range ownedLabels {
		delete(node.Labels, key)
	}
	for key, value := range desired.Labels {
		node.Labels[key] = value
	}

	for identity := range decodeManagedTaints(node.Annotations[managedprotocol.AnnotationManagedTaints]) {
		node.Spec.Taints = deleteTaint(node.Spec.Taints, identity)
	}
	node.Spec.Taints = deleteTaint(node.Spec.Taints, taintIdentity(topologyInitializationTaint()))
	node.Spec.Taints = deleteTaint(node.Spec.Taints, taintIdentity(maintenanceGuardTaint()))
	for _, taint := range device.Spec.Taints {
		node.Spec.Taints = upsertTaint(node.Spec.Taints, taint)
	}
	node.Spec.Taints = upsertTaint(node.Spec.Taints, topologyInitializationTaint())
	if cordonOwner := strings.TrimSpace(node.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID]); cordonOwner != "" {
		if cordonOwner != string(device.UID) {
			return nil, nil, fmt.Errorf("refusing legacy handoff: Node %q app-hosting cordon belongs to device UID %q",
				node.Name, cordonOwner)
		}
		// A CVK-owned cordon must not leak into the isolated legacy lifecycle.
		// An operator-owned cordon has no marker and remains untouched.
		node.Spec.Unschedulable = false
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	if err := sanitizeVirtualKubeletLastAppliedMetadata(&node); err != nil {
		return nil, nil, fmt.Errorf("reset legacy writer metadata baseline: %w", err)
	}
	for _, key := range managedNodeBindingAnnotationKeys {
		delete(node.Annotations, key)
	}
	node.Annotations[managedprotocol.AnnotationLegacyHandoff] = handoff.NodeUID
	if err := r.Patch(ctx, &node,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return nil, nil, fmt.Errorf("release Node %q to isolated legacy writer: %w", node.Name, err)
	}
	return &node, desired.Labels, nil
}

func managedNodeMatchesLegacyReleaseBinding(
	node *corev1.Node,
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
) bool {
	if node == nil || device == nil || handoff == nil {
		return false
	}
	workerUsername := node.Annotations[managedprotocol.AnnotationWorkerUsername]
	appUsername := node.Annotations[managedprotocol.AnnotationAppWorkerUsername]
	networkUsername := node.Annotations[managedprotocol.AnnotationNetworkWorkerUsername]
	workerBindingMatches := workerUsername == "system:serviceaccount:"+device.Namespace+":"+managedWorkerServiceAccountName(device)
	if appUsername != "" || networkUsername != "" {
		workerBindingMatches = workerUsername != "" && workerUsername == appUsername && networkUsername != ""
	}
	if !managedNodeMatchesDevice(node, device) ||
		node.Name != handoff.NodeName || string(node.UID) != handoff.NodeUID ||
		node.Annotations[managedprotocol.AnnotationNodeUID] != handoff.NodeUID ||
		!workerBindingMatches ||
		node.Annotations[managedprotocol.AnnotationWorkerProtocol] != managedprotocol.Version ||
		node.Annotations[managedprotocol.AnnotationProjectionHash] != handoff.ProjectionHash {
		return false
	}
	_, hasProjectedKeys := node.Annotations[managedprotocol.AnnotationProjectedKeys]
	_, hasManagedTaints := node.Annotations[managedprotocol.AnnotationManagedTaints]
	return hasProjectedKeys && hasManagedTaints
}

var managedNodeBindingAnnotationKeys = []string{
	managedprotocol.AnnotationManaged,
	managedprotocol.AnnotationDeviceNamespace,
	managedprotocol.AnnotationDeviceName,
	managedprotocol.AnnotationDeviceUID,
	managedprotocol.AnnotationNodeUID,
	managedprotocol.AnnotationWorkerUsername,
	managedprotocol.AnnotationAppWorkerUsername,
	managedprotocol.AnnotationAppWorkerPodName,
	managedprotocol.AnnotationAppWorkerPodUID,
	managedprotocol.AnnotationNetworkWorkerUsername,
	managedprotocol.AnnotationNetworkWorkerPodName,
	managedprotocol.AnnotationNetworkWorkerPodUID,
	managedprotocol.AnnotationWorkerProtocol,
	managedprotocol.AnnotationWorkerObservedRevision,
	managedprotocol.AnnotationProjectedKeys,
	managedprotocol.AnnotationProjectionHash,
	managedprotocol.AnnotationManagedTaints,
	managedprotocol.AnnotationAppHostingCordonDeviceUID,
}

func legacyHandoffNodeMatches(node *corev1.Node, handoff *ciskov1.DeviceLegacyHandoffStatus) bool {
	return node != nil && handoff != nil && node.Name == handoff.NodeName &&
		string(node.UID) == handoff.NodeUID && node.Annotations[managedprotocol.AnnotationManaged] != "true" &&
		node.Annotations[managedprotocol.AnnotationLegacyHandoff] == handoff.NodeUID
}

func completedLegacyHandoffMatchesNode(device *ciskov1.CiscoDevice, node *corev1.Node) bool {
	if device == nil || device.Status.LegacyHandoff == nil ||
		device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
		return false
	}
	handoff := device.Status.LegacyHandoff
	return handoff.DeviceUID == string(device.UID) && legacyHandoffNodeMatches(node, handoff)
}

func (r *CiscoDeviceReconciler) legacyWriterReady(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	desiredLabels map[string]string,
	releasedAt time.Time,
) (bool, error) {
	return r.legacyWriterReadyWithLabels(ctx, device, node, desiredLabels,
		topologyLegacyWorkerServiceAccountName(device), releasedAt)
}

func (r *CiscoDeviceReconciler) legacyWriterReadyForServiceAccount(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	serviceAccount string,
	readinessAfter time.Time,
) (bool, error) {
	desired, err := configprovider.GetInitialNodeSpecWithTopologyMode(
		device.Status.LegacyHandoff.NodeName, device.Spec.DeepCopy(), topology.ProjectionModeStandaloneCompatibility)
	if err != nil {
		return false, fmt.Errorf("build standalone Node projection for shared legacy readiness: %w", err)
	}
	return r.legacyWriterReadyWithLabels(ctx, device, node, desired.Labels, serviceAccount, readinessAfter)
}

func (r *CiscoDeviceReconciler) legacyWriterReadyWithLabels(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	desiredLabels map[string]string,
	serviceAccount string,
	readinessAfter time.Time,
) (bool, error) {
	var deployment appsv1.Deployment
	key := types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}
	if err := r.reader().Get(ctx, key, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy worker Deployment: %w", err)
	}
	if !metav1.IsControlledBy(&deployment, device) {
		return false, fmt.Errorf("legacy worker Deployment is not controlled by this CiscoDevice incarnation")
	}
	releaseEpoch := device.Status.LegacyHandoff.NodeReleasedAt.UTC().Format(time.RFC3339Nano)
	if deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType ||
		deployment.Spec.Template.Spec.ServiceAccountName != serviceAccount ||
		deployment.Spec.Template.Annotations[managedprotocol.AnnotationLegacyHandoffRelease] != releaseEpoch ||
		deployment.Status.ObservedGeneration < deployment.Generation || !deploymentRolloutComplete(&deployment) {
		return false, nil
	}

	var replicaSets appsv1.ReplicaSetList
	if err := r.reader().List(ctx, &replicaSets, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list legacy worker ReplicaSets: %w", err)
	}
	legacyReplicaSets := map[types.UID]struct{}{}
	for i := range replicaSets.Items {
		rs := &replicaSets.Items[i]
		owner := metav1.GetControllerOf(rs)
		if owner != nil && owner.APIVersion == appsv1.SchemeGroupVersion.String() && owner.Kind == "Deployment" &&
			owner.Name == deployment.Name && owner.UID == deployment.UID &&
			rs.Spec.Template.Spec.ServiceAccountName == serviceAccount &&
			rs.Spec.Template.Annotations[managedprotocol.AnnotationLegacyHandoffRelease] == releaseEpoch {
			legacyReplicaSets[rs.UID] = struct{}{}
		}
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list legacy worker Pods: %w", err)
	}
	readyPod := false
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.ServiceAccountName != serviceAccount || !pod.DeletionTimestamp.IsZero() ||
			pod.Status.Phase != corev1.PodRunning || pod.Status.StartTime == nil || !pod.Status.StartTime.Time.After(readinessAfter) {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		if owner == nil {
			continue
		}
		if _, ok := legacyReplicaSets[owner.UID]; !ok {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				readyPod = true
				break
			}
		}
	}
	if !readyPod || !legacyHandoffNodeMatches(node, device.Status.LegacyHandoff) || !nodeReady(node) {
		return false, nil
	}
	ready := managedNodeReadyCondition(node)
	if ready == nil || ready.LastHeartbeatTime.IsZero() || !ready.LastHeartbeatTime.Time.After(readinessAfter) ||
		ready.LastHeartbeatTime.Time.After(r.now().Add(legacyHandoffFutureSkew)) {
		return false, nil
	}
	return legacyWriterPublishedMetadata(node, desiredLabels), nil
}

func legacyWriterPublishedMetadata(node *corev1.Node, desiredLabels map[string]string) bool {
	if node == nil || node.Annotations == nil {
		return false
	}
	encoded := node.Annotations[managedprotocol.VirtualKubeletLastAppliedObjectMeta]
	if encoded == "" {
		return false
	}
	var observed metav1.ObjectMeta
	if err := json.Unmarshal([]byte(encoded), &observed); err != nil {
		return false
	}
	if observed.Name != node.Name || (observed.UID != "" && observed.UID != node.UID) {
		return false
	}
	for key, value := range desiredLabels {
		if observed.Labels[key] != value {
			return false
		}
	}
	return len(observed.Labels) != 0
}

func (r *CiscoDeviceReconciler) removeLegacyHandoffGuard(
	ctx context.Context,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
) error {
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return fmt.Errorf("read completed legacy Node: %w", err)
	}
	if !legacyHandoffNodeMatches(&node, handoff) {
		return fmt.Errorf("completed legacy Node identity or audit marker changed")
	}
	before := node.DeepCopy()
	node.Spec.Taints = deleteTaint(node.Spec.Taints, taintIdentity(topologyInitializationTaint()))
	if taintsEqual(before.Spec.Taints, node.Spec.Taints) {
		return nil
	}
	if err := r.Patch(ctx, &node,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("remove completed legacy handoff scheduling guard: %w", err)
	}
	return nil
}
