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
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
)

// resolveManagedDrainIntent establishes the manager-owned half of the drain
// handshake before a Pod teardown callback can acquire the canonical mutation
// Lease. The Lease is deliberately left idle: the durable CiscoDevice session
// fences new ordinary writes between callbacks, while each exact teardown owns
// the Lease only for the duration of its device mutation.
func (r *CiscoDeviceReconciler) resolveManagedDrainIntent(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	lease *coordv1.Lease,
) (managedMaintenanceDecision, bool) {
	leaf, found, err := r.managedDrainLeaf(ctx, device)
	if err != nil {
		return blockedMaintenanceDecision(device, err), true
	}
	if !found {
		return managedMaintenanceDecision{}, false
	}
	// Direct session establishment is the authorization boundary and therefore
	// requires the canonical mutation Lease to be wholly idle and request-free.
	// Held teardown/promotion requests use validateManagedDrainIntent below too,
	// but are separately authenticated by validateMaintenanceRequest.
	if lease.Spec.HolderIdentity != nil && strings.TrimSpace(*lease.Spec.HolderIdentity) != "" {
		return blockedMaintenanceDecision(device,
			fmt.Errorf("managed drain requires an idle canonical mutation Lease")), true
	}
	if lease.Spec.LeaseDurationSeconds != nil || lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil ||
		hasMaintenanceRequestAnnotations(lease.Annotations) {
		return blockedMaintenanceDecision(device,
			fmt.Errorf("managed drain requires a wholly idle, request-free canonical mutation Lease")), true
	}
	drain, err := validateManagedDrainIntent(device, node, lease, leaf)
	if err != nil {
		return blockedMaintenanceDecision(device, err), true
	}

	purpose := ciskov1.DeviceMaintenancePurposeWorkloadDrain
	if drain.State == opsv1alpha1.UpgradeManagerDrainPromoted || len(leaf.Status.ManagedMutationClaims) != 0 {
		purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
	}
	if current := device.Status.MaintenanceSession; current != nil &&
		current.SessionToken == drain.SessionToken &&
		current.Purpose == ciskov1.DeviceMaintenancePurposeSoftwareMutation {
		purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
	}

	// The Node metadata patch is completed before this status is published, so
	// Active is the manager's durable acknowledgement that both the cordon and
	// maintenance taint have been applied. The rollout controller must observe
	// this exact state before it may reserve drain authority or protect Pods.
	phase := ciskov1.DeviceMaintenanceSessionActive
	switch drain.State {
	case opsv1alpha1.UpgradeManagerDrainRecovering:
		phase = ciskov1.DeviceMaintenanceSessionRecovering
	case opsv1alpha1.UpgradeManagerDrainSettled:
		phase = ciskov1.DeviceMaintenanceSessionSettled
	}

	holder := devicecoordination.HolderIdentity("software-drain", leaf.Namespace, leaf.Name, string(leaf.UID))
	if purpose == ciskov1.DeviceMaintenancePurposeSoftwareMutation {
		holder = mutationguard.UpgradeHolderIdentity(leaf)
	}
	now := metav1.NewTime(r.now())
	session := &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: phase, ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1, Purpose: purpose,
		SessionToken: drain.SessionToken,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID),
			},
			Holder: holder,
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
		},
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		RequestedAt: drain.StartedAt, AcknowledgedAt: &now, ControlRevision: drain.ControlRevision,
		Message: "manager acknowledged the exact PDB-aware workload-drain session",
	}
	if current := device.Status.MaintenanceSession; current != nil && current.SessionToken == drain.SessionToken {
		if current.AcknowledgedAt != nil {
			session.AcknowledgedAt = current.AcknowledgedAt.DeepCopy()
		}
		if current.RequestedAt.Time.After(session.RequestedAt.Time) || current.ControlRevision > session.ControlRevision {
			return blockedMaintenanceDecision(device,
				fmt.Errorf("managed drain session retained a future request time or control revision")), true
		}
	}
	guard := phase != ciskov1.DeviceMaintenanceSessionRecovering && phase != ciskov1.DeviceMaintenanceSessionSettled
	decision := managedMaintenanceDecision{
		guard: guard, session: session, drain: drain.DeepCopy(),
		drainHold: device.Annotations[managedprotocol.AnnotationDrainCordonHold] == "true",
		status:    metav1.ConditionTrue,
		reason:    "DrainSessionAcknowledged", message: session.Message,
	}
	if phase == ciskov1.DeviceMaintenanceSessionRecovering {
		decision.reason = "DrainRecovering"
		decision.message = "drain side effects are closed; restoring only session-owned scheduling guards"
		session.Message = decision.message
	}
	if phase == ciskov1.DeviceMaintenanceSessionSettled {
		decision.reason = "DrainSettled"
		decision.message = "drain, device mutation, and workload recovery are settled"
		session.Message = decision.message
	}
	return decision, true
}

// resolveRetainedStaleDrainRecovery recognizes the one held-request state in
// which restoring a generic maintenance guard would prevent the worker from
// retiring its own retained quarantine. It does not acknowledge the current
// manager revision or authorize device access: the durable CiscoDevice session
// remains at the strictly older revision, while the drain-aware Node projector
// preserves the already-restored scheduling state. The provider must retire an
// expired exact Lease and then wait for a later manager acknowledgement before
// it can acquire or dispatch again.
func (r *CiscoDeviceReconciler) resolveRetainedStaleDrainRecovery(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	lease *coordv1.Lease,
	holder string,
) (managedMaintenanceDecision, bool) {
	current := device.Status.MaintenanceSession
	if current == nil || current.Phase != ciskov1.DeviceMaintenanceSessionRecovering ||
		current.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 ||
		current.Purpose != ciskov1.DeviceMaintenancePurposeWorkloadDrain ||
		current.AcknowledgedAt == nil || current.AcknowledgedAt.IsZero() ||
		current.RequestedAt.IsZero() || current.AcknowledgedAt.Before(&current.RequestedAt) ||
		current.DeviceUID != string(device.UID) || current.NodeName != node.Name ||
		current.NodeUID != string(node.UID) || current.Operation.Namespace != device.Namespace ||
		current.Operation.Name == "" || current.Operation.UID == "" || current.ControlRevision < 0 {
		return managedMaintenanceDecision{}, false
	}

	expectedHolder := devicecoordination.HolderIdentity(
		"software-drain", current.Operation.Namespace, current.Operation.Name, current.Operation.UID,
	)
	if holder != expectedHolder || current.Lease.Holder != expectedHolder || lease.UID == "" ||
		current.Lease.Namespace != lease.Namespace || current.Lease.Name != lease.Name ||
		current.Lease.UID != string(lease.UID) {
		return managedMaintenanceDecision{}, false
	}

	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.reader().Get(ctx, types.NamespacedName{
		Namespace: current.Operation.Namespace, Name: current.Operation.Name,
	}, &leaf); err != nil || string(leaf.UID) != current.Operation.UID {
		return managedMaintenanceDecision{}, false
	}
	drain, err := validateManagedDrainIntent(device, node, lease, &leaf)
	if err != nil || drain.State != opsv1alpha1.UpgradeManagerDrainRecovering ||
		drain.ControlRevision <= current.ControlRevision ||
		current.SessionToken != drain.SessionToken || !current.RequestedAt.Equal(&drain.StartedAt) ||
		drain.RecoveryDeadline == nil || drain.RecoveryDeadline.IsZero() ||
		!drain.RecoveryDeadline.After(drain.StartedAt.Time) ||
		leaf.Status.ManagerAdmission.ControlRevision == nil ||
		*leaf.Status.ManagerAdmission.ControlRevision != drain.ControlRevision {
		return managedMaintenanceDecision{}, false
	}

	// A crash is possible immediately before request publication, so wholly
	// absent metadata is unambiguous. Once any request key exists, require the
	// complete older request to bind this exact session and operation.
	if hasMaintenanceRequestAnnotations(lease.Annotations) {
		expectedRequest := map[string]string{
			managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.DrainProtocolVersion,
			managedprotocol.AnnotationMaintenanceSessionToken:    current.SessionToken,
			managedprotocol.AnnotationMaintenanceRequestedAt:     current.RequestedAt.Time.UTC().Format(time.RFC3339Nano),
			managedprotocol.AnnotationMaintenanceOperationNS:     current.Operation.Namespace,
			managedprotocol.AnnotationMaintenanceOperationName:   current.Operation.Name,
			managedprotocol.AnnotationMaintenanceOperationUID:    current.Operation.UID,
			managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(current.ControlRevision, 10),
			managedprotocol.AnnotationMaintenancePurpose:         managedprotocol.MaintenancePurposeWorkloadDrain,
		}
		for annotation, want := range expectedRequest {
			if lease.Annotations[annotation] != want {
				return managedMaintenanceDecision{}, false
			}
		}
	}

	wantUnschedulable := drain.NodeUnschedulableBefore ||
		device.Annotations[managedprotocol.AnnotationDrainCordonHold] == "true"
	if node.Spec.Unschedulable != wantUnschedulable ||
		hasDrainMaintenanceTaint(node.Spec.Taints) != drain.MaintenanceTaintPresentBefore ||
		node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" ||
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		return managedMaintenanceDecision{}, false
	}

	return managedMaintenanceDecision{
		guard: false, session: current.DeepCopy(), drain: drain.DeepCopy(),
		drainHold: device.Annotations[managedprotocol.AnnotationDrainCordonHold] == "true",
		status:    metav1.ConditionFalse,
		reason:    "StaleDrainRequestRetained",
		message: fmt.Sprintf(
			"exact drain request revision %d remains quarantined while recovery revision %d awaits Lease retirement and fresh acknowledgement",
			current.ControlRevision, drain.ControlRevision,
		),
	}, true
}

// managedDrainLeaf resolves new intent through the manager-owned topology lock
// and an existing session through its immutable operation reference. A lock
// for the normal BlockIfRunning path is not interpreted as drain intent.
func (r *CiscoDeviceReconciler) managedDrainLeaf(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (*opsv1alpha1.IOSXESoftwareUpgrade, bool, error) {
	if current := device.Status.MaintenanceSession; current != nil &&
		current.ProtocolVersion == ciskov1.DeviceMaintenanceProtocolPDBDrainV1 &&
		current.Phase != ciskov1.DeviceMaintenanceSessionSettled {
		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		key := types.NamespacedName{Namespace: current.Operation.Namespace, Name: current.Operation.Name}
		if err := r.reader().Get(ctx, key, &leaf); err != nil {
			return nil, true, fmt.Errorf("read active drain leaf: %w", err)
		}
		if string(leaf.UID) != current.Operation.UID {
			return nil, true, fmt.Errorf("active drain leaf incarnation changed")
		}
		return &leaf, true, nil
	}

	lock := device.Status.TopologyLock
	if lock == nil || lock.State != ciskov1.DeviceTopologyLockActive {
		return nil, false, nil
	}
	var rollout opsv1alpha1.IOSXESoftwareRollout
	if err := r.reader().Get(ctx, types.NamespacedName{
		Namespace: lock.CampaignNamespace, Name: lock.CampaignName,
	}, &rollout); err != nil {
		return nil, false, nil
	}
	if string(rollout.UID) != lock.CampaignUID || rollout.Status.FrozenPlan == nil ||
		rollout.Status.FrozenPlan.Hash != lock.PlanHash ||
		rollout.Spec.Plan.Workloads.Policy != opsv1alpha1.IOSXESoftwareRolloutWorkloadDrain {
		return nil, false, nil
	}
	for i := range rollout.Status.FrozenPlan.Targets {
		target := &rollout.Status.FrozenPlan.Targets[i]
		if target.DeviceUID != string(device.UID) {
			continue
		}
		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}, &leaf); err != nil {
			return nil, false, nil
		}
		if leaf.Status.ManagerDrain == nil {
			return nil, false, nil
		}
		return &leaf, true, nil
	}
	return nil, false, nil
}

func validateManagedDrainIntent(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	lease *coordv1.Lease,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) (*opsv1alpha1.UpgradeManagerDrainStatus, error) {
	if device == nil || node == nil || lease == nil || leaf == nil || device.Status.TopologyLock == nil ||
		device.Status.NodeIdentity == nil || leaf.Status.ManagerAdmission == nil ||
		leaf.Status.ManagerControl == nil || leaf.Status.ManagerDrain == nil {
		return nil, fmt.Errorf("managed drain intent identity is incomplete")
	}
	drain := leaf.Status.ManagerDrain
	admission := leaf.Status.ManagerAdmission
	lock := device.Status.TopologyLock
	parsed, err := uuid.Parse(drain.SessionToken)
	if err != nil || parsed.String() != drain.SessionToken || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		return nil, fmt.Errorf("managed drain session token is not a canonical UUIDv4")
	}
	if leaf.Namespace != device.Namespace || leaf.Spec.DeviceRef.Name != device.Name || leaf.UID == "" ||
		leaf.Annotations[managedprotocol.AnnotationManaged] != "true" ||
		leaf.Annotations[managedprotocol.AnnotationDeviceUID] != string(device.UID) ||
		leaf.Annotations[managedprotocol.AnnotationNodeUID] != string(node.UID) ||
		admission.LeafUID != string(leaf.UID) || admission.DeviceUID != string(device.UID) ||
		admission.NodeUID != string(node.UID) || admission.ReservationID != lock.ReservationID ||
		admission.PolicyEpoch != lock.PolicyEpoch || admission.TopologyLockID != lock.AcquisitionID ||
		drain.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		drain.ReservationID != admission.ReservationID || drain.PolicyEpoch != admission.PolicyEpoch ||
		drain.ControlRevision != leaf.Status.ManagerControl.Revision || drain.NodeUID != string(node.UID) ||
		drain.StartedAt.IsZero() || drain.DrainDeadline.IsZero() || drain.UpdatedAt.IsZero() ||
		!drain.DrainDeadline.After(drain.StartedAt.Time) || string(lease.UID) == "" {
		return nil, fmt.Errorf("managed drain intent does not match the leaf, topology lock, device, Node, and Lease incarnations")
	}
	if lock.DeviceUID != string(device.UID) || lock.DeviceGeneration != device.Generation ||
		lock.NodeUID != string(node.UID) || lock.CampaignUID != admission.CampaignUID ||
		lock.PlanHash != admission.PlanHash || lock.ReservationID != drain.ReservationID {
		return nil, fmt.Errorf("managed drain topology-lock binding is stale")
	}
	if current := device.Status.MaintenanceSession; current != nil && current.Phase != ciskov1.DeviceMaintenanceSessionSettled {
		if current.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 ||
			current.SessionToken != drain.SessionToken || current.Operation.UID != string(leaf.UID) ||
			current.DeviceUID != string(device.UID) || current.NodeUID != string(node.UID) ||
			current.Lease.UID != string(lease.UID) {
			return nil, fmt.Errorf("a different or stale maintenance session already owns the device")
		}
	}
	return drain, nil
}

// reconcileManagedDrainCordon applies or restores only the cordon owned by the
// exact drain session. An operator cordon appearing after the manager snapshot
// is never adopted, and an explicit hold converts the cordon back to operator
// ownership during recovery.
func reconcileManagedDrainCordon(node *corev1.Node, maintenance managedMaintenanceDecision) error {
	if node == nil || maintenance.drain == nil || maintenance.session == nil {
		return nil
	}
	token := maintenance.session.SessionToken
	owner := node.Annotations[managedprotocol.AnnotationDrainCordonOwner]
	active := maintenance.session.Phase != ciskov1.DeviceMaintenanceSessionRecovering &&
		maintenance.session.Phase != ciskov1.DeviceMaintenanceSessionSettled
	if active {
		if maintenance.drain.NodeUnschedulableBefore {
			node.Spec.Unschedulable = true
			if owner == token {
				delete(node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
			}
			return nil
		}
		if owner != "" && owner != token {
			return fmt.Errorf("Node cordon is owned by a different drain session")
		}
		if owner == "" && node.Spec.Unschedulable {
			return fmt.Errorf("Node became unschedulable after the drain snapshot; refusing to adopt operator state")
		}
		node.Spec.Unschedulable = true
		node.Annotations[managedprotocol.AnnotationDrainCordonOwner] = token
		return nil
	}

	if maintenance.drain.NodeUnschedulableBefore {
		if owner == token {
			delete(node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
		}
		return nil
	}
	if owner != token {
		// Missing or foreign ownership means the manager no longer has evidence
		// that it may clear the shared spec.unschedulable boolean.
		return nil
	}
	delete(node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
	if maintenance.drainHold {
		node.Spec.Unschedulable = true
		return nil
	}
	node.Spec.Unschedulable = false
	return nil
}

// reconcileManagedDrainTaint applies and restores the exact maintenance taint
// only while the immutable drain session owns it. The owner marker and taint
// are written in the same optimistic Node patch by reconcileManagedNodeMetadata.
// Therefore an unowned taint that appears after the manager's snapshot is
// operator state, not evidence that an interrupted manager patch succeeded.
func reconcileManagedDrainTaint(
	node *corev1.Node,
	priorManagedTaints map[string]struct{},
	managedTaints []corev1.Taint,
	maintenance managedMaintenanceDecision,
) ([]corev1.Taint, error) {
	if node == nil || maintenance.drain == nil || maintenance.session == nil {
		return managedTaints, nil
	}
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}

	taint := maintenanceGuardTaint()
	identity := taintIdentity(taint)
	owner := node.Annotations[managedprotocol.AnnotationDrainTaintOwner]
	token := maintenance.session.SessionToken
	active := maintenance.session.Phase != ciskov1.DeviceMaintenanceSessionRecovering &&
		maintenance.session.Phase != ciskov1.DeviceMaintenanceSessionSettled

	if active {
		if maintenance.drain.MaintenanceTaintPresentBefore {
			if owner != "" {
				return managedTaints, fmt.Errorf("pre-existing Node maintenance taint carries drain-session ownership")
			}
			if existing, found := taintByIdentity(node.Spec.Taints, identity); found {
				if existing.Value != taint.Value {
					return managedTaints, fmt.Errorf("pre-existing Node maintenance taint changed after the drain snapshot")
				}
				return managedTaints, nil
			}
			// The taint remains operator-owned. Reapply the exact snapshotted guard
			// if it disappeared, but never place it in the manager-owned set.
			node.Spec.Taints = upsertTaint(node.Spec.Taints, taint)
			return managedTaints, nil
		}

		if owner != "" && owner != token {
			return managedTaints, fmt.Errorf("Node maintenance taint is owned by a different drain session")
		}
		if existing, found := taintByIdentity(node.Spec.Taints, identity); owner == token && found && existing.Value != taint.Value {
			return managedTaints, fmt.Errorf("drain-owned Node maintenance taint changed during the active session")
		}
		if owner == "" {
			if _, previouslyOwned := priorManagedTaints[identity]; previouslyOwned {
				return managedTaints, fmt.Errorf("Node maintenance taint lost its drain-session ownership marker")
			}
			if _, found := taintByIdentity(node.Spec.Taints, identity); found {
				return managedTaints, fmt.Errorf("Node maintenance taint appeared after the drain snapshot; refusing to adopt operator state")
			}
			node.Annotations[managedprotocol.AnnotationDrainTaintOwner] = token
		}
		return upsertTaint(managedTaints, taint), nil
	}

	if owner != "" && owner != token {
		return managedTaints, fmt.Errorf("Node maintenance taint is owned by a different drain session")
	}
	if maintenance.drain.MaintenanceTaintPresentBefore {
		if owner != "" {
			return managedTaints, fmt.Errorf("pre-existing Node maintenance taint unexpectedly carries drain-session ownership")
		}
		if existing, found := taintByIdentity(node.Spec.Taints, identity); found {
			if existing.Value != taint.Value {
				return managedTaints, fmt.Errorf("pre-existing Node maintenance taint changed before recovery")
			}
			return managedTaints, nil
		}
		node.Spec.Taints = upsertTaint(node.Spec.Taints, taint)
		return managedTaints, nil
	}
	if owner == token {
		if existing, found := taintByIdentity(node.Spec.Taints, identity); found && existing.Value != taint.Value {
			return managedTaints, fmt.Errorf("drain-owned Node maintenance taint changed before recovery")
		}
		node.Spec.Taints = deleteTaint(node.Spec.Taints, identity)
		delete(node.Annotations, managedprotocol.AnnotationDrainTaintOwner)
	}
	// With no matching owner, preserve any late operator taint. The rollout's
	// restored-state check will remain closed until the operator resolves it.
	return managedTaints, nil
}

func taintByIdentity(taints []corev1.Taint, identity string) (corev1.Taint, bool) {
	for _, taint := range taints {
		if taintIdentity(taint) == identity {
			return taint, true
		}
	}
	return corev1.Taint{}, false
}
