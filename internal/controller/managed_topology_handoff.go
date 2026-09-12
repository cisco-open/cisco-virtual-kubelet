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

// reconcileLegacyWriterHandoff transfers a Node from the managed status-only
// worker to an isolated, UID-derived legacy worker. The user must explicitly
// authorize the exact Node incarnation through a topology-protected
// annotation. No phase restores the chart-wide shared worker credential.
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
		completedAt := metav1.NewTime(r.now())
		complete := copyLegacyHandoff(handoff)
		complete.Phase = ciskov1.DeviceLegacyHandoffComplete
		complete.CompletedAt = &completedAt
		if err := r.completeLegacyHandoffStatus(ctx, device, complete, reason); err != nil {
			return result, err
		}
		if err := r.removeLegacyHandoffGuard(ctx, complete); err != nil {
			return completedLegacyHandoffResult(device), err
		}
		return completedLegacyHandoffResult(device), nil

	case handoff.Phase == ciskov1.DeviceLegacyHandoffComplete:
		return r.reconcileCompletedLegacyHandoff(ctx, device)
	default:
		return result, fmt.Errorf("unsupported legacy handoff phase %q", handoff.Phase)
	}
}

func completedLegacyHandoffResult(device *ciskov1.CiscoDevice) managedTopologyResult {
	if device == nil || device.Status.LegacyHandoff == nil ||
		device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
		return managedTopologyResult{}
	}
	return managedTopologyResult{
		LegacyWorker: true,
		NodeName:     device.Status.LegacyHandoff.NodeName,
	}
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
	if r.reader() == nil {
		return false, fmt.Errorf("cannot verify isolated legacy worker without an API reader")
	}
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	var serviceAccount corev1.ServiceAccount
	key := types.NamespacedName{Namespace: device.Namespace, Name: legacySA}
	if err := r.reader().Get(ctx, key, &serviceAccount); err != nil {
		if apierrors.IsNotFound(err) {
			if marker != "" {
				return false, fmt.Errorf("isolated legacy worker marker has no exact owned ServiceAccount evidence")
			}
			return false, nil
		}
		return false, fmt.Errorf("inspect isolated legacy ServiceAccount: %w", err)
	}
	if !managedServiceAccountOwnedByDevice(&serviceAccount, device) ||
		!workerAnnotationsMatch(serviceAccount.Annotations, workerServiceAccountAnnotations(device, false)) {
		return false, fmt.Errorf("existing isolated legacy ServiceAccount is not exactly bound to this CiscoDevice incarnation")
	}
	if err := r.auditGeneratedWorkerBindings(ctx, device, legacySA, vkSharedClusterRole); err != nil {
		return false, fmt.Errorf("audit recovered isolated legacy worker: %w", err)
	}
	if err := r.ensureIsolatedLegacyWorkerMarker(ctx, device); err != nil {
		return false, err
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) reconcileCompletedLegacyHandoff(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (managedTopologyResult, error) {
	handoff := device.Status.LegacyHandoff
	if err := validateCompletedLegacyHandoffStatus(device, handoff); err != nil {
		return managedTopologyResult{}, err
	}
	if device.Status.NodeIdentity != nil || device.Status.TopologyProjection != nil || device.Status.HealthObservation != nil ||
		device.Status.WorkerRevision != nil {
		return managedTopologyResult{}, fmt.Errorf("completed legacy handoff retained managed binding status")
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return managedTopologyResult{}, fmt.Errorf("verify completed legacy Node: %w", err)
	}
	if !legacyHandoffNodeMatches(&node, handoff) {
		return managedTopologyResult{}, fmt.Errorf("completed legacy handoff does not match the exact released Node and audit marker")
	}
	// A retained status record is never sufficient authority to mint broad
	// legacy access. Require the exact manager-owned ServiceAccount and both
	// canonical bindings to exist first. This makes forged pre-binding status
	// fail closed even if admission was temporarily unavailable.
	recovered, err := r.recoverIsolatedLegacyWorker(ctx, device)
	if err != nil {
		return managedTopologyResult{}, err
	}
	if !recovered {
		return managedTopologyResult{}, fmt.Errorf("completed legacy handoff has no exact isolated worker access evidence")
	}
	if err := r.ensureVKAccess(ctx, device, topologyLegacyWorkerServiceAccountName(device), false, true); err != nil {
		return completedLegacyHandoffResult(device), fmt.Errorf("reconcile completed isolated legacy worker access: %w", err)
	}
	revoked, err := r.managedWorkerAccessRevoked(ctx, device)
	if err != nil {
		return completedLegacyHandoffResult(device), err
	}
	if !revoked {
		return completedLegacyHandoffResult(device), fmt.Errorf("managed worker authority exists after completed legacy handoff")
	}
	if err := r.removeLegacyHandoffGuard(ctx, handoff); err != nil {
		return completedLegacyHandoffResult(device), err
	}
	return completedLegacyHandoffResult(device), nil
}

func validateCompletedLegacyHandoffStatus(device *ciskov1.CiscoDevice, handoff *ciskov1.DeviceLegacyHandoffStatus) error {
	if device == nil || device.UID == "" || handoff == nil ||
		handoff.Phase != ciskov1.DeviceLegacyHandoffComplete ||
		handoff.DeviceUID != string(device.UID) || handoff.NodeName != resolvedNodeName(device) || handoff.NodeUID == "" ||
		handoff.LegacyWorkerUsername != "system:serviceaccount:"+device.Namespace+":"+topologyLegacyWorkerServiceAccountName(device) ||
		handoff.RequestedAt.IsZero() || handoff.NodeReleasedAt == nil || handoff.NodeReleasedAt.IsZero() ||
		handoff.CompletedAt == nil || handoff.CompletedAt.IsZero() {
		return fmt.Errorf("completed legacy handoff record is incomplete or does not match this CiscoDevice incarnation")
	}
	if handoff.NodeReleasedAt.Time.Before(handoff.RequestedAt.Time) || handoff.CompletedAt.Time.Before(handoff.NodeReleasedAt.Time) {
		return fmt.Errorf("completed legacy handoff timestamps are out of order")
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
	if handoff.Phase == ciskov1.DeviceLegacyHandoffComplete {
		return fmt.Errorf("completed legacy handoff cannot retain a managed Node identity")
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
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type:               ciskov1.CiscoDeviceConditionLegacyHandoffReady,
		Status:             metav1.ConditionTrue,
		Reason:             "LegacyWriterReady",
		Message:            "isolated per-device legacy worker owns the Node; shared worker credentials remain retired",
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
		return fmt.Errorf("complete legacy writer handoff status: %w", err)
	}
	return nil
}

// managedWriterWorkloadsStopped proves that no Pod can still be running with
// the former managed ServiceAccount before its bindings and Leases are removed.
func (r *CiscoDeviceReconciler) managedWriterWorkloadsStopped(ctx context.Context, device *ciskov1.CiscoDevice) (bool, error) {
	managedSA := managedWorkerServiceAccountName(device)
	var deployments appsv1.DeploymentList
	if err := r.reader().List(ctx, &deployments, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Deployments before managed writer revocation: %w", err)
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if deployment.Spec.Template.Spec.ServiceAccountName == managedSA {
			return false, nil
		}
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(device.Namespace)); err != nil {
		return false, fmt.Errorf("list Pods before managed writer revocation: %w", err)
	}
	for i := range pods.Items {
		if pods.Items[i].Spec.ServiceAccountName == managedSA {
			return false, nil
		}
	}
	return true, nil
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
	if node == nil || device == nil || handoff == nil || !managedNodeMatchesDevice(node, device) ||
		node.Name != handoff.NodeName || string(node.UID) != handoff.NodeUID ||
		node.Annotations[managedprotocol.AnnotationNodeUID] != handoff.NodeUID ||
		node.Annotations[managedprotocol.AnnotationWorkerUsername] !=
			"system:serviceaccount:"+device.Namespace+":"+managedWorkerServiceAccountName(device) ||
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
	managedprotocol.AnnotationWorkerProtocol,
	managedprotocol.AnnotationWorkerObservedRevision,
	managedprotocol.AnnotationProjectedKeys,
	managedprotocol.AnnotationProjectionHash,
	managedprotocol.AnnotationManagedTaints,
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
	legacySA := topologyLegacyWorkerServiceAccountName(device)
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
	if deployment.Spec.Template.Spec.ServiceAccountName != legacySA ||
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
			rs.Spec.Template.Spec.ServiceAccountName == legacySA &&
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
		if pod.Spec.ServiceAccountName != legacySA || !pod.DeletionTimestamp.IsZero() ||
			pod.Status.Phase != corev1.PodRunning || pod.Status.StartTime == nil || pod.Status.StartTime.Time.Before(releasedAt) {
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
	if ready == nil || ready.LastHeartbeatTime.IsZero() || ready.LastHeartbeatTime.Time.Before(releasedAt) ||
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
