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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
)

type managedMaintenanceDecision struct {
	guard     bool
	session   *ciskov1.DeviceMaintenanceSessionStatus
	drain     *opsv1alpha1.UpgradeManagerDrainStatus
	drainHold bool
	status    metav1.ConditionStatus
	reason    string
	message   string
	err       error
}

const maxManagedMutationLeaseSeconds = int32((7*24*time.Hour + 26*time.Hour) / time.Second)

func (r *CiscoDeviceReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func (r *CiscoDeviceReconciler) ensureManagedMutationLease(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
) error {
	namespace := r.LeaseNamespace
	if namespace == "" {
		namespace = device.Namespace
	}
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	key := types.NamespacedName{
		Namespace: namespace,
		Name:      engine.LeaseName(deviceKey, devicecoordination.MutationLeaseFamily),
	}
	worker := managedNetworkWorkerUsername(node)
	if device.UID == "" || node.UID == "" || worker == "" {
		return fmt.Errorf("managed Node identity/worker binding is incomplete before mutation Lease creation")
	}
	desiredAnnotations, desiredLabels := managedMutationLeaseMetadata(device, node.Name, string(node.UID), worker)
	copyManagedWorkerBindingAnnotations(desiredAnnotations, node.Annotations)
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: key.Namespace, Name: key.Name,
		Labels:      desiredLabels,
		Annotations: desiredAnnotations,
	}}
	if err := r.Create(ctx, lease); err == nil {
		if err := validateManagedMutationLeaseMetadata(lease, desiredAnnotations, desiredLabels); err != nil {
			return fmt.Errorf("new managed mutation Lease %s is unsafe to use: %w", key, err)
		}
		return nil
	} else if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("pre-create managed mutation Lease %s: %w", key, err)
	}
	if err := r.reader().Get(ctx, key, lease); err != nil {
		return fmt.Errorf("read managed mutation Lease %s: %w", key, err)
	}
	if lease.Annotations[managedprotocol.AnnotationManaged] == "true" {
		if err := r.repairManagedLeaseBindings(ctx, lease, desiredAnnotations, desiredLabels, nil); err != nil {
			return fmt.Errorf("managed mutation Lease %s is unsafe to use: %w", key, err)
		}
		return nil
	}
	// A legacy empty canonical Lease is safe to adopt during the explicit
	// managed-mode writer handoff. Require a wholly idle, unowned object rather
	// than interpreting stale/future timing fields or partial request metadata.
	if err := validateLegacyMutationLeaseAdoption(lease, desiredAnnotations, desiredLabels); err != nil {
		return fmt.Errorf("legacy mutation Lease %s cannot be adopted: %w", key, err)
	}
	before := lease.DeepCopy()
	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	for annotation, value := range desiredAnnotations {
		lease.Annotations[annotation] = value
	}
	if lease.Labels == nil {
		lease.Labels = map[string]string{}
	}
	lease.Labels["cisco.vk/device"] = deviceKey
	lease.Labels["cisco.vk/family"] = devicecoordination.MutationLeaseFamily
	if err := r.Patch(ctx, lease, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("adopt empty managed mutation Lease %s: %w", key, err)
	}
	return nil
}

func managedMutationLeaseMetadata(
	device *ciskov1.CiscoDevice,
	nodeName, nodeUID, worker string,
) (map[string]string, map[string]string) {
	deviceKey := devicecoordination.DeviceKey(device.Namespace, device.Name)
	return managedLeaseBindingAnnotations(
		device, nodeName, nodeUID, worker, managedprotocol.LeasePurposeDeviceMutation,
	), managedLeaseLabels(deviceKey, devicecoordination.MutationLeaseFamily)
}

func validateManagedMutationLeaseMetadata(
	lease *coordv1.Lease,
	expectedAnnotations, expectedLabels map[string]string,
) error {
	return validateManagedBoundLeaseMetadata(lease, expectedAnnotations, expectedLabels, nil)
}

func validateLegacyMutationLeaseAdoption(
	lease *coordv1.Lease,
	managedBindings map[string]string,
	managedLabels map[string]string,
) error {
	if lease == nil {
		return fmt.Errorf("Lease is nil")
	}
	if !lease.DeletionTimestamp.IsZero() {
		return fmt.Errorf("deletion is in progress")
	}
	if len(lease.OwnerReferences) != 0 || len(lease.Finalizers) != 0 {
		return fmt.Errorf("ownerReferences and finalizers must be empty")
	}
	if lease.Spec.HolderIdentity != nil && strings.TrimSpace(*lease.Spec.HolderIdentity) != "" {
		return fmt.Errorf("holderIdentity is active")
	}
	if lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil || lease.Spec.LeaseDurationSeconds != nil || lease.Spec.LeaseTransitions != nil {
		return fmt.Errorf("Lease timing and transition fields must be absent")
	}
	for annotation := range managedBindings {
		if _, exists := lease.Annotations[annotation]; exists {
			return fmt.Errorf("partial managed binding annotation %s is present", annotation)
		}
	}
	for label, expected := range managedLabels {
		if actual, exists := lease.Labels[label]; exists && actual != expected {
			return fmt.Errorf("reserved identity label %s=%q, want %q", label, actual, expected)
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
		managedprotocol.AnnotationMaintenancePurpose,
	} {
		if _, exists := lease.Annotations[annotation]; exists {
			return fmt.Errorf("maintenance request annotation %s is present", annotation)
		}
	}
	return nil
}

func (r *CiscoDeviceReconciler) resolveManagedMaintenance(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
) managedMaintenanceDecision {
	decision := managedMaintenanceDecision{
		status: metav1.ConditionTrue, reason: "Idle", message: "no managed maintenance session is active",
	}
	leaseNamespace := r.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	leaseKey := types.NamespacedName{
		Namespace: leaseNamespace,
		Name: engine.LeaseName(
			devicecoordination.DeviceKey(device.Namespace, device.Name),
			devicecoordination.MutationLeaseFamily,
		),
	}
	var lease coordv1.Lease
	err := r.reader().Get(ctx, leaseKey, &lease)
	if client.IgnoreNotFound(err) != nil {
		return blockedMaintenanceDecision(device, fmt.Errorf("read mutation Lease: %w", err))
	}
	if err != nil {
		return r.retainOrSettleMaintenance(ctx, device, node, nil)
	}
	expectedAnnotations, expectedLabels := managedMutationLeaseMetadata(
		device, node.Name, string(node.UID), managedNetworkWorkerUsername(node),
	)
	if err := validateManagedMutationLeaseMetadata(&lease, expectedAnnotations, expectedLabels); err != nil {
		return blockedMaintenanceDecision(device, fmt.Errorf("mutation Lease binding is invalid: %w", err))
	}

	holder := ""
	if lease.Spec.HolderIdentity != nil {
		holder = strings.TrimSpace(*lease.Spec.HolderIdentity)
	}
	if err := validateManagedMutationLeaseSpec(&lease.Spec, holder); err != nil {
		return blockedMaintenanceDecision(device, fmt.Errorf("mutation Lease state is invalid: %w", err))
	}
	if holder == "" || strings.HasPrefix(holder, "device-write/") {
		if hasMaintenanceRequestAnnotations(lease.Annotations) {
			return blockedMaintenanceDecision(device,
				fmt.Errorf("idle or routine-write mutation Lease retains maintenance request metadata"))
		}
		// Establishing a drain session is an authorization boundary. A routine
		// writer that won the canonical Lease immediately before the drain intent
		// was published must finish first; the next reconciliation can then prove
		// a wholly idle Lease before it applies any scheduling guard or publishes
		// an Active drain session.
		if holder == "" {
			if drainDecision, handled := r.resolveManagedDrainIntent(ctx, device, node, &lease); handled {
				return drainDecision
			}
		}
		return r.retainOrSettleMaintenance(ctx, device, node, &lease)
	}
	// Any disruptive holder fences scheduling, even if its request is missing
	// or malformed. Only a fully validated request receives acknowledgement.
	decision.guard = true
	session, requestErr := r.validateMaintenanceRequest(ctx, device, node, &lease, holder)
	if requestErr != nil {
		if staleRecovery, handled := r.resolveRetainedStaleDrainRecovery(
			ctx, device, node, &lease, holder,
		); handled {
			return staleRecovery
		}
		decision.status = metav1.ConditionFalse
		decision.reason = "RequestRejected"
		decision.message = truncateTopologyMessage(requestErr.Error())
		decision.err = requestErr
		decision.session = device.Status.MaintenanceSession.DeepCopy()
		return decision
	}
	if current := device.Status.MaintenanceSession; current != nil && current.Phase != ciskov1.DeviceMaintenanceSessionSettled &&
		current.SessionToken != session.SessionToken {
		return blockedMaintenanceDecision(device,
			fmt.Errorf("active maintenance session %q prevents acknowledgement of request %q", current.SessionToken, session.SessionToken))
	}
	if current := device.Status.MaintenanceSession; current != nil && current.SessionToken == session.SessionToken &&
		current.AcknowledgedAt != nil {
		leaseIdentityChanged := current.Lease.Namespace != session.Lease.Namespace ||
			current.Lease.Name != session.Lease.Name || current.Lease.UID != session.Lease.UID
		validDrainPromotion := current.ProtocolVersion == ciskov1.DeviceMaintenanceProtocolPDBDrainV1 &&
			current.Purpose == ciskov1.DeviceMaintenancePurposeWorkloadDrain &&
			session.ProtocolVersion == ciskov1.DeviceMaintenanceProtocolPDBDrainV1 &&
			session.Purpose == ciskov1.DeviceMaintenancePurposeSoftwareMutation &&
			current.Lease.Holder == devicecoordination.HolderIdentity("software-drain", session.Operation.Namespace,
				session.Operation.Name, session.Operation.UID) &&
			session.Lease.Holder == "software-upgrade/"+session.Operation.UID
		protocolOrPurposeChanged := current.ProtocolVersion != session.ProtocolVersion ||
			(current.Purpose != session.Purpose && !validDrainPromotion)
		if leaseIdentityChanged || (current.Lease.Holder != session.Lease.Holder && !validDrainPromotion) ||
			protocolOrPurposeChanged || current.Operation != session.Operation ||
			current.DeviceUID != session.DeviceUID || current.NodeName != session.NodeName || current.NodeUID != session.NodeUID ||
			!current.RequestedAt.Equal(&session.RequestedAt) || current.ControlRevision > session.ControlRevision {
			return blockedMaintenanceDecision(device, fmt.Errorf("maintenance session token was reused with a different binding or stale control revision"))
		}
		session.AcknowledgedAt = current.AcknowledgedAt.DeepCopy()
	}
	if session.ProtocolVersion == ciskov1.DeviceMaintenanceProtocolPDBDrainV1 {
		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.reader().Get(ctx, types.NamespacedName{
			Namespace: session.Operation.Namespace, Name: session.Operation.Name,
		}, &leaf); err != nil {
			return blockedMaintenanceDecision(device, fmt.Errorf("read acknowledged drain state: %w", err))
		}
		if leaf.Status.ManagerDrain == nil {
			return blockedMaintenanceDecision(device, fmt.Errorf("acknowledged drain leaf lost manager drain state"))
		}
		decision.drain = leaf.Status.ManagerDrain.DeepCopy()
		decision.guard = session.Phase != ciskov1.DeviceMaintenanceSessionRecovering &&
			session.Phase != ciskov1.DeviceMaintenanceSessionSettled
	}
	decision.session = session
	decision.status = metav1.ConditionTrue
	decision.reason = "GuardAcknowledged"
	decision.message = "manager applied and verified the Node maintenance guard for the exact Lease request"
	return decision
}

func blockedMaintenanceDecision(device *ciskov1.CiscoDevice, err error) managedMaintenanceDecision {
	decision := managedMaintenanceDecision{
		guard: true, status: metav1.ConditionFalse, reason: "MaintenanceBlocked",
		message: truncateTopologyMessage(err.Error()), err: err,
	}
	if device != nil && device.Status.MaintenanceSession != nil {
		decision.session = device.Status.MaintenanceSession.DeepCopy()
	}
	return decision
}

// managedCancellationWorkerRecoveryReady recognizes the only maintenance
// failure for which replacing the managed worker is useful and safe. A
// promoted pdb-drain request can retain its immutable pre-cancel Lease revision
// while manager admission/control advance to cancellation and recovery. With
// no claim or durable mutation marker, the replacement worker may do exactly
// enough to acknowledge that cancellation and release the unused exact Lease.
//
// This is deliberately independent of the rejected request validation above:
// the old request must remain rejected and must never be rewritten to look like
// the newer cancellation revision.
func (r *CiscoDeviceReconciler) managedCancellationWorkerRecoveryReady(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
) (bool, error) {
	// Aggregator-owned config drivers intentionally have no per-device worker
	// substrate to repair. Never let this exception enter the aggregator
	// handover path, which has broader authority than cancellation recovery.
	if device != nil && r.AggregatorEnabled && drivers.ConfigDriverRegistered(device.Spec.Driver) {
		return false, nil
	}
	if device == nil || node == nil || device.Status.NodeIdentity == nil ||
		device.Status.TopologyLock == nil || device.Status.MaintenanceSession == nil {
		return false, nil
	}
	session := device.Status.MaintenanceSession
	if (session.Phase != ciskov1.DeviceMaintenanceSessionActive &&
		session.Phase != ciskov1.DeviceMaintenanceSessionRecovering) ||
		session.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 ||
		session.Purpose != ciskov1.DeviceMaintenancePurposeSoftwareMutation ||
		session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() ||
		session.AcknowledgedAt.Before(&session.RequestedAt) || session.ControlRevision < 0 ||
		session.SessionToken == "" || session.RequestedAt.IsZero() ||
		session.DeviceUID != string(device.UID) || session.NodeName != node.Name ||
		session.NodeUID != string(node.UID) || session.Operation.Namespace != device.Namespace ||
		session.Operation.Name == "" || session.Operation.UID == "" ||
		device.Status.TopologyLock.State != ciskov1.DeviceTopologyLockActive {
		return false, nil
	}

	leaseNamespace := r.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = device.Namespace
	}
	leaseKey := types.NamespacedName{
		Namespace: leaseNamespace,
		Name: engine.LeaseName(
			devicecoordination.DeviceKey(device.Namespace, device.Name),
			devicecoordination.MutationLeaseFamily,
		),
	}
	if session.Lease.Namespace != leaseKey.Namespace || session.Lease.Name != leaseKey.Name ||
		session.Lease.UID == "" || session.Lease.Holder == "" {
		return false, nil
	}
	var lease coordv1.Lease
	if err := r.reader().Get(ctx, leaseKey, &lease); err != nil {
		return false, fmt.Errorf("read cancelled maintenance recovery Lease: %w", err)
	}
	expectedAnnotations, expectedLabels := managedMutationLeaseMetadata(
		device, node.Name, string(node.UID), managedNetworkWorkerUsername(node),
	)
	if validateManagedMutationLeaseMetadata(&lease, expectedAnnotations, expectedLabels) != nil ||
		string(lease.UID) != session.Lease.UID || lease.Spec.HolderIdentity == nil ||
		*lease.Spec.HolderIdentity != session.Lease.Holder ||
		validateManagedMutationLeaseSpec(&lease.Spec, session.Lease.Holder) != nil ||
		!r.now().Before(lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second)) {
		return false, nil
	}

	annotations := lease.Annotations
	if annotations[managedprotocol.AnnotationMaintenanceRequestVersion] != managedprotocol.DrainProtocolVersion ||
		annotations[managedprotocol.AnnotationMaintenancePurpose] != managedprotocol.MaintenancePurposeSoftwareMutation ||
		annotations[managedprotocol.AnnotationMaintenanceSessionToken] != session.SessionToken ||
		annotations[managedprotocol.AnnotationMaintenanceOperationNS] != session.Operation.Namespace ||
		annotations[managedprotocol.AnnotationMaintenanceOperationName] != session.Operation.Name ||
		annotations[managedprotocol.AnnotationMaintenanceOperationUID] != session.Operation.UID {
		return false, nil
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
	if err != nil || !requestedAt.Equal(session.RequestedAt.Time) {
		return false, nil
	}
	requestRevision, err := strconv.ParseInt(
		annotations[managedprotocol.AnnotationMaintenanceControlRevision], 10, 64,
	)
	if err != nil || requestRevision != session.ControlRevision {
		return false, nil
	}

	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.reader().Get(ctx, types.NamespacedName{
		Namespace: session.Operation.Namespace, Name: session.Operation.Name,
	}, &leaf); err != nil {
		return false, fmt.Errorf("read cancelled maintenance recovery leaf: %w", err)
	}
	if string(leaf.UID) != session.Operation.UID ||
		mutationguard.UpgradeHolderIdentity(&leaf) != session.Lease.Holder {
		return false, nil
	}
	drain, err := validateManagedDrainIntent(device, node, &lease, &leaf)
	if err != nil {
		return false, nil
	}
	admission := leaf.Status.ManagerAdmission
	control := leaf.Status.ManagerControl
	if admission == nil || control == nil || drain == nil ||
		admission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
		admission.ControlRevision == nil || *admission.ControlRevision != control.Revision ||
		!control.Cancel || control.Revision <= requestRevision ||
		drain.State != opsv1alpha1.UpgradeManagerDrainRecovering ||
		drain.ControlRevision != control.Revision || drain.SessionToken != session.SessionToken ||
		!drain.StartedAt.Equal(&session.RequestedAt) || len(leaf.Status.ManagedMutationClaims) != 0 ||
		mutationguard.UpgradeMutationSubmitted(&leaf) {
		return false, nil
	}
	for annotation, expected := range map[string]string{
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationNodeName:        node.Name,
		managedprotocol.AnnotationWorkerUsername:  managedNetworkWorkerUsername(node),
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		managedprotocol.AnnotationCampaignUID:     admission.CampaignUID,
		managedprotocol.AnnotationPlanHash:        admission.PlanHash,
		managedprotocol.AnnotationLedgerUID:       admission.LedgerUID,
		managedprotocol.AnnotationReservationID:   admission.ReservationID,
	} {
		if expected == "" || leaf.Annotations[annotation] != expected {
			return false, nil
		}
	}
	return true, nil
}

func (r *CiscoDeviceReconciler) retainOrSettleMaintenance(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	_ *coordv1.Lease,
) managedMaintenanceDecision {
	current := device.Status.MaintenanceSession
	if current == nil || current.Phase == ciskov1.DeviceMaintenanceSessionSettled {
		return managedMaintenanceDecision{status: metav1.ConditionTrue, reason: "Idle", message: "no managed maintenance session is active", session: current.DeepCopy()}
	}
	decision := managedMaintenanceDecision{guard: true, session: current.DeepCopy(), status: metav1.ConditionFalse,
		reason: "OutcomeUnresolved", message: "maintenance guard retained until the manager settles the exact operation outcome"}
	if current.DeviceUID != string(device.UID) || current.NodeName != node.Name || current.NodeUID != string(node.UID) ||
		current.Operation.Namespace != device.Namespace {
		decision.err = fmt.Errorf("maintenance session does not bind the current device and Node incarnation")
		return decision
	}
	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	key := types.NamespacedName{Namespace: current.Operation.Namespace, Name: current.Operation.Name}
	if err := r.reader().Get(ctx, key, &leaf); err != nil {
		decision.err = fmt.Errorf("read active maintenance operation: %w", err)
		decision.message = truncateTopologyMessage(decision.err.Error())
		return decision
	}
	if string(leaf.UID) != current.Operation.UID || leaf.Status.ManagerAdmission == nil ||
		leaf.Spec.DeviceRef.Name != device.Name || leaf.Status.ManagerAdmission.LeafUID != string(leaf.UID) ||
		leaf.Status.ManagerAdmission.DeviceUID != string(device.UID) || leaf.Status.ManagerAdmission.NodeUID != string(node.UID) ||
		leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled || !terminalManagedLeaf(leaf.Status.Phase) {
		return decision
	}
	settled := current.DeepCopy()
	settled.Phase = ciskov1.DeviceMaintenanceSessionSettled
	settled.Message = "manager settled the operation outcome and released its reservation"
	decision.guard = false
	decision.session = settled
	decision.status = metav1.ConditionTrue
	decision.reason = "SessionSettled"
	decision.message = settled.Message
	return decision
}

func (r *CiscoDeviceReconciler) validateMaintenanceRequest(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	lease *coordv1.Lease,
	holder string,
) (*ciskov1.DeviceMaintenanceSessionStatus, error) {
	if err := validateManagedMutationLeaseSpec(&lease.Spec, holder); err != nil {
		return nil, fmt.Errorf("maintenance request mutation Lease state is invalid: %w", err)
	}
	if lease.UID == "" || !r.now().Before(lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds)*time.Second)) {
		return nil, fmt.Errorf("maintenance request requires a current, UID-bound mutation Lease")
	}
	annotations := lease.Annotations
	required := map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
		managedprotocol.AnnotationNodeName:        node.Name,
		managedprotocol.AnnotationNodeUID:         string(node.UID),
		managedprotocol.AnnotationWorkerUsername:  managedNetworkWorkerUsername(node),
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
		managedprotocol.AnnotationLeasePurpose:    managedprotocol.LeasePurposeDeviceMutation,
		devicecoordination.RetainLeaseAnnotation:  "true",
	}
	for _, key := range []string{
		managedprotocol.AnnotationAppWorkerUsername,
		managedprotocol.AnnotationAppWorkerPodName,
		managedprotocol.AnnotationAppWorkerPodUID,
		managedprotocol.AnnotationNetworkWorkerUsername,
		managedprotocol.AnnotationNetworkWorkerPodName,
		managedprotocol.AnnotationNetworkWorkerPodUID,
	} {
		required[key] = node.Annotations[key]
	}
	for key, want := range required {
		if strings.TrimSpace(want) == "" || annotations[key] != want {
			return nil, fmt.Errorf("maintenance Lease annotation %s=%q, want %q", key, annotations[key], want)
		}
	}
	token := annotations[managedprotocol.AnnotationMaintenanceSessionToken]
	parsedToken, tokenErr := uuid.Parse(token)
	if tokenErr != nil || parsedToken.String() != token {
		return nil, fmt.Errorf("maintenance request has an invalid session token")
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
	if err != nil || requestedAt.After(r.now().Add(5*time.Minute)) {
		return nil, fmt.Errorf("maintenance request has an invalid requested-at timestamp")
	}
	revision, err := strconv.ParseInt(annotations[managedprotocol.AnnotationMaintenanceControlRevision], 10, 64)
	if err != nil || revision < 0 {
		return nil, fmt.Errorf("maintenance request has an invalid control revision")
	}
	operation := ciskov1.DeviceMaintenanceObjectReference{
		Namespace: annotations[managedprotocol.AnnotationMaintenanceOperationNS],
		Name:      annotations[managedprotocol.AnnotationMaintenanceOperationName],
		UID:       annotations[managedprotocol.AnnotationMaintenanceOperationUID],
	}
	if operation.Namespace != device.Namespace || operation.Name == "" || operation.UID == "" {
		return nil, fmt.Errorf("maintenance request operation identity is incomplete or cross-namespace")
	}

	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: operation.Namespace, Name: operation.Name}, &leaf); err != nil {
		return nil, fmt.Errorf("read requested software-upgrade leaf: %w", err)
	}
	if string(leaf.UID) != operation.UID || holder != mutationguard.UpgradeHolderIdentity(&leaf) {
		if annotations[managedprotocol.AnnotationMaintenanceRequestVersion] != managedprotocol.DrainProtocolVersion ||
			holder != devicecoordination.HolderIdentity("software-drain", leaf.Namespace, leaf.Name, string(leaf.UID)) {
			return nil, fmt.Errorf("maintenance Lease holder does not bind the exact software-upgrade leaf UID")
		}
	}
	protocol := ciskov1.DeviceMaintenanceProtocolVersion("")
	purpose := ciskov1.DeviceMaintenancePurpose("")
	requestVersion := annotations[managedprotocol.AnnotationMaintenanceRequestVersion]
	requestPurpose := annotations[managedprotocol.AnnotationMaintenancePurpose]
	switch requestVersion {
	case managedprotocol.Version:
		if requestPurpose != "" && requestPurpose != managedprotocol.MaintenancePurposeSoftwareMutation {
			return nil, fmt.Errorf("rollout-v1 maintenance request has invalid purpose %q", requestPurpose)
		}
		if holder != mutationguard.UpgradeHolderIdentity(&leaf) {
			return nil, fmt.Errorf("software-mutation request has a non-upgrade Lease holder")
		}
		if err := validateMaintenanceLeafBinding(device, node, &leaf, revision); err != nil {
			return nil, err
		}
		if requestPurpose != "" {
			protocol = ciskov1.DeviceMaintenanceProtocolRolloutV1
			purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
		}
	case managedprotocol.DrainProtocolVersion:
		drain, err := validateManagedDrainIntent(device, node, lease, &leaf)
		if err != nil {
			return nil, err
		}
		if token != drain.SessionToken || revision != drain.ControlRevision ||
			!requestedAt.Equal(drain.StartedAt.Time) {
			return nil, fmt.Errorf("drain maintenance request does not match its immutable session")
		}
		protocol = ciskov1.DeviceMaintenanceProtocolPDBDrainV1
		switch requestPurpose {
		case managedprotocol.MaintenancePurposeWorkloadDrain:
			purpose = ciskov1.DeviceMaintenancePurposeWorkloadDrain
			if holder != devicecoordination.HolderIdentity("software-drain", leaf.Namespace, leaf.Name, string(leaf.UID)) ||
				(drain.State != opsv1alpha1.UpgradeManagerDrainEvicting &&
					drain.State != opsv1alpha1.UpgradeManagerDrainRecovering) {
				return nil, fmt.Errorf("workload-drain request is not authorized by the current drain state")
			}
		case managedprotocol.MaintenancePurposeSoftwareMutation:
			purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
			if holder != mutationguard.UpgradeHolderIdentity(&leaf) ||
				(drain.State != opsv1alpha1.UpgradeManagerDrainPromoted &&
					drain.State != opsv1alpha1.UpgradeManagerDrainRecovering) {
				return nil, fmt.Errorf("promoted software-mutation request is not authorized by the current drain state")
			}
		default:
			return nil, fmt.Errorf("pdb-drain-v1 maintenance request has invalid purpose %q", requestPurpose)
		}
	default:
		return nil, fmt.Errorf("unsupported maintenance request version %q", requestVersion)
	}
	phase := ciskov1.DeviceMaintenanceSessionAcknowledged
	if len(leaf.Status.ManagedMutationClaims) != 0 {
		phase = ciskov1.DeviceMaintenanceSessionActive
	}
	if protocol == ciskov1.DeviceMaintenanceProtocolPDBDrainV1 {
		// The direct manager handshake applies and verifies the scheduling guard
		// before any drain/promotion request may acquire this Lease. A held exact
		// request therefore preserves Active rather than downgrading the durable
		// session to Acknowledged.
		phase = ciskov1.DeviceMaintenanceSessionActive
	}
	if leaf.Status.ManagerDrain != nil && leaf.Status.ManagerDrain.State == opsv1alpha1.UpgradeManagerDrainRecovering {
		phase = ciskov1.DeviceMaintenanceSessionRecovering
	}
	now := metav1.NewTime(r.now())
	return &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: phase, ProtocolVersion: protocol, Purpose: purpose, SessionToken: token,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID),
			},
			Holder: holder,
		},
		Operation: operation, DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		RequestedAt: metav1.NewTime(requestedAt), AcknowledgedAt: &now, ControlRevision: revision,
		Message: "manager acknowledged the exact managed software-upgrade maintenance request",
	}, nil
}

func validateManagedMutationLeaseSpec(spec *coordv1.LeaseSpec, holder string) error {
	if spec == nil || spec.Strategy != nil || spec.PreferredHolder != nil {
		return fmt.Errorf("unsupported Lease strategy metadata is present")
	}
	if holder == "" {
		if spec.HolderIdentity != nil && *spec.HolderIdentity != "" {
			return fmt.Errorf("idle Lease has a nonempty holderIdentity")
		}
		if spec.LeaseDurationSeconds != nil || spec.AcquireTime != nil || spec.RenewTime != nil {
			return fmt.Errorf("idle Lease retains holder timing metadata")
		}
		if spec.LeaseTransitions != nil && *spec.LeaseTransitions < 0 {
			return fmt.Errorf("idle Lease has a negative transition count")
		}
		return nil
	}
	if spec.HolderIdentity == nil || *spec.HolderIdentity != holder || strings.TrimSpace(holder) != holder ||
		len(holder) > 512 || strings.ContainsAny(holder, "\r\n\x00") {
		return fmt.Errorf("holderIdentity is malformed")
	}
	if spec.LeaseDurationSeconds == nil || *spec.LeaseDurationSeconds <= 0 ||
		*spec.LeaseDurationSeconds > maxManagedMutationLeaseSeconds || spec.AcquireTime == nil ||
		spec.RenewTime == nil || spec.AcquireTime.After(spec.RenewTime.Time) ||
		spec.LeaseTransitions == nil || *spec.LeaseTransitions <= 0 {
		return fmt.Errorf("held Lease timing/transition metadata is malformed")
	}
	return nil
}

func hasMaintenanceRequestAnnotations(annotations map[string]string) bool {
	for _, key := range []string{
		managedprotocol.AnnotationMaintenanceRequestVersion,
		managedprotocol.AnnotationMaintenanceSessionToken,
		managedprotocol.AnnotationMaintenanceRequestedAt,
		managedprotocol.AnnotationMaintenanceOperationNS,
		managedprotocol.AnnotationMaintenanceOperationName,
		managedprotocol.AnnotationMaintenanceOperationUID,
		managedprotocol.AnnotationMaintenanceControlRevision,
		managedprotocol.AnnotationMaintenancePurpose,
	} {
		if _, present := annotations[key]; present {
			return true
		}
	}
	return false
}

func validateMaintenanceLeafBinding(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	revision int64,
) error {
	if leaf.Namespace != device.Namespace || leaf.Spec.DeviceRef.Name != device.Name || leaf.UID == "" {
		return fmt.Errorf("maintenance leaf does not target this exact device namespace/name")
	}
	expected := map[string]string{
		managedprotocol.AnnotationManaged:         "true",
		managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName:      device.Name,
		managedprotocol.AnnotationDeviceUID:       string(device.UID),
		managedprotocol.AnnotationNodeName:        node.Name,
		managedprotocol.AnnotationNodeUID:         string(node.UID),
		managedprotocol.AnnotationWorkerUsername:  managedNetworkWorkerUsername(node),
		managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
	}
	for _, key := range []string{
		managedprotocol.AnnotationNetworkWorkerUsername,
		managedprotocol.AnnotationNetworkWorkerPodName,
		managedprotocol.AnnotationNetworkWorkerPodUID,
	} {
		expected[key] = node.Annotations[key]
	}
	for key, want := range expected {
		if leaf.Annotations[key] != want {
			return fmt.Errorf("maintenance leaf annotation %s=%q, want %q", key, leaf.Annotations[key], want)
		}
	}
	admission := leaf.Status.ManagerAdmission
	control := leaf.Status.ManagerControl
	worker := leaf.Status.WorkerControl
	if admission == nil || control == nil || worker == nil || admission.LeafUID != string(leaf.UID) ||
		admission.ProtocolVersion != opsv1alpha1.ManagedUpgradeProtocolVersion(managedprotocol.Version) ||
		admission.DeviceUID != string(device.UID) || admission.NodeUID != string(node.UID) ||
		admission.ReservationID == "" || admission.LedgerUID == "" || admission.PlanHash == "" ||
		admission.ControlRevision == nil || *admission.ControlRevision > revision ||
		control.Revision != revision || worker.ObservedControlRevision != revision || worker.ObservedAdmissionState != admission.State {
		return fmt.Errorf("maintenance leaf admission/control identity is incomplete or stale")
	}
	for annotation, expected := range map[string]string{
		managedprotocol.AnnotationCampaignUID:   admission.CampaignUID,
		managedprotocol.AnnotationPlanHash:      admission.PlanHash,
		managedprotocol.AnnotationLedgerUID:     admission.LedgerUID,
		managedprotocol.AnnotationReservationID: admission.ReservationID,
	} {
		if expected == "" || leaf.Annotations[annotation] != expected {
			return fmt.Errorf("maintenance leaf reservation binding %s is missing or inconsistent", annotation)
		}
	}
	hasClaim := len(leaf.Status.ManagedMutationClaims) != 0
	if admission.State != opsv1alpha1.UpgradeManagerAdmissionGranted &&
		!(hasClaim && admission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked) {
		return fmt.Errorf("maintenance leaf admission state %q does not authorize a session", admission.State)
	}
	if !hasClaim && (control.Pause || control.Cancel || worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady) {
		return fmt.Errorf("maintenance leaf is paused, cancelled, or has not acknowledged its grant")
	}
	if hasClaim && worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlClaimed &&
		worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		return fmt.Errorf("maintenance leaf claim is not in an observable worker state")
	}
	return nil
}

func terminalManagedLeaf(phase opsv1alpha1.UpgradePhase) bool {
	switch phase {
	case opsv1alpha1.UpgradePhaseSucceeded, opsv1alpha1.UpgradePhaseStagedForNextBoot,
		opsv1alpha1.UpgradePhaseFailed, opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseCancelled, opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseValidationFailed, opsv1alpha1.UpgradePhaseRebootTimeout:
		return true
	default:
		return false
	}
}

func applyManagedMaintenanceStatus(device *ciskov1.CiscoDevice, decision managedMaintenanceDecision) {
	if decision.session == nil {
		device.Status.MaintenanceSession = nil
	} else {
		device.Status.MaintenanceSession = decision.session.DeepCopy()
	}
	meta.SetStatusCondition(&device.Status.Conditions, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionMaintenanceReady, Status: decision.status,
		Reason: decision.reason, Message: truncateTopologyMessage(decision.message),
		ObservedGeneration: device.Generation,
	})
}

func maintenanceGuardTaint() corev1.Taint {
	return corev1.Taint{Key: maintenance.TaintKey, Value: maintenance.TaintValue, Effect: corev1.TaintEffectNoSchedule}
}
