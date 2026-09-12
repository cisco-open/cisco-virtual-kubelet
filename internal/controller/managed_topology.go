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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	configprovider "github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

// managedTopologyResult tells the CiscoDevice reconciler whether this device
// has entered managed ownership and whether its worker may be reconciled.
type managedTopologyResult struct {
	Managed      bool
	LegacyWorker bool
	NodeName     string
	Policy       *topologyrollout.ParsedAdminPolicy
	RequeueAfter time.Duration
}

func (r *CiscoDeviceReconciler) reconcileManagedTopology(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (managedTopologyResult, error) {
	if !r.ManagedTopology {
		if device.Status.NodeIdentity == nil {
			if device.Status.LegacyHandoff == nil {
				isolated, err := r.recoverIsolatedLegacyWorker(ctx, device)
				if err != nil {
					return managedTopologyResult{}, err
				}
				if isolated {
					return managedTopologyResult{LegacyWorker: true, NodeName: resolvedNodeName(device)}, nil
				}
				return managedTopologyResult{}, nil
			}
			return r.reconcileCompletedLegacyHandoff(ctx, device)
		}
		return r.reconcileLegacyWriterHandoff(ctx, device, "ManagedTopologyDisabled")
	}
	// A recorded reverse handoff is a consumed, manager-authenticated request.
	// Configuration rollback or selector re-entry cannot safely resurrect the
	// managed writer midway through the transfer. Finish the durable protocol;
	// a later forward enrollment is allowed only from Complete.
	if handoff := device.Status.LegacyHandoff; handoff != nil && handoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
		if device.Status.NodeIdentity == nil {
			return managedTopologyResult{}, fmt.Errorf("incomplete legacy writer handoff lost its managed Node identity")
		}
		return r.reconcileLegacyWriterHandoff(ctx, device, "LegacyHandoffInProgress")
	}
	if err := validateTopologyWorkerNodeName(device); err != nil {
		if device.Status.NodeIdentity != nil {
			result := managedTopologyResult{Managed: true, NodeName: resolvedNodeName(device)}
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"WorkerIdentityInvalid",
				err.Error(),
			)
		}
		return managedTopologyResult{}, err
	}
	policy, err := topologyrollout.BootstrapAdminPolicy(
		ctx,
		r.Client,
		r.APIReader,
		types.NamespacedName{Namespace: r.TopologyPolicyNamespace, Name: r.TopologyPolicyName},
	)
	if err != nil {
		if device.Status.NodeIdentity != nil {
			result := managedTopologyResult{Managed: true, NodeName: resolvedNodeName(device)}
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"TopologyPolicyUnavailable",
				err.Error(),
			)
		}
		return managedTopologyResult{}, fmt.Errorf("managed topology policy: %w", err)
	}

	selected := policy.Selector.Matches(labels.Set(device.Labels))
	wasManaged := device.Status.NodeIdentity != nil
	if selected && !wasManaged && device.Status.LegacyHandoff != nil {
		if device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
			return managedTopologyResult{}, fmt.Errorf("legacy writer handoff is incomplete in phase %q", device.Status.LegacyHandoff.Phase)
		}
		if request := strings.TrimSpace(device.Annotations[managedprotocol.AnnotationRequestLegacyHandoff]); request != "" {
			return managedTopologyResult{LegacyWorker: true, NodeName: device.Status.LegacyHandoff.NodeName},
				fmt.Errorf("remove completed %s request before re-enrolling the device into managed topology", managedprotocol.AnnotationRequestLegacyHandoff)
		}
	}
	if selected && !wasManaged {
		sharedAuthority, err := r.sharedWorkerAuthorityPresent(ctx)
		if err != nil {
			return managedTopologyResult{}, fmt.Errorf("verify shared-worker migration boundary: %w", err)
		}
		if sharedAuthority {
			// Phase zero migrates every old shared writer to a unique, owned
			// legacy identity before any device is admitted into managed mode.
			// This avoids a transition window where a still-valid shared token
			// can bypass per-node admission on a newly managed object.
			return managedTopologyResult{}, nil
		}
	}
	if !selected && !wasManaged {
		if device.Status.LegacyHandoff == nil {
			legacySA := topologyLegacyWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
				return managedTopologyResult{}, fmt.Errorf("provision isolated legacy worker access: %w", err)
			}
			// Marker-last makes interruption recoverable from exact owned
			// ServiceAccount/RBAC evidence without ever trusting marker alone.
			if err := r.ensureIsolatedLegacyWorkerMarker(ctx, device); err != nil {
				return managedTopologyResult{}, err
			}
			return managedTopologyResult{LegacyWorker: true, NodeName: resolvedNodeName(device)}, nil
		}
		return r.reconcileCompletedLegacyHandoff(ctx, device)
	}
	result := managedTopologyResult{Managed: true, NodeName: resolvedNodeName(device), Policy: policy}
	if !selected {
		return r.reconcileLegacyWriterHandoff(ctx, device, "ManagedFleetSelectorExit")
	}
	if err := topology.ValidateManagedMaxPods(int64(device.Spec.MaxPods)); err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyIncomplete,
			"ManagedCapacityInvalid",
			err.Error(),
		)
	}
	if _, err := r.validateManagedPhysicalIdentity(ctx, device, policy); err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict,
			"PhysicalIdentityConflict",
			err.Error(),
		)
	}
	if r.AggregatorEnabled {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict,
			"WorkerTopologyUnsupported",
			"managed topology currently requires a per-device worker; disable the config aggregator for this device fleet",
		)
	}

	sourceLabels, err := managedProjectionLabels(device, policy)
	if err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyIncomplete,
			"InvalidTopology",
			err.Error(),
		)
	}
	projectionSpec := device.Spec.DeepCopy()
	projectionSpec.Labels = sourceLabels
	projectionSpec.Region = ""
	projectionSpec.Zone = ""
	desired, err := configprovider.GetInitialNodeSpecWithTopologyMode(result.NodeName, projectionSpec, topology.ProjectionModeManaged)
	if err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict,
			"ProjectionRejected",
			err.Error(),
		)
	}

	projectionHash, err := topologyProjectionHash(desired.Labels)
	if err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict, "ProjectionHashFailed", err.Error())
	}
	if prior := device.Status.TopologyProjection; prior != nil && prior.EffectiveLabelHash != projectionHash {
		if lock := device.Status.TopologyLock; lock != nil {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"ReclassificationLocked",
				fmt.Sprintf("topology projection is locked by rollout %s/%s reservation %s until exact settlement", lock.CampaignNamespace, lock.CampaignName, lock.ReservationID),
			)
		}
		if session := device.Status.MaintenanceSession; session != nil && session.Phase != ciskov1.DeviceMaintenanceSessionSettled {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"ReclassificationMaintenanceActive",
				fmt.Sprintf("topology projection remains unchanged while maintenance session %s is %s", session.SessionToken, session.Phase),
			)
		}
		if device.Annotations[managedprotocol.AnnotationReclassifyFrom] != prior.EffectiveLabelHash {
			return r.failManagedTopology(ctx, device, result,
				ciskov1.CiscoDeviceConditionTopologyConflict,
				"ReclassificationApprovalRequired",
				fmt.Sprintf("topology changed from %s to %s; guard is retained until annotation %s names the prior hash", prior.EffectiveLabelHash, projectionHash, managedprotocol.AnnotationReclassifyFrom),
			)
		}
	}

	node, err := r.reserveManagedNode(ctx, device, desired.Labels, projectionHash)
	if err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionNodeIdentityReady,
			"NodeIdentityConflict",
			err.Error(),
		)
	}
	if err := r.ensureManagedWorkerLeases(ctx, device, node); err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict,
			"WorkerLeaseConflict",
			err.Error(),
		)
	}
	maintenanceDecision := r.resolveManagedMaintenance(ctx, device, node)
	if err := r.reconcileManagedNodeMetadata(ctx, device, node, desired.Labels, policy, projectionHash, maintenanceDecision.guard); err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict, "NodeProjectionFailed", err.Error())
	}
	if err := r.patchManagedTopologyStatus(ctx, device, node, projectionHash, maintenanceDecision); err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict, "TopologyStatusFailed", err.Error())
	}
	if maintenanceDecision.err != nil {
		return r.failManagedTopology(ctx, device, result,
			ciskov1.CiscoDeviceConditionTopologyConflict, "MaintenanceFenceFailed", maintenanceDecision.err.Error())
	}
	return result, nil
}

// failManagedTopology makes the scheduling guard the first invariant on every
// managed-path failure. Status is diagnostic; it must never be the only fence.
// Join preserves a guard write/CAS failure alongside the primary condition so
// reconciliation retries until both the taint and status are durable.
func (r *CiscoDeviceReconciler) failManagedTopology(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	result managedTopologyResult,
	conditionType, reason, message string,
) (managedTopologyResult, error) {
	guardErr := r.guardBoundNode(ctx, device)
	statusErr := r.patchTopologyFailure(ctx, device, conditionType, reason, message)
	return result, errors.Join(guardErr, statusErr)
}

func resolvedNodeName(device *ciskov1.CiscoDevice) string {
	if name := strings.TrimSpace(device.Spec.NodeName); name != "" {
		return name
	}
	return device.Name
}

// validateTopologyWorkerNodeName preserves an exact, lookup-free admission
// binding between the authenticated per-device ServiceAccount and the Node it
// may update. spec.nodeName already has the 63-byte boundary and DNS-subdomain
// grammar. The explicit check covers the legacy compatibility case where an
// omitted spec.nodeName inherits a CiscoDevice metadata.name of up to 253
// bytes, which cannot be losslessly embedded alongside protocol and
// incarnation identity in a ServiceAccount name of the same maximum size.
func validateTopologyWorkerNodeName(device *ciskov1.CiscoDevice) error {
	name := resolvedNodeName(device)
	if len(name) > 63 {
		return fmt.Errorf("managed topology requires resolved Node name %q to be a DNS subdomain of at most 63 bytes; recreate the CiscoDevice with an explicit spec.nodeName", name)
	}
	if problems := validation.IsDNS1123Subdomain(name); len(problems) != 0 {
		return fmt.Errorf("managed topology requires resolved Node name %q to be a DNS subdomain of at most 63 bytes: %s; recreate the CiscoDevice with an explicit spec.nodeName", name, strings.Join(problems, "; "))
	}
	return nil
}

func managedProjectionLabels(device *ciskov1.CiscoDevice, policy *topologyrollout.ParsedAdminPolicy) (map[string]string, error) {
	for _, key := range policy.Config.RequiredTopologyKeys {
		value, ok := device.Labels[key]
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("required topology label %q is missing", key)
		}
	}

	projectedKeys := make(map[string]struct{}, len(policy.Config.ProjectedTopologyKeys))
	for _, key := range policy.Config.ProjectedTopologyKeys {
		projectedKeys[key] = struct{}{}
	}

	// Preserve ordinary legacy spec.labels for one compatibility period. Stable
	// topology is different: it may be projected only through the administrator
	// allowlist and metadata labels remain the required authority.
	source := make(map[string]string, len(device.Spec.Labels)+len(projectedKeys))
	legacyTopology := map[string]string{}
	for key, value := range device.Spec.Labels {
		isTopology := key == corev1.LabelTopologyRegion || key == corev1.LabelTopologyZone ||
			strings.HasPrefix(key, topology.CiscoTopologyLabelPrefix)
		if !isTopology {
			source[key] = value
			continue
		}
		if _, allowed := projectedKeys[key]; !allowed {
			return nil, fmt.Errorf("legacy spec.labels[%q] is scheduling topology but is not in projectedTopologyKeys", key)
		}
		legacyTopology[key] = value
	}
	for key, value := range map[string]string{
		corev1.LabelTopologyRegion: device.Spec.Region,
		corev1.LabelTopologyZone:   device.Spec.Zone,
	} {
		if value == "" {
			continue
		}
		if _, allowed := projectedKeys[key]; !allowed {
			return nil, fmt.Errorf("legacy %s is set but that key is not in projectedTopologyKeys", key)
		}
		if prior := legacyTopology[key]; prior != "" && prior != value {
			return nil, fmt.Errorf("legacy %s sources conflict: %q and %q", key, prior, value)
		}
		legacyTopology[key] = value
	}

	for _, key := range policy.Config.ProjectedTopologyKeys {
		value := device.Labels[key]
		if problems := validation.IsValidLabelValue(value); len(problems) > 0 {
			return nil, fmt.Errorf("topology label %q is invalid: %s", key, strings.Join(problems, "; "))
		}
		if oldValue := legacyTopology[key]; oldValue != "" && oldValue != value {
			return nil, fmt.Errorf("legacy topology %s value %q conflicts with authoritative metadata label %q", key, oldValue, value)
		}
		source[key] = value
	}
	return source, nil
}

func topologyProjectionHash(projected map[string]string) (string, error) {
	data, err := json.Marshal(projected)
	if err != nil {
		return "", fmt.Errorf("encode topology projection: %w", err)
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (r *CiscoDeviceReconciler) reserveManagedNode(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	projected map[string]string,
	projectionHash string,
) (*corev1.Node, error) {
	name := resolvedNodeName(device)
	saName := managedWorkerServiceAccountName(device)
	workerUsername := fmt.Sprintf("system:serviceaccount:%s:%s", device.Namespace, saName)
	binding := map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
		managedprotocol.AnnotationWorkerUsername:  workerUsername,
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		managedprotocol.AnnotationProjectedKeys:   encodeStringSet(mapKeys(projected)),
		managedprotocol.AnnotationProjectionHash:  projectionHash,
	}
	desiredTaints := append([]corev1.Taint(nil), device.Spec.Taints...)
	desiredTaints = upsertTaint(desiredTaints, topologyInitializationTaint())
	binding[managedprotocol.AnnotationManagedTaints] = encodeManagedTaints(desiredTaints)

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: cloneLabels(projected), Annotations: binding}}
	node.Spec.Taints = desiredTaints
	if err := r.Create(ctx, node); err == nil {
		return r.bindCreatedNodeUID(ctx, node)
	} else if !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("reserve managed Node %q: %w", name, err)
	}

	if err := r.Get(ctx, types.NamespacedName{Name: name}, node); err != nil {
		return nil, fmt.Errorf("read existing Node %q: %w", name, err)
	}
	if managedNodeMatchesDevice(node, device) {
		if boundUID := node.Annotations[managedprotocol.AnnotationNodeUID]; boundUID != "" && boundUID != string(node.UID) {
			return nil, fmt.Errorf("Node %q binding names stale UID %q (current %q)", name, boundUID, node.UID)
		}
		if node.Annotations[managedprotocol.AnnotationNodeUID] == "" {
			return r.bindCreatedNodeUID(ctx, node)
		}
		return node, nil
	}
	handoffAdoption := completedLegacyHandoffMatchesNode(device, node)
	if device.Annotations[managedprotocol.AnnotationAdoptNodeUID] != string(node.UID) && !handoffAdoption {
		return nil, fmt.Errorf("Node %q already exists without this device binding; set %s=%s only after reviewing that exact legacy Node", name, managedprotocol.AnnotationAdoptNodeUID, node.UID)
	}
	if node.Labels[topology.LabelType] != topology.TypeVirtualKubelet {
		return nil, fmt.Errorf("Node %q is not an adoptable Cisco virtual-kubelet Node", name)
	}
	before := node.DeepCopy()
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	for key, value := range binding {
		node.Annotations[key] = value
	}
	node.Annotations[managedprotocol.AnnotationNodeUID] = string(node.UID)
	if err := r.Patch(ctx, node, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return nil, fmt.Errorf("adopt legacy Node %q: %w", name, err)
	}
	return node, nil
}

func (r *CiscoDeviceReconciler) bindCreatedNodeUID(ctx context.Context, node *corev1.Node) (*corev1.Node, error) {
	before := node.DeepCopy()
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[managedprotocol.AnnotationNodeUID] = string(node.UID)
	if err := r.Patch(ctx, node, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return nil, fmt.Errorf("bind managed Node %q UID: %w", node.Name, err)
	}
	return node, nil
}

func managedNodeMatchesDevice(node *corev1.Node, device *ciskov1.CiscoDevice) bool {
	return node.Annotations[managedprotocol.AnnotationManaged] == "true" &&
		node.Annotations[managedprotocol.AnnotationDeviceNamespace] == device.Namespace &&
		node.Annotations[managedprotocol.AnnotationDeviceName] == device.Name &&
		node.Annotations[managedprotocol.AnnotationDeviceUID] == string(device.UID)
}

func (r *CiscoDeviceReconciler) reconcileManagedNodeMetadata(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	desiredLabels map[string]string,
	policy *topologyrollout.ParsedAdminPolicy,
	projectionHash string,
	maintenanceGuard bool,
) error {
	before := node.DeepCopy()
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	ownedKeys := decodeStringSet(node.Annotations[managedprotocol.AnnotationProjectedKeys])
	for _, key := range policy.Config.ProjectedTopologyKeys {
		ownedKeys[key] = struct{}{}
	}
	for _, key := range []string{corev1.LabelHostname, topology.LabelPlatform, topology.LabelProvider, topology.LabelType} {
		ownedKeys[key] = struct{}{}
	}
	for key := range ownedKeys {
		if _, keep := desiredLabels[key]; !keep {
			delete(node.Labels, key)
		}
	}
	for key, value := range desiredLabels {
		node.Labels[key] = value
	}

	priorManagedTaints := decodeManagedTaints(node.Annotations[managedprotocol.AnnotationManagedTaints])
	for identity := range priorManagedTaints {
		node.Spec.Taints = deleteTaint(node.Spec.Taints, identity)
	}
	managedTaints := append([]corev1.Taint(nil), device.Spec.Taints...)
	projectionChanged := device.Status.TopologyProjection == nil || device.Status.TopologyProjection.EffectiveLabelHash != projectionHash
	physicalIdentity := managedPhysicalIdentityState(device, node)
	if projectionChanged || !managedWorkerReadyForProjection(node, device.Status.TopologyProjection) || !physicalIdentity.ready {
		managedTaints = upsertTaint(managedTaints, topologyInitializationTaint())
	}
	if maintenanceGuard {
		managedTaints = upsertTaint(managedTaints, maintenanceGuardTaint())
	}
	for _, taint := range managedTaints {
		node.Spec.Taints = upsertTaint(node.Spec.Taints, taint)
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	if err := sanitizeVirtualKubeletLastAppliedMetadata(node); err != nil {
		return fmt.Errorf("sanitize legacy Virtual Kubelet metadata for Node %q: %w", node.Name, err)
	}
	node.Annotations[managedprotocol.AnnotationManaged] = "true"
	node.Annotations[managedprotocol.AnnotationDeviceNamespace] = device.Namespace
	node.Annotations[managedprotocol.AnnotationDeviceName] = device.Name
	node.Annotations[managedprotocol.AnnotationDeviceUID] = string(device.UID)
	node.Annotations[managedprotocol.AnnotationNodeUID] = string(node.UID)
	node.Annotations[managedprotocol.AnnotationWorkerUsername] = fmt.Sprintf("system:serviceaccount:%s:%s", device.Namespace, managedWorkerServiceAccountName(device))
	node.Annotations[managedprotocol.AnnotationWorkerProtocol] = managedprotocol.Version
	node.Annotations[managedprotocol.AnnotationProjectedKeys] = encodeStringSet(mapKeys(desiredLabels))
	node.Annotations[managedprotocol.AnnotationProjectionHash] = projectionHash
	node.Annotations[managedprotocol.AnnotationManagedTaints] = encodeManagedTaints(managedTaints)
	if equalitySemanticNodeMetadata(before, node) {
		return nil
	}
	if err := r.Patch(ctx, node, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("project managed Node %q metadata: %w", node.Name, err)
	}
	return nil
}

// sanitizeVirtualKubeletLastAppliedMetadata completes the managed writer
// handoff. Upstream Virtual Kubelet uses this annotation as the old side of a
// three-way status patch; retaining labels from a legacy worker would make the
// new status-only worker attempt to delete the manager's projection.
func sanitizeVirtualKubeletLastAppliedMetadata(node *corev1.Node) error {
	if node == nil || node.Annotations == nil {
		return nil
	}
	if _, exists := node.Annotations[managedprotocol.VirtualKubeletLastAppliedObjectMeta]; !exists {
		return nil
	}
	safe := metav1.ObjectMeta{
		Namespace:   node.Namespace,
		Name:        node.Name,
		UID:         node.UID,
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return err
	}
	node.Annotations[managedprotocol.VirtualKubeletLastAppliedObjectMeta] = string(encoded)
	return nil
}

func equalitySemanticNodeMetadata(a, b *corev1.Node) bool {
	return mapsEqual(a.Labels, b.Labels) && mapsEqual(a.Annotations, b.Annotations) &&
		taintsEqual(a.Spec.Taints, b.Spec.Taints)
}

func (r *CiscoDeviceReconciler) patchManagedTopologyStatus(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	hash string,
	maintenance managedMaintenanceDecision,
) error {
	before := device.DeepCopy()
	physicalObservation := managedPhysicalIdentityState(device, node)
	physicalIdentity, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
	if err != nil {
		return fmt.Errorf("bind managed physical identity: %w", err)
	}
	applyManagedMaintenanceStatus(device, maintenance)
	sourceVersion := device.ResourceVersion
	lastSuccessful := metav1.NewTime(r.now())
	if current := device.Status.TopologyProjection; current != nil && current.EffectiveLabelHash == hash {
		sourceVersion = current.SourceResourceVersion
		lastSuccessful = current.LastSuccessfulTime
	}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		NodeName:         node.Name,
		NodeUID:          string(node.UID),
		DeviceUID:        string(device.UID),
		PhysicalIdentity: physicalIdentity,
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{
		EffectiveLabelHash:    hash,
		SourceResourceVersion: sourceVersion,
		LastSuccessfulTime:    lastSuccessful,
	}
	// A new managed binding completes the forward handoff from an isolated
	// legacy worker. Never erase an in-flight reverse protocol: configuration
	// rollback must finish that protocol before a fresh forward enrollment.
	if device.Status.LegacyHandoff != nil {
		if device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete {
			return fmt.Errorf("cannot replace legacy handoff in phase %q with a managed binding", device.Status.LegacyHandoff.Phase)
		}
		device.Status.LegacyHandoff = nil
	}
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionNodeIdentityReady, Status: metav1.ConditionTrue,
		Reason: "UIDBound", Message: "Node name and UID are bound to this CiscoDevice incarnation",
		ObservedGeneration: device.Generation,
	})
	projectionCurrent := before.Status.TopologyProjection != nil &&
		before.Status.TopologyProjection.EffectiveLabelHash == hash
	workerReady := projectionCurrent && managedWorkerReadyForProjection(node, before.Status.TopologyProjection)
	topologyReady := workerReady && physicalObservation.ready
	topologyReadyStatus := metav1.ConditionFalse
	topologyReadyReason := "ManagedWriterHandoffPending"
	topologyReadyMessage := "managed worker has not acknowledged the current topology projection"
	if topologyReady {
		topologyReadyStatus = metav1.ConditionTrue
		topologyReadyReason = "ProjectionComplete"
		topologyReadyMessage = "required topology is projected and the live physical identity is verified"
	} else if workerReady {
		topologyReadyReason = physicalObservation.reason
		topologyReadyMessage = physicalObservation.message
	}
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: topologyReadyStatus,
		Reason: topologyReadyReason, Message: truncateTopologyMessage(topologyReadyMessage),
		ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionTopologyIncomplete, Status: metav1.ConditionFalse,
		Reason: "ProjectionComplete", Message: "all required topology labels are present",
		ObservedGeneration: device.Generation,
	})
	conflictStatus := metav1.ConditionFalse
	conflictReason := "ProjectionComplete"
	conflictMessage := "no topology ownership conflict is present"
	if physicalObservation.conflict {
		conflictStatus = metav1.ConditionTrue
		conflictReason = physicalObservation.reason
		conflictMessage = physicalObservation.message
	}
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionTopologyConflict, Status: conflictStatus,
		Reason: conflictReason, Message: truncateTopologyMessage(conflictMessage),
		ObservedGeneration: device.Generation,
	})
	newBinding := before.Status.NodeIdentity == nil ||
		before.Status.NodeIdentity.DeviceUID != string(device.UID) ||
		before.Status.NodeIdentity.NodeUID != string(node.UID)
	if newBinding {
		// Status is not manager-only until NodeIdentity is established. Never
		// adopt a pre-binding health snapshot supplied through that compatibility
		// window, even if its source fields happen to match. The binding write
		// intentionally leaves health absent: a later reconciliation must observe
		// the now manager-owned sources before rollout admission can use them.
		device.Status.HealthObservation = nil
	} else {
		if err := refreshManagedHealthObservation(device, node, r.now(),
			ciskov1.CiscoDeviceConditionNodeIdentityReady,
			ciskov1.CiscoDeviceConditionTopologyReady,
		); err != nil {
			return fmt.Errorf("record managed health observation: %w", err)
		}
	}
	if statusesEqual(before.Status, device.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("record managed topology status: %w", err)
	}
	return nil
}

type deviceHealthDigest struct {
	Phase      string             `json:"phase"`
	Conditions []metav1.Condition `json:"conditions"`
}

func deviceConditionsHash(device *ciskov1.CiscoDevice) (string, error) {
	if device == nil {
		return "", fmt.Errorf("CiscoDevice is nil")
	}
	conditions := append([]metav1.Condition(nil), device.Status.Conditions...)
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].Type < conditions[j].Type })
	encoded, err := json.Marshal(deviceHealthDigest{Phase: device.Status.Phase, Conditions: conditions})
	if err != nil {
		return "", fmt.Errorf("encode device health conditions: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func refreshManagedHealthObservation(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	now time.Time,
	observedConditionTypes ...string,
) error {
	ready := managedNodeReadyCondition(node)
	if ready == nil || ready.LastHeartbeatTime.IsZero() {
		device.Status.HealthObservation = nil
		return nil
	}
	conditionsHash, err := deviceConditionsHash(device)
	if err != nil {
		return err
	}
	current := device.Status.HealthObservation
	if current == nil {
		current = &ciskov1.DeviceHealthObservationStatus{}
		device.Status.HealthObservation = current
	}
	changed := current.DeviceConditionsHash != conditionsHash ||
		!current.NodeReadyHeartbeatTime.Time.Equal(ready.LastHeartbeatTime.Time)
	if changed {
		current.NodeReadyHeartbeatTime = ready.LastHeartbeatTime
		current.DeviceConditionsHash = conditionsHash
	}
	if observeManagedDeviceConditions(device, now, observedConditionTypes...) {
		changed = true
	}
	if changed || current.ObservedAt.IsZero() {
		current.ObservedAt = metav1.NewTime(now)
	}
	return nil
}

const managedConditionProbeInterval = 30 * time.Second

func observeManagedDeviceConditions(device *ciskov1.CiscoDevice, now time.Time, conditionTypes ...string) bool {
	if device == nil || device.Status.HealthObservation == nil {
		return false
	}
	changed := false
	observations := device.Status.HealthObservation.ConditionObservations
	for _, conditionType := range conditionTypes {
		condition := meta.FindStatusCondition(device.Status.Conditions, conditionType)
		index := -1
		for i := range observations {
			if observations[i].Type == conditionType {
				index = i
				break
			}
		}
		if condition == nil {
			if index >= 0 {
				observations = append(observations[:index], observations[index+1:]...)
				changed = true
			}
			continue
		}
		desired := ciskov1.DeviceConditionObservationStatus{
			Type: condition.Type, Status: condition.Status,
			ObservedGeneration: condition.ObservedGeneration,
			ObservedAt:         metav1.NewTime(now),
		}
		if index >= 0 {
			existing := observations[index]
			sourceUnchanged := existing.Status == desired.Status &&
				existing.ObservedGeneration == desired.ObservedGeneration
			if sourceUnchanged && !existing.ObservedAt.IsZero() && !now.Before(existing.ObservedAt.Time) &&
				now.Sub(existing.ObservedAt.Time) < managedConditionProbeInterval {
				continue
			}
			observations[index] = desired
		} else {
			observations = append(observations, desired)
		}
		changed = true
	}
	if changed {
		sort.Slice(observations, func(i, j int) bool { return observations[i].Type < observations[j].Type })
		device.Status.HealthObservation.ConditionObservations = observations
	}
	return changed
}

func managedNodeReadyCondition(node *corev1.Node) *corev1.NodeCondition {
	if node == nil {
		return nil
	}
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeReady {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}

func statusesEqual(a, b ciskov1.DeviceStatus) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func (r *CiscoDeviceReconciler) patchTopologyFailure(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	conditionType, reason, message string,
) error {
	before := device.DeepCopy()
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: conditionType, Status: metav1.ConditionTrue, Reason: reason,
		Message: truncateTopologyMessage(message), ObservedGeneration: device.Generation,
	})
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionFalse,
		Reason: reason, Message: truncateTopologyMessage(message), ObservedGeneration: device.Generation,
	})
	if statusesEqual(before.Status, device.Status) {
		return fmt.Errorf("%s: %s", reason, message)
	}
	if err := r.Status().Patch(ctx, device,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("record managed topology failure: %w", err)
	}
	return fmt.Errorf("%s: %s", reason, message)
}

func (r *CiscoDeviceReconciler) guardBoundNode(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if device.Status.NodeIdentity == nil {
		return nil
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: device.Status.NodeIdentity.NodeName}, &node); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !managedNodeMatchesDevice(&node, device) || string(node.UID) != device.Status.NodeIdentity.NodeUID {
		return fmt.Errorf("cannot guard Node %q: its current UID or managed device binding differs from status", node.Name)
	}
	before := node.DeepCopy()
	node.Spec.Taints = upsertTaint(node.Spec.Taints, topologyInitializationTaint())
	if taintsEqual(before.Spec.Taints, node.Spec.Taints) {
		return nil
	}
	return r.Patch(ctx, &node, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func topologyInitializationTaint() corev1.Taint {
	return corev1.Taint{Key: managedprotocol.TopologyInitializingTaint, Value: "true", Effect: corev1.TaintEffectNoSchedule}
}

func nodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// managedWorkerReadyForProjection is the versioned writer-handoff proof. An
// adopted legacy Node may already be Ready, so readiness alone cannot permit
// scheduling. The protocol condition can be written only by the Node's bound
// status-only ServiceAccount, and its heartbeat must post-date establishment
// of the current projection to reject a stale condition after reclassification.
func managedWorkerReadyForProjection(node *corev1.Node, projection *ciskov1.DeviceTopologyProjectionStatus) bool {
	if node == nil || projection == nil || projection.LastSuccessfulTime.IsZero() || !nodeReady(node) {
		return false
	}
	for i := range node.Status.Conditions {
		condition := &node.Status.Conditions[i]
		if condition.Type != corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition) {
			continue
		}
		return condition.Status == corev1.ConditionTrue &&
			condition.Reason == managedprotocol.ManagedWorkerReadyReason &&
			!condition.LastHeartbeatTime.IsZero() &&
			!condition.LastHeartbeatTime.Time.Before(projection.LastSuccessfulTime.Time)
	}
	return false
}

func managedWorkerServiceAccountName(device *ciskov1.CiscoDevice) string {
	// Encode the resolved virtual Node name so native Pod-status admission can
	// bind this authenticated worker to oldObject.spec.nodeName without an API
	// lookup or a per-device admission object. Managed projection accepts only
	// DNS label values (at most 63 bytes), keeping the ServiceAccount name below
	// Kubernetes' DNS-subdomain limit. The final digest binds the identity to
	// this CiscoDevice incarnation, invalidating tokens after delete/recreate.
	nodeName := resolvedNodeName(device)
	if bound := device.Status.NodeIdentity; bound != nil &&
		bound.DeviceUID == string(device.UID) && bound.NodeName != "" {
		nodeName = bound.NodeName
	}
	digest := sha256.Sum256([]byte(string(device.UID)))
	return "cisco-vk-managed-" + nodeName + "-" + hex.EncodeToString(digest[:4])
}

func topologyLegacyWorkerServiceAccountName(device *ciskov1.CiscoDevice) string {
	nodeName := resolvedNodeName(device)
	digest := sha256.Sum256([]byte(string(device.UID)))
	return "cisco-vk-legacy-" + nodeName + "-" + hex.EncodeToString(digest[:4])
}

func encodeStringSet(keys []string) string {
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func decodeStringSet(encoded string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range strings.Split(encoded, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out[item] = struct{}{}
		}
	}
	return out
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func cloneLabels(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func encodeManagedTaints(taints []corev1.Taint) string {
	ids := make([]string, 0, len(taints))
	for _, taint := range taints {
		ids = append(ids, taintIdentity(taint))
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func decodeManagedTaints(encoded string) map[string]struct{} {
	return decodeStringSet(encoded)
}

func taintIdentity(taint corev1.Taint) string {
	return taint.Key + "|" + string(taint.Effect)
}

func upsertTaint(taints []corev1.Taint, desired corev1.Taint) []corev1.Taint {
	identity := taintIdentity(desired)
	out := make([]corev1.Taint, 0, len(taints)+1)
	for _, taint := range taints {
		if taintIdentity(taint) != identity {
			out = append(out, taint)
		}
	}
	return append(out, desired)
}

func deleteTaint(taints []corev1.Taint, identity string) []corev1.Taint {
	out := taints[:0]
	for _, taint := range taints {
		if taintIdentity(taint) != identity {
			out = append(out, taint)
		}
	}
	return out
}

func taintsEqual(a, b []corev1.Taint) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func truncateTopologyMessage(message string) string {
	const max = 512
	if len(message) <= max {
		return message
	}
	return message[:max]
}

// topologyRequeueInterval bounds retry pressure for invalid policy or
// migration inputs while still reacting promptly to a corrected object.
const topologyRequeueInterval = 15 * time.Second
