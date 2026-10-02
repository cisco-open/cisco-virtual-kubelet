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

package softwareupgrade

import (
	"context"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyhealth"
)

const managedAdmissionPoll = 15 * time.Second

type managedLeafDecision struct {
	applies         bool
	bindingDenied   bool
	allowProgress   bool
	allowClaim      bool
	admissionState  opsv1alpha1.UpgradeManagerAdmissionState
	policyEpoch     int64
	controlRevision int64
	workerRevision  string
	effectiveState  opsv1alpha1.UpgradeWorkerControlState
	reason          string
	message         string
}

// syncManagedLeafGate acknowledges the latest manager grant/control revision
// before the phase state machine can advance. The claim functions repeat this
// validation against the resourceVersion they mutate, closing the race between
// a manager pause/cancel and a worker's durable device-mutation marker.
func (r *Reconciler) syncManagedLeafGate(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (managedLeafDecision, bool, error) {
	if !r.ManagedTopology && up.Annotations[managedprotocol.AnnotationManaged] != "true" {
		return managedLeafDecision{allowProgress: true, allowClaim: true}, false, nil
	}
	if r.ManagedTopology && up.Annotations[managedprotocol.AnnotationManaged] != "true" {
		// Managed workers have no ownership of ordinary leaves. Returning a
		// non-progressing decision without a status update keeps the native
		// admission boundary strict and prevents peer-object writes.
		decision := r.evaluateManagedLeaf(ctx, up)
		return decision, false, nil
	}

	var decision managedLeafDecision
	updated := false
	err := retryOnConflict(func() error {
		var current opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.apiReader().Get(ctx, client.ObjectKeyFromObject(up), &current); err != nil {
			return err
		}
		decision = r.evaluateManagedLeaf(ctx, &current)
		// A retained leaf from a predecessor worker can remain in the API
		// while its old Pod is being fenced. Its status mutation is correctly
		// denied by the admission policy because this worker is not the exact
		// bound Pod. Do not turn that expected denial into a reconcile hot loop;
		// settled history is observed read-only and unresolved claims remain
		// fenced for the manager's recovery path.
		if decision.bindingDenied {
			*up = *current.DeepCopy()
			return nil
		}
		desired := workerControlForDecision(&current, decision, now)
		if reflect.DeepEqual(current.Status.WorkerControl, desired) {
			*up = *current.DeepCopy()
			return nil
		}
		current.Status.WorkerControl = desired
		if err := r.Client.Status().Update(ctx, &current); err != nil {
			return err
		}
		*up = *current.DeepCopy()
		updated = true
		return nil
	})
	if err != nil {
		return decision, false, fmt.Errorf("acknowledge managed upgrade control: %w", err)
	}
	return decision, updated, nil
}

func (r *Reconciler) evaluateManagedLeaf(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade) managedLeafDecision {
	leafManaged := up.Annotations[managedprotocol.AnnotationManaged] == "true"
	if !r.ManagedTopology && !leafManaged {
		return managedLeafDecision{allowProgress: true, allowClaim: true}
	}
	decision := managedLeafDecision{
		applies:        true,
		admissionState: safeAdmissionState(up.Status.ManagerAdmission),
		effectiveState: opsv1alpha1.UpgradeWorkerControlDenied,
		reason:         "ManagedAdmissionDenied",
		workerRevision: r.WorkerRevision,
	}
	if up.Status.ManagerControl != nil {
		decision.controlRevision = up.Status.ManagerControl.Revision
	}
	if up.Status.ManagerAdmission != nil {
		decision.policyEpoch = up.Status.ManagerAdmission.PolicyEpoch
	}

	if !r.ManagedTopology {
		decision.message = "managed upgrade leaf cannot execute on a standalone worker"
		return decision
	}
	if !leafManaged {
		decision.message = fmt.Sprintf("managed worker requires %s=true on every upgrade leaf", managedprotocol.AnnotationManaged)
		return decision
	}
	if err := r.validateManagedLeafBinding(ctx, up); err != nil {
		decision.bindingDenied = true
		decision.message = boundedWorkerMessage("managed upgrade binding denied: " + err.Error())
		return decision
	}

	if terminalUpgradePhase(up.Status.Phase) {
		decision.allowProgress = true
		if managedTerminalMutationSettled(up) {
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlSettled
			decision.reason = "ManagedOutcomeSettled"
			decision.message = "managed upgrade has a terminal and conclusively settled worker outcome"
		} else {
			// A terminal API phase is not evidence that a claimed physical
			// mutation stopped. Keep the durable claim observable and let the
			// terminal quarantine/recovery path continue; the manager must not
			// release its topology reservation from phase alone.
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlClaimed
			decision.reason = "ManagedOutcomeUnresolved"
			decision.message = "managed upgrade is terminal but its claimed device mutation outcome remains unresolved"
		}
		return decision
	}

	inFlight := managedMutationNeedsObservation(up)
	admission := up.Status.ManagerAdmission
	control := up.Status.ManagerControl
	if admission.State != opsv1alpha1.UpgradeManagerAdmissionGranted {
		if admission.State == opsv1alpha1.UpgradeManagerAdmissionPending {
			switch {
			case control.Cancel:
				decision.effectiveState = opsv1alpha1.UpgradeWorkerControlCancelled
				decision.reason = "ManagerCancelled"
				decision.message = "manager cancellation is effective while admission is pending"
			case control.Pause:
				decision.effectiveState = opsv1alpha1.UpgradeWorkerControlPaused
				decision.reason = "ManagerPaused"
				decision.message = "manager pause is effective while admission is pending"
			default:
				// Ready here acknowledges that the exact pending identity
				// binding is usable by this worker. Progress and claims remain
				// false until the manager atomically changes State to Granted.
				decision.effectiveState = opsv1alpha1.UpgradeWorkerControlReady
				decision.reason = "ManagedAdmissionPending"
				decision.message = "managed identity binding is ready; waiting for manager admission"
			}
			return decision
		}
		if admission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked && inFlight {
			decision.allowProgress = true
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlClaimed
			decision.reason = "ManagedClaimObservation"
			decision.message = "manager admission was revoked after a durable mutation claim; observing without authorizing another claim"
			return decision
		}
		if admission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked && control.Cancel {
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlCancelled
			decision.reason = "ManagerCancelled"
			decision.message = "manager cancelled and revoked admission before a durable mutation claim"
			return decision
		}
		if admission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlSettled
			decision.reason = "ManagerAdmissionSettled"
			decision.message = "manager admission is settled; no further worker progress is authorized"
			return decision
		}
		decision.message = fmt.Sprintf("manager admission is %s, not Granted", admission.State)
		return decision
	}
	if control.Cancel {
		if inFlight {
			decision.allowProgress = true
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlClaimed
			decision.reason = "ManagedClaimObservation"
			decision.message = "manager cancellation is effective for new claims; observing the existing durable mutation claim"
			return decision
		}
		decision.effectiveState = opsv1alpha1.UpgradeWorkerControlCancelled
		decision.reason = "ManagerCancelled"
		decision.message = "manager cancellation is effective; no new mutation claim is authorized"
		return decision
	}
	if control.Pause {
		if inFlight {
			decision.allowProgress = true
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlClaimed
			decision.reason = "ManagedClaimObservation"
			decision.message = "manager pause is effective for new claims; observing the existing durable mutation claim"
			return decision
		}
		decision.effectiveState = opsv1alpha1.UpgradeWorkerControlPaused
		decision.reason = "ManagerPaused"
		decision.message = "manager pause is effective; no new mutation claim is authorized"
		return decision
	}

	decision.allowProgress = true
	decision.allowClaim = true
	decision.effectiveState = opsv1alpha1.UpgradeWorkerControlReady
	decision.reason = "ManagedAdmissionReady"
	decision.message = "manager grant and control revision are ready for worker progress"
	return decision
}

func managedTerminalMutationSettled(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	return up != nil && terminalUpgradePhase(up.Status.Phase) &&
		(len(up.Status.ManagedMutationClaims) == 0 ||
			meta.IsStatusConditionTrue(up.Status.Conditions, conditionTypeMutationSettled))
}

// managedCancellationCanReleaseMutationLease recognizes the narrow
// pre-dispatch cancellation boundary. The worker acknowledgement must already
// reflect the exact manager revision and admission epoch; the caller invokes
// this only after syncManagedLeafGate found that acknowledgement durable. A
// durable claim or any at-most-once mutation marker makes the outcome
// potentially physical and therefore keeps the Lease quarantined.
func managedCancellationCanReleaseMutationLease(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	decision managedLeafDecision,
) bool {
	if up == nil || !decision.applies || decision.allowProgress ||
		decision.effectiveState != opsv1alpha1.UpgradeWorkerControlCancelled ||
		len(up.Status.ManagedMutationClaims) != 0 || upgradeMutationSubmitted(up) {
		return false
	}
	admission := up.Status.ManagerAdmission
	control := up.Status.ManagerControl
	worker := up.Status.WorkerControl
	return admission != nil && control != nil && worker != nil && control.Cancel &&
		decision.admissionState == admission.State &&
		decision.policyEpoch == admission.PolicyEpoch &&
		decision.controlRevision == control.Revision &&
		worker.ObservedAdmissionState == admission.State &&
		worker.ObservedPolicyEpoch == admission.PolicyEpoch &&
		worker.ObservedControlRevision == control.Revision &&
		worker.ObservedWorkerConfigRevision == decision.workerRevision &&
		worker.EffectiveState == opsv1alpha1.UpgradeWorkerControlCancelled
}

func (r *Reconciler) validateManagedLeafBinding(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade) error {
	if r.Reader == nil {
		return fmt.Errorf("managed topology requires an uncached Kubernetes API reader")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "runtime device namespace", value: r.DeviceNamespace},
		{name: "runtime device name", value: r.DeviceName},
		{name: "runtime device UID", value: r.DeviceUID},
		{name: "runtime Node name", value: r.NodeName},
		{name: "runtime worker configuration revision", value: r.WorkerRevision},
		{name: "runtime worker Pod name", value: r.WorkerPodName},
		{name: "runtime worker Pod UID", value: r.WorkerPodUID},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is empty", field.name)
		}
	}
	if up.Namespace != r.DeviceNamespace || up.Spec.DeviceRef.Name != r.DeviceName {
		return fmt.Errorf("leaf target %s/%s does not match runtime CiscoDevice %s/%s",
			up.Namespace, up.Spec.DeviceRef.Name, r.DeviceNamespace, r.DeviceName)
	}
	annotatedGeneration, err := strconv.ParseInt(
		up.Annotations[managedprotocol.AnnotationDeviceGeneration], 10, 64,
	)
	if err != nil || annotatedGeneration < 1 {
		return fmt.Errorf("annotation %s is missing or invalid", managedprotocol.AnnotationDeviceGeneration)
	}
	var device ciskov1.CiscoDevice
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: r.DeviceNamespace, Name: r.DeviceName}, &device); err != nil {
		return fmt.Errorf("read bound CiscoDevice %s/%s: %w", r.DeviceNamespace, r.DeviceName, err)
	}
	if device.UID == "" || string(device.UID) != r.DeviceUID || device.Generation != annotatedGeneration ||
		device.Spec.Driver != ciskov1.DeviceDriverXE {
		return fmt.Errorf("live CiscoDevice UID, generation, or driver no longer matches the managed grant")
	}
	if !managedUpgradeWorkerReady(&device, r.WorkerRevision, r.WorkerPodUID) {
		return fmt.Errorf("live CiscoDevice has no ready worker proof for runtime revision %q", r.WorkerRevision)
	}
	podNameAnnotation := managedprotocol.AnnotationAppWorkerPodName
	podUIDAnnotation := managedprotocol.AnnotationAppWorkerPodUID
	if device.Status.NetworkWorkerRevision != nil {
		podNameAnnotation = managedprotocol.AnnotationNetworkWorkerPodName
		podUIDAnnotation = managedprotocol.AnnotationNetworkWorkerPodUID
	}
	if err := requireAnnotation(up.Annotations, podNameAnnotation, r.WorkerPodName); err != nil {
		return fmt.Errorf("runtime worker Pod binding: %w", err)
	}
	if err := requireAnnotation(up.Annotations, podUIDAnnotation, r.WorkerPodUID); err != nil {
		return fmt.Errorf("runtime worker Pod binding: %w", err)
	}
	if err := r.validateManagedRuntimeSecretRevisions(ctx, &device); err != nil {
		return err
	}

	annotations := up.Annotations
	for _, binding := range []struct {
		key      string
		expected string
	}{
		{key: managedprotocol.AnnotationManaged, expected: "true"},
		{key: managedprotocol.AnnotationDeviceNamespace, expected: r.DeviceNamespace},
		{key: managedprotocol.AnnotationDeviceName, expected: r.DeviceName},
		{key: managedprotocol.AnnotationDeviceUID, expected: r.DeviceUID},
		{key: managedprotocol.AnnotationNodeName, expected: r.NodeName},
		{key: managedprotocol.AnnotationWorkerProtocol, expected: managedprotocol.Version},
	} {
		if err := requireAnnotation(annotations, binding.key, binding.expected); err != nil {
			return err
		}
	}

	var node corev1.Node
	if err := r.apiReader().Get(ctx, client.ObjectKey{Name: r.NodeName}, &node); err != nil {
		return fmt.Errorf("read bound Node %q: %w", r.NodeName, err)
	}
	if node.UID == "" {
		return fmt.Errorf("bound Node %q has no UID", r.NodeName)
	}
	nodeUID := string(node.UID)
	identity := device.Status.NodeIdentity
	if identity == nil || identity.DeviceUID != r.DeviceUID || identity.NodeName != r.NodeName ||
		identity.NodeUID != nodeUID {
		return fmt.Errorf("live CiscoDevice manager-owned Node identity does not match the runtime binding")
	}
	physicalIdentity, err := topology.ObservedPhysicalIdentity(
		device.Spec.PhysicalIdentity,
		node.Status.NodeInfo.MachineID,
		node.Status.NodeInfo.SystemUUID,
	)
	if err != nil {
		return fmt.Errorf("live physical identity consistency check failed: %w", err)
	}
	if device.Status.WorkerRevision == nil || device.Status.WorkerRevision.ObservedRevision == "" {
		return fmt.Errorf("live CiscoDevice has no observed app-hosting worker revision")
	}
	if identity.PhysicalIdentity != physicalIdentity {
		return fmt.Errorf("live CiscoDevice manager-bound physical identity %q does not match declaration and Node observation %q",
			identity.PhysicalIdentity, physicalIdentity)
	}
	for _, binding := range []struct {
		key      string
		expected string
	}{
		{key: managedprotocol.AnnotationManaged, expected: "true"},
		{key: managedprotocol.AnnotationDeviceNamespace, expected: r.DeviceNamespace},
		{key: managedprotocol.AnnotationDeviceName, expected: r.DeviceName},
		{key: managedprotocol.AnnotationDeviceUID, expected: r.DeviceUID},
		{key: managedprotocol.AnnotationNodeUID, expected: nodeUID},
		{key: managedprotocol.AnnotationWorkerProtocol, expected: managedprotocol.Version},
		{key: managedprotocol.AnnotationWorkerObservedRevision, expected: device.Status.WorkerRevision.ObservedRevision},
	} {
		if err := requireAnnotation(node.Annotations, binding.key, binding.expected); err != nil {
			return fmt.Errorf("Node binding: %w", err)
		}
	}
	workerUsername := node.Annotations[managedprotocol.AnnotationNetworkWorkerUsername]
	if device.Status.NetworkWorkerRevision == nil {
		workerUsername = node.Annotations[managedprotocol.AnnotationWorkerUsername]
	}
	if !strings.HasPrefix(workerUsername, "system:serviceaccount:"+r.DeviceNamespace+":") ||
		strings.TrimPrefix(workerUsername, "system:serviceaccount:"+r.DeviceNamespace+":") == "" {
		return fmt.Errorf("Node binding annotation %s does not name a worker ServiceAccount in namespace %q",
			managedprotocol.AnnotationWorkerUsername, r.DeviceNamespace)
	}
	if err := requireAnnotation(annotations, managedprotocol.AnnotationWorkerUsername, workerUsername); err != nil {
		return err
	}
	if err := requireAnnotation(annotations, managedprotocol.AnnotationNodeUID, nodeUID); err != nil {
		return err
	}

	admission := up.Status.ManagerAdmission
	if admission == nil {
		return fmt.Errorf("status.managerAdmission is absent")
	}
	expectedProtocol := opsv1alpha1.ExpectedManagedUpgradeProtocol(up.Spec.MaxTransferBytesPerSecond)
	if admission.ProtocolVersion != expectedProtocol {
		return fmt.Errorf("manager protocol %q does not match required leaf protocol %q", admission.ProtocolVersion, expectedProtocol)
	}
	if up.UID == "" || admission.LeafUID != string(up.UID) {
		return fmt.Errorf("manager leaf UID %q does not match metadata.uid %q", admission.LeafUID, up.UID)
	}
	if admission.DeviceUID != r.DeviceUID {
		return fmt.Errorf("manager device UID %q does not match runtime device UID %q", admission.DeviceUID, r.DeviceUID)
	}
	if admission.DeviceGeneration != annotatedGeneration {
		return fmt.Errorf("manager device generation %d does not match live generation %d",
			admission.DeviceGeneration, annotatedGeneration)
	}
	if admission.NodeUID != nodeUID {
		return fmt.Errorf("manager Node UID %q does not match bound Node UID %q", admission.NodeUID, nodeUID)
	}
	if admission.PhysicalIdentity != physicalIdentity {
		return fmt.Errorf("manager physical identity %q does not match the live bound identity %q",
			admission.PhysicalIdentity, physicalIdentity)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "policy UID", value: admission.PolicyUID},
		{name: "policy resourceVersion", value: admission.PolicyResourceVersion},
		{name: "physical identity", value: admission.PhysicalIdentity},
		{name: "topology lock acquisition ID", value: admission.TopologyLockID},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("status.managerAdmission %s is empty", field.name)
		}
	}
	if admission.TopologyLockID != strings.ToLower(admission.TopologyLockID) || len(admission.TopologyLockID) != 32 {
		return fmt.Errorf("status.managerAdmission topology lock acquisition ID is invalid")
	}
	if decoded, err := hex.DecodeString(admission.TopologyLockID); err != nil || len(decoded) != 16 {
		return fmt.Errorf("status.managerAdmission topology lock acquisition ID is invalid")
	}
	if admission.UpdatedAt.IsZero() {
		return fmt.Errorf("status.managerAdmission.updatedAt is absent")
	}
	if admission.PolicyEpoch < 1 {
		return fmt.Errorf("status.managerAdmission.policyEpoch is invalid")
	}
	for _, binding := range []struct {
		key      string
		expected string
	}{
		{key: managedprotocol.AnnotationCampaignUID, expected: admission.CampaignUID},
		{key: managedprotocol.AnnotationPlanHash, expected: admission.PlanHash},
		{key: managedprotocol.AnnotationLedgerUID, expected: admission.LedgerUID},
		{key: managedprotocol.AnnotationReservationID, expected: admission.ReservationID},
	} {
		if strings.TrimSpace(binding.expected) == "" {
			return fmt.Errorf("status.managerAdmission binding for %s is empty", binding.key)
		}
		if err := requireAnnotation(annotations, binding.key, binding.expected); err != nil {
			return err
		}
	}
	if admission.ControlRevision == nil {
		return fmt.Errorf("status.managerAdmission.controlRevision is absent")
	}
	if *admission.ControlRevision < 0 {
		return fmt.Errorf("status.managerAdmission.controlRevision is negative")
	}
	if up.Status.ManagerControl == nil {
		return fmt.Errorf("status.managerControl is absent")
	}
	if up.Status.ManagerControl.Revision < 0 || up.Status.ManagerControl.UpdatedAt.IsZero() {
		return fmt.Errorf("status.managerControl has invalid revision or updatedAt")
	}
	if up.Status.ManagerControl.Revision < *admission.ControlRevision {
		return fmt.Errorf("manager control revision %d is older than admitted revision %d",
			up.Status.ManagerControl.Revision, *admission.ControlRevision)
	}
	if err := r.validateManagedSourceSecret(ctx, up); err != nil {
		return err
	}
	return validateManagedClaimCoverage(up, up.Status.ManagerControl.Revision)
}

// validateManagedActivationBinding makes a manager-created preinstalled leaf
// unusable unless it names the exact independently approved preparation
// receipt and the worker is still using the same device trust inputs. Native
// inventory is checked separately immediately before entering Activating.
func (r *Reconciler) validateManagedActivationBinding(up *opsv1alpha1.IOSXESoftwareUpgrade) error {
	if up == nil || up.Annotations[managedprotocol.AnnotationManaged] != "true" {
		return nil
	}
	if up.Spec.ImageSource.Preinstalled == nil {
		if up.Annotations[managedprotocol.AnnotationActivationApprovalHash] != "" {
			return fmt.Errorf("activation authorization is present on a non-preinstalled image source")
		}
		return nil
	}
	annotations := up.Annotations
	for _, key := range []string{
		managedprotocol.AnnotationActivationApprovalHash,
		managedprotocol.AnnotationPreparedReceiptHash,
		managedprotocol.AnnotationPreparedSourceDigest,
	} {
		if !validManagedSHA256(annotations[key]) {
			return fmt.Errorf("annotation %s is missing or is not a canonical sha256 identity", key)
		}
	}
	if strings.TrimSpace(annotations[managedprotocol.AnnotationPreparedUpgradeUID]) == "" {
		return fmt.Errorf("annotation %s is missing", managedprotocol.AnnotationPreparedUpgradeUID)
	}
	if strings.TrimSpace(annotations[managedprotocol.AnnotationPreparedUpgradeName]) == "" {
		return fmt.Errorf("annotation %s is missing", managedprotocol.AnnotationPreparedUpgradeName)
	}
	expectedTrust := annotations[managedprotocol.AnnotationPreparedTrustHash]
	currentTrust := ""
	if r.CredentialSecretRevision != "" || r.GNOITLSSecretRevision != "" || r.GNOIProvisioningRevision != "" {
		var err error
		currentTrust, err = PreparedTrustIdentityHash(
			r.CredentialSecretRevision, r.GNOITLSSecretRevision, r.GNOIProvisioningRevision)
		if err != nil {
			return err
		}
	}
	if currentTrust != expectedTrust {
		return fmt.Errorf("current device trust identity does not match the prepared receipt")
	}
	return nil
}

func (r *Reconciler) validateManagedActivationReceipt(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	_, err := r.validatedPreparedActivationParent(ctx, up)
	return err
}

func (r *Reconciler) validatedPreparedActivationParent(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
) (*opsv1alpha1.IOSXESoftwareUpgrade, error) {
	if err := r.validateManagedActivationBinding(up); err != nil {
		return nil, err
	}
	if up == nil || up.Annotations[managedprotocol.AnnotationManaged] != "true" || up.Spec.ImageSource.Preinstalled == nil {
		return nil, nil
	}
	var prepared opsv1alpha1.IOSXESoftwareUpgrade
	name := up.Annotations[managedprotocol.AnnotationPreparedUpgradeName]
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: up.Namespace, Name: name}, &prepared); err != nil {
		return nil, fmt.Errorf("read authorized prepared leaf %s/%s: %w", up.Namespace, name, err)
	}
	if err := validatePreparedActivationParent(up, &prepared); err != nil {
		return nil, err
	}
	return &prepared, nil
}

// validatePreparedActivationParent proves that a preinstalled activation leaf
// names this exact immutable Prepared object and receipt. The per-device queue
// uses the same predicate as activation reconciliation so the retained receipt
// can yield only to its own authorized activation, never to an unrelated leaf.
func validatePreparedActivationParent(
	up, prepared *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if up == nil || prepared == nil {
		return fmt.Errorf("activation or prepared leaf is absent")
	}
	annotations := up.Annotations
	if annotations[managedprotocol.AnnotationManaged] != "true" || up.Spec.ImageSource.Preinstalled == nil ||
		!validManagedSHA256(annotations[managedprotocol.AnnotationActivationApprovalHash]) ||
		!validManagedSHA256(annotations[managedprotocol.AnnotationPreparedReceiptHash]) ||
		!validManagedSHA256(annotations[managedprotocol.AnnotationPreparedSourceDigest]) ||
		!validManagedSHA256(annotations[managedprotocol.AnnotationPreparedTrustHash]) ||
		!validManagedSHA256(annotations[managedprotocol.AnnotationPlanHash]) ||
		strings.TrimSpace(annotations[managedprotocol.AnnotationCampaignUID]) == "" ||
		strings.TrimSpace(annotations[managedprotocol.AnnotationDeviceUID]) == "" {
		return fmt.Errorf("activation leaf authority binding is incomplete")
	}
	receipt := prepared.Status.PreparedReceipt
	if string(prepared.UID) != annotations[managedprotocol.AnnotationPreparedUpgradeUID] ||
		prepared.Name != annotations[managedprotocol.AnnotationPreparedUpgradeName] ||
		prepared.Namespace != up.Namespace ||
		prepared.Status.Phase != opsv1alpha1.UpgradePhasePrepared || receipt == nil {
		return fmt.Errorf("authorized prepared leaf identity or phase changed")
	}
	if err := ValidatePreparedReceipt(receipt); err != nil {
		return fmt.Errorf("authorized prepared receipt is invalid: %w", err)
	}
	if receipt.ReceiptHash != annotations[managedprotocol.AnnotationPreparedReceiptHash] ||
		receipt.UpgradeUID != annotations[managedprotocol.AnnotationPreparedUpgradeUID] ||
		receipt.DeviceUID != annotations[managedprotocol.AnnotationDeviceUID] ||
		receipt.CampaignUID != annotations[managedprotocol.AnnotationCampaignUID] ||
		receipt.PlanHash != annotations[managedprotocol.AnnotationPlanHash] ||
		receipt.TargetVersion != up.Spec.TargetVersion ||
		receipt.SourceDigest != annotations[managedprotocol.AnnotationPreparedSourceDigest] ||
		receipt.TrustIdentityHash != annotations[managedprotocol.AnnotationPreparedTrustHash] {
		return fmt.Errorf("authorized prepared receipt no longer matches the activation leaf binding")
	}
	return nil
}

// ValidatePreparedActivationParent verifies that an activation leaf is bound
// to one exact immutable Prepared object and receipt. Manager-side ownership
// settlement uses the same predicate as worker-side activation dispatch.
func ValidatePreparedActivationParent(
	activation, prepared *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	return validatePreparedActivationParent(activation, prepared)
}

// PreparedReceiptConsumed reports whether an immutable preparation has one
// exact, conclusively settled activation successor. The Prepared object remains
// append-only audit evidence; only a terminal, verified successor with the same
// frozen manager and content bindings releases its per-device queue ownership.
// Policy resourceVersion is deliberately not an equality fence: metadata-only
// policy churn advances it within the same policy UID and safety epoch.
func PreparedReceiptConsumed(
	prepared *opsv1alpha1.IOSXESoftwareUpgrade,
	candidates []opsv1alpha1.IOSXESoftwareUpgrade,
) bool {
	if prepared == nil || prepared.Status.Phase != opsv1alpha1.UpgradePhasePrepared ||
		prepared.Status.PreparedReceipt == nil ||
		ValidatePreparedReceipt(prepared.Status.PreparedReceipt) != nil {
		return false
	}
	receipt := prepared.Status.PreparedReceipt
	for i := range candidates {
		consumer := &candidates[i]
		admission := consumer.Status.ManagerAdmission
		if consumer.UID == prepared.UID ||
			validatePreparedActivationParent(consumer, prepared) != nil ||
			consumer.Status.Phase != opsv1alpha1.UpgradePhaseSucceeded ||
			consumer.Status.CompletionTime == nil ||
			(consumer.Status.RunningVersion != receipt.TargetVersion &&
				!strings.HasPrefix(consumer.Status.RunningVersion, receipt.TargetVersion+".")) ||
			!meta.IsStatusConditionTrue(consumer.Status.Conditions, "Verified") ||
			!meta.IsStatusConditionTrue(consumer.Status.Conditions, "DeviceMutationSettled") ||
			admission == nil || admission.ProtocolVersion != receipt.ManagedProtocolVersion ||
			admission.State != opsv1alpha1.UpgradeManagerAdmissionSettled ||
			admission.LeafUID != string(consumer.UID) ||
			admission.DeviceUID != receipt.DeviceUID || admission.NodeUID != receipt.NodeUID ||
			admission.DeviceGeneration != receipt.DeviceGeneration ||
			admission.PhysicalIdentity != receipt.PhysicalIdentity ||
			admission.CampaignUID != receipt.CampaignUID || admission.PlanHash != receipt.PlanHash ||
			admission.PolicyUID != receipt.PolicyUID ||
			admission.PolicyEpoch != receipt.PolicyEpoch {
			continue
		}
		return true
	}
	return false
}

func validManagedSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

// managedUpgradeWorkerReady verifies the worker which owns the software
// upgrade control plane. In managed topology that is the dedicated
// network-management worker, whose proof is recorded separately from the
// app-hosting worker. The app-hosting status remains the compatibility
// fallback for legacy/isolated workers where no network worker status exists.
func managedUpgradeWorkerReady(device *ciskov1.CiscoDevice, revision, podUID string) bool {
	if device == nil {
		return false
	}
	if network := device.Status.NetworkWorkerRevision; network != nil {
		return network.DesiredRevision == revision && network.ObservedRevision == revision &&
			network.PodUID == podUID &&
			network.PodStartTime != nil &&
			network.PodReadyTime != nil &&
			!network.PodReadyTime.Before(network.PodStartTime)
	}
	worker := device.Status.WorkerRevision
	return worker != nil &&
		worker.DesiredRevision == revision &&
		worker.ObservedRevision == revision &&
		worker.PodUID == podUID &&
		worker.PodStartTime != nil &&
		worker.ReadyHeartbeatTime != nil &&
		!worker.ReadyHeartbeatTime.Before(worker.PodStartTime)
}

func (r *Reconciler) validateManagedRuntimeSecretRevisions(ctx context.Context, device *ciskov1.CiscoDevice) error {
	if device == nil {
		return fmt.Errorf("live CiscoDevice is absent")
	}
	bindings := []struct {
		purpose  string
		name     string
		expected string
	}{
		{purpose: "device credential", expected: r.CredentialSecretRevision},
		{purpose: "gNOI TLS", expected: r.GNOITLSSecretRevision},
		{purpose: "gNOI provisioning", expected: r.GNOIProvisioningRevision},
	}
	if device.Spec.CredentialSecretRef != nil {
		bindings[0].name = device.Spec.CredentialSecretRef.Name
	}
	if ref := managedGNOITLSSecretRef(&device.Spec); ref != nil {
		bindings[1].name = ref.Name
	}
	if provisioning := managedGNOIProvisioning(&device.Spec); provisioning != nil {
		bindings[2].name = provisioning.SecretRef.Name
	}
	for _, binding := range bindings {
		if binding.name == "" {
			if binding.expected != "" {
				return fmt.Errorf("runtime %s Secret revision is present without a live Secret reference", binding.purpose)
			}
			continue
		}
		if binding.expected == "" {
			return fmt.Errorf("runtime %s Secret revision is empty", binding.purpose)
		}
		var secret corev1.Secret
		if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: device.Namespace, Name: binding.name}, &secret); err != nil {
			return fmt.Errorf("read live %s Secret %s/%s: %w", binding.purpose, device.Namespace, binding.name, err)
		}
		if secret.ResourceVersion == "" || secret.ResourceVersion != binding.expected {
			return fmt.Errorf("live %s Secret resourceVersion %q does not match runtime revision %q",
				binding.purpose, secret.ResourceVersion, binding.expected)
		}
	}
	return nil
}

func managedGNOITLSSecretRef(spec *ciskov1.DeviceSpec) *ciskov1.GNOITLSSecretReference {
	if spec == nil || spec.GNOI == nil || spec.GNOI.TLS == nil {
		return nil
	}
	return spec.GNOI.TLS.SecretRef
}

func managedGNOIProvisioning(spec *ciskov1.DeviceSpec) *ciskov1.XEGNOICertificateProvisioning {
	if spec == nil || spec.Driver != ciskov1.DeviceDriverXE || spec.XE == nil || spec.XE.GNOI == nil {
		return nil
	}
	return spec.XE.GNOI.CertificateProvisioning
}

func (r *Reconciler) validateManagedSourceSecret(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade) error {
	uid, uidAnnotated := up.Annotations[managedprotocol.AnnotationSourceSecretUID]
	ref := up.Spec.ImageSource.URLSecretRef
	if ref == nil {
		if uidAnnotated {
			return fmt.Errorf("source Secret identity annotation is present without imageSource.urlSecretRef")
		}
		return nil
	}
	if strings.TrimSpace(uid) == "" {
		return fmt.Errorf("annotation %s is required with imageSource.urlSecretRef", managedprotocol.AnnotationSourceSecretUID)
	}
	var secret corev1.Secret
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: up.Namespace, Name: ref.Name}, &secret); err != nil {
		return fmt.Errorf("read managed image source Secret %s/%s: %w", up.Namespace, ref.Name, err)
	}
	if secret.UID == "" || string(secret.UID) != uid {
		return fmt.Errorf("managed image source Secret UID %q does not match annotation %q", secret.UID, uid)
	}
	if err := ValidateURLSecretEndpoint(&secret, up.Spec.ImageSource.URL); err != nil {
		return fmt.Errorf("managed image source Secret endpoint authorization is invalid: %w", err)
	}
	return nil
}

func (r *Reconciler) prepareManagedMutationClaim(
	ctx context.Context,
	up, current *opsv1alpha1.IOSXESoftwareUpgrade,
	stage opsv1alpha1.UpgradeManagedMutationStage,
	now time.Time,
) (bool, error) {
	decision := r.evaluateManagedLeaf(ctx, current)
	if !decision.applies {
		return true, nil
	}
	if decision.allowClaim {
		if err := r.validateManagedNetworkGrant(ctx, current.Status.ManagerAdmission, now); err != nil {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "NetworkEvidenceAuthorityInvalid"
			decision.message = boundedWorkerMessage(err.Error())
		}
	}
	if decision.allowClaim {
		devicePodLister, workloadGate, gateErr := r.managedClaimPodLister(current)
		if gateErr != nil {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "WorkloadGateUnavailable"
			decision.message = boundedWorkerMessage(gateErr.Error())
		} else if err := r.ensureNoFailedCampaignPeer(ctx, current); err != nil {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "CampaignTargetFailed"
			decision.message = boundedWorkerMessage(err.Error())
		} else if pods, err := r.boundActiveWorkloads(ctx); err != nil {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "WorkloadCheckFailed"
			decision.message = boundedWorkerMessage(workloadGate + " could not list workloads for the bound Node: " + err.Error())
		} else if len(pods) > 0 {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "WorkloadsRunning"
			decision.message = fmt.Sprintf("%s denied %s: %d active workload Pod(s) remain bound to Node %q", workloadGate,
				stage, len(pods), r.NodeName)
		} else if devicePodLister == nil {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "DeviceWorkloadCheckFailed"
			decision.message = workloadGate + " could not verify the device app-hosting inventory"
		} else if devicePods, err := devicePodLister(ctx); err != nil {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "DeviceWorkloadCheckFailed"
			decision.message = boundedWorkerMessage(workloadGate + " could not list the device app-hosting inventory: " + err.Error())
		} else if count := nonNilPodCount(devicePods); count > 0 {
			decision.allowClaim = false
			decision.effectiveState = opsv1alpha1.UpgradeWorkerControlDenied
			decision.reason = "DeviceWorkloadsRunning"
			decision.message = fmt.Sprintf("%s denied %s: device inventory still reports %d hosted workload Pod(s)", workloadGate, stage, count)
		}
	}
	if !decision.allowClaim {
		changed := applyWorkerControlDecision(current, decision, now)
		if current.Status.Message != decision.message || readyReason(current.Status.Conditions) != decision.reason {
			current.Status.Message = decision.message
			r.setReady(current, metav1.ConditionFalse, decision.reason, decision.message, now)
			current.Status.ObservedGeneration = current.Generation
			changed = true
		}
		if changed {
			if err := r.Client.Status().Update(ctx, current); err != nil {
				return false, err
			}
		}
		up.Status = *current.Status.DeepCopy()
		return false, nil
	}
	if err := addManagedMutationClaim(current, stage, decision.policyEpoch, decision.controlRevision, now); err != nil {
		return false, err
	}
	decision.effectiveState = opsv1alpha1.UpgradeWorkerControlClaimed
	decision.reason = "ManagedMutationClaimed"
	decision.message = fmt.Sprintf("claimed %s at manager control revision %d", stage, decision.controlRevision)
	applyWorkerControlDecision(current, decision, now)
	return true, nil
}

// validateManagedNetworkGrant closes the manager-grant/worker-claim race for
// an opted-in network gate. It uses an uncached read immediately before a new
// durable mutation claim and requires the exact manager-accepted observation
// bound into the grant to remain current and unexpired. An omitted authority
// is the compatibility contract for campaigns without a network policy.
func (r *Reconciler) validateManagedNetworkGrant(
	ctx context.Context,
	admission *opsv1alpha1.UpgradeManagerAdmissionStatus,
	now time.Time,
) error {
	if admission == nil {
		return fmt.Errorf("manager admission is absent")
	}
	present := []bool{
		admission.NetworkEvidenceHash != "",
		admission.NetworkEvidenceProducerRevision != "",
		admission.NetworkEvidenceWorkerPodUID != "",
		admission.NetworkEvidenceSampleSequence != nil,
		admission.NetworkEvidenceNotAfter != nil,
	}
	for _, value := range present[1:] {
		if value != present[0] {
			return fmt.Errorf("manager network evidence authority is incomplete")
		}
	}
	if !present[0] {
		return nil
	}
	if *admission.NetworkEvidenceSampleSequence < 1 || admission.NetworkEvidenceNotAfter.IsZero() {
		return fmt.Errorf("manager network evidence authority is invalid")
	}
	if !now.UTC().Before(admission.NetworkEvidenceNotAfter.Time.UTC()) {
		return fmt.Errorf("manager network evidence authority expired at %s",
			admission.NetworkEvidenceNotAfter.Time.UTC().Format(time.RFC3339Nano))
	}
	var device ciskov1.CiscoDevice
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: r.DeviceNamespace, Name: r.DeviceName}, &device); err != nil {
		return fmt.Errorf("read current network evidence: %w", err)
	}
	networkWorker := device.Status.NetworkWorkerRevision
	if networkWorker == nil || networkWorker.DesiredRevision != admission.NetworkEvidenceProducerRevision ||
		networkWorker.ObservedRevision != admission.NetworkEvidenceProducerRevision ||
		networkWorker.PodUID != admission.NetworkEvidenceWorkerPodUID {
		return fmt.Errorf("current network worker no longer matches the manager grant")
	}
	accepted := device.Status.HealthObservation
	if accepted == nil || accepted.AcceptedNetwork == nil {
		return fmt.Errorf("current manager-accepted network evidence is absent")
	}
	sample := accepted.AcceptedNetwork
	if sample.ProducerRevision != admission.NetworkEvidenceProducerRevision ||
		sample.WorkerPodUID != admission.NetworkEvidenceWorkerPodUID ||
		sample.SampleSequence != *admission.NetworkEvidenceSampleSequence {
		return fmt.Errorf("current manager-accepted network evidence identity changed")
	}
	digest, err := topologyhealth.AcceptedNetworkDigest(sample)
	if err != nil {
		return err
	}
	if digest != admission.NetworkEvidenceHash {
		return fmt.Errorf("current manager-accepted network evidence digest changed")
	}
	return nil
}

// managedClaimPodLister selects the final device-side workload fence. The
// established BlockIfRunning path keeps its compatibility inventory, while an
// explicitly promoted drain requires the driver's fail-closed complete scan.
func (r *Reconciler) managedClaimPodLister(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
) (func(context.Context) ([]*corev1.Pod, error), string, error) {
	if up == nil || up.Status.ManagerDrain == nil {
		return r.DevicePodLister, "BlockIfRunning", nil
	}
	drain := up.Status.ManagerDrain
	admission := up.Status.ManagerAdmission
	control := up.Status.ManagerControl
	if admission == nil || control == nil || admission.ControlRevision == nil ||
		drain.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		drain.State != opsv1alpha1.UpgradeManagerDrainPromoted ||
		drain.ReservationID != admission.ReservationID || drain.PolicyEpoch != admission.PolicyEpoch ||
		drain.ControlRevision != control.Revision || drain.ControlRevision != *admission.ControlRevision ||
		drain.NodeUID != admission.NodeUID || drain.SessionToken == "" {
		return nil, "Drain", fmt.Errorf("managed Drain has no exact promoted session authority")
	}
	if r.DrainDevicePodLister == nil {
		return nil, "Drain", fmt.Errorf("managed Drain requires complete device inventory support from the platform driver")
	}
	return r.DrainDevicePodLister, "Drain", nil
}

// ensureNoFailedCampaignPeer is the worker-side half of the campaign failure
// fence. A durable terminal failure on any sibling is sufficient to deny a new
// device mutation even if a stale manager replica has not yet revoked this
// leaf's grant. Existing claims are observed by evaluateManagedLeaf and never
// pass through this new-claim gate.
func (r *Reconciler) ensureNoFailedCampaignPeer(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	campaignUID := strings.TrimSpace(up.Annotations[managedprotocol.AnnotationCampaignUID])
	if campaignUID == "" {
		return fmt.Errorf("managed campaign UID is empty")
	}
	var leaves opsv1alpha1.IOSXESoftwareUpgradeList
	if err := r.apiReader().List(ctx, &leaves, client.InNamespace(up.Namespace)); err != nil {
		return fmt.Errorf("cannot verify campaign failure fence: %w", err)
	}
	for i := range leaves.Items {
		peer := &leaves.Items[i]
		if peer.Name == up.Name && peer.UID == up.UID {
			continue
		}
		if peer.Annotations[managedprotocol.AnnotationManaged] != "true" ||
			peer.Annotations[managedprotocol.AnnotationCampaignUID] != campaignUID {
			continue
		}
		if isTerminalFailurePhase(peer.Status.Phase) {
			return fmt.Errorf("campaign peer %s/%s is terminal in phase %s; no new device mutation is authorized",
				peer.Namespace, peer.Name, peer.Status.Phase)
		}
	}
	return nil
}

func (r *Reconciler) boundActiveWorkloads(ctx context.Context) ([]corev1.Pod, error) {
	var pods corev1.PodList
	if err := r.apiReader().List(ctx, &pods, client.MatchingFields{"spec.nodeName": r.NodeName}); err != nil {
		return nil, err
	}
	active := make([]corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != r.NodeName {
			continue
		}
		// Terminating Pods remain active until the device inventory proves that
		// teardown finished. Retained terminal Pods do not block on their own.
		if pod.DeletionTimestamp == nil &&
			(pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed) {
			continue
		}
		active = append(active, *pod.DeepCopy())
	}
	return active, nil
}

func nonNilPodCount(pods []*corev1.Pod) int {
	count := 0
	for _, pod := range pods {
		if pod != nil {
			count++
		}
	}
	return count
}

func (r *Reconciler) apiReader() client.Reader {
	if r.Reader != nil {
		return r.Reader
	}
	return r.Client
}

func addManagedMutationClaim(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	stage opsv1alpha1.UpgradeManagedMutationStage,
	policyEpoch int64,
	revision int64,
	now time.Time,
) error {
	if up.Status.ManagerAdmission == nil || strings.TrimSpace(up.Status.ManagerAdmission.ReservationID) == "" {
		return fmt.Errorf("managed %s claim has no admitted reservation", stage)
	}
	for i := range up.Status.ManagedMutationClaims {
		if up.Status.ManagedMutationClaims[i].Stage == stage {
			return fmt.Errorf("managed %s claim already exists without its durable mutation marker", stage)
		}
	}
	up.Status.ManagedMutationClaims = append(up.Status.ManagedMutationClaims, opsv1alpha1.UpgradeManagedMutationClaimStatus{
		Stage:           stage,
		ReservationID:   up.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:     policyEpoch,
		ControlRevision: revision,
		ClaimedAt:       metav1.Time{Time: now},
	})
	sort.Slice(up.Status.ManagedMutationClaims, func(i, j int) bool {
		return up.Status.ManagedMutationClaims[i].Stage < up.Status.ManagedMutationClaims[j].Stage
	})
	return nil
}

func validateManagedClaimCoverage(up *opsv1alpha1.IOSXESoftwareUpgrade, currentRevision int64) error {
	if up.Status.ManagerAdmission == nil {
		return fmt.Errorf("managed mutation claims have no manager admission")
	}
	claims := make(map[opsv1alpha1.UpgradeManagedMutationStage]opsv1alpha1.UpgradeManagedMutationClaimStatus,
		len(up.Status.ManagedMutationClaims))
	for _, claim := range up.Status.ManagedMutationClaims {
		if _, exists := claims[claim.Stage]; exists {
			return fmt.Errorf("duplicate managed mutation claim for stage %s", claim.Stage)
		}
		if !knownManagedMutationStage(claim.Stage) {
			return fmt.Errorf("unknown managed mutation claim stage %q", claim.Stage)
		}
		if claim.ReservationID != up.Status.ManagerAdmission.ReservationID {
			return fmt.Errorf("managed %s claim reservation %q does not match admission %q",
				claim.Stage, claim.ReservationID, up.Status.ManagerAdmission.ReservationID)
		}
		if claim.PolicyEpoch < 1 || claim.PolicyEpoch != up.Status.ManagerAdmission.PolicyEpoch {
			return fmt.Errorf("managed %s claim policy epoch %d does not match admission epoch %d",
				claim.Stage, claim.PolicyEpoch, up.Status.ManagerAdmission.PolicyEpoch)
		}
		if claim.ControlRevision < 0 || claim.ControlRevision > currentRevision {
			return fmt.Errorf("managed %s claim revision %d is outside current manager revision %d",
				claim.Stage, claim.ControlRevision, currentRevision)
		}
		if claim.ClaimedAt.IsZero() {
			return fmt.Errorf("managed %s claim has no durable claim timestamp", claim.Stage)
		}
		claims[claim.Stage] = claim
	}
	for stage, marked := range managedMutationMarkers(up) {
		_, claimed := claims[stage]
		if marked != claimed {
			return fmt.Errorf("managed %s durable marker/claim mismatch: marker=%t claim=%t", stage, marked, claimed)
		}
	}
	return nil
}

func managedMutationMarkers(up *opsv1alpha1.IOSXESoftwareUpgrade) map[opsv1alpha1.UpgradeManagedMutationStage]bool {
	return map[opsv1alpha1.UpgradeManagedMutationStage]bool{
		opsv1alpha1.UpgradeManagedMutationStaging:            stagingRequestSubmitted(up),
		opsv1alpha1.UpgradeManagedMutationPrimaryInstall:     up.Status.PrimarySupervisorInstallRequested,
		opsv1alpha1.UpgradeManagedMutationStandbyInstall:     up.Status.StandbySupervisorInstallRequested,
		opsv1alpha1.UpgradeManagedMutationStandbyActivation:  up.Status.StandbySupervisorActivationRequested,
		opsv1alpha1.UpgradeManagedMutationPrimaryActivation:  primaryActivationRequestSubmitted(up),
		opsv1alpha1.UpgradeManagedMutationRollbackActivation: rollbackRequestSubmitted(up),
	}
}

func managedMutationNeedsObservation(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if up == nil || terminalUpgradePhase(up.Status.Phase) {
		return false
	}
	switch up.Status.Phase {
	case opsv1alpha1.UpgradePhaseStaging, opsv1alpha1.UpgradePhaseValidating:
		return stagingRequestSubmitted(up)
	case opsv1alpha1.UpgradePhaseTransferring, opsv1alpha1.UpgradePhaseTransferInterrupted:
		return up.Status.PrimarySupervisorInstallRequested && !up.Status.PrimarySupervisorInstalled ||
			up.Status.StandbySupervisorInstallRequested && !up.Status.StandbySupervisorInstalled
	case opsv1alpha1.UpgradePhaseActivating, opsv1alpha1.UpgradePhaseAwaitingReachability, opsv1alpha1.UpgradePhaseVerifying:
		return up.Status.StandbySupervisorActivationRequested && !up.Status.StandbySupervisorActivated ||
			primaryActivationRequestSubmitted(up)
	case opsv1alpha1.UpgradePhaseRollingBack:
		return rollbackRequestSubmitted(up)
	default:
		return false
	}
}

func managedStatusCASMatches(expected, current *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if expected == nil || current == nil || expected.UID != current.UID {
		return false
	}
	for _, key := range []string{
		managedprotocol.AnnotationManaged,
		managedprotocol.AnnotationDeviceNamespace,
		managedprotocol.AnnotationDeviceName,
		managedprotocol.AnnotationDeviceUID,
		managedprotocol.AnnotationDeviceGeneration,
		managedprotocol.AnnotationNodeName,
		managedprotocol.AnnotationNodeUID,
		managedprotocol.AnnotationWorkerUsername,
		managedprotocol.AnnotationWorkerProtocol,
		managedprotocol.AnnotationCampaignUID,
		managedprotocol.AnnotationPlanHash,
		managedprotocol.AnnotationLedgerUID,
		managedprotocol.AnnotationReservationID,
		managedprotocol.AnnotationSourceSecretUID,
	} {
		if expected.Annotations[key] != current.Annotations[key] {
			return false
		}
	}
	return reflect.DeepEqual(expected.Status.ManagerAdmission, current.Status.ManagerAdmission) &&
		reflect.DeepEqual(expected.Status.ManagerControl, current.Status.ManagerControl) &&
		reflect.DeepEqual(expected.Status.WorkerControl, current.Status.WorkerControl) &&
		reflect.DeepEqual(expected.Status.ManagedMutationClaims, current.Status.ManagedMutationClaims)
}

func workerControlForDecision(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	decision managedLeafDecision,
	now time.Time,
) *opsv1alpha1.UpgradeWorkerControlStatus {
	desired := &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState:       decision.admissionState,
		ObservedPolicyEpoch:          decision.policyEpoch,
		ObservedControlRevision:      decision.controlRevision,
		ObservedWorkerConfigRevision: decision.workerRevision,
		EffectiveState:               decision.effectiveState,
		UpdatedAt:                    metav1.Time{Time: now},
		Message:                      boundedWorkerMessage(decision.message),
	}
	if existing := up.Status.WorkerControl; existing != nil &&
		existing.ObservedAdmissionState == desired.ObservedAdmissionState &&
		existing.ObservedPolicyEpoch == desired.ObservedPolicyEpoch &&
		existing.ObservedControlRevision == desired.ObservedControlRevision &&
		existing.ObservedWorkerConfigRevision == desired.ObservedWorkerConfigRevision &&
		existing.EffectiveState == desired.EffectiveState &&
		existing.Message == desired.Message {
		return existing.DeepCopy()
	}
	return desired
}

func applyWorkerControlDecision(up *opsv1alpha1.IOSXESoftwareUpgrade, decision managedLeafDecision, now time.Time) bool {
	desired := workerControlForDecision(up, decision, now)
	if reflect.DeepEqual(up.Status.WorkerControl, desired) {
		return false
	}
	up.Status.WorkerControl = desired
	return true
}

func safeAdmissionState(admission *opsv1alpha1.UpgradeManagerAdmissionStatus) opsv1alpha1.UpgradeManagerAdmissionState {
	if admission == nil {
		return opsv1alpha1.UpgradeManagerAdmissionPending
	}
	switch admission.State {
	case opsv1alpha1.UpgradeManagerAdmissionPending,
		opsv1alpha1.UpgradeManagerAdmissionGranted,
		opsv1alpha1.UpgradeManagerAdmissionRevoked,
		opsv1alpha1.UpgradeManagerAdmissionSettled:
		return admission.State
	default:
		return opsv1alpha1.UpgradeManagerAdmissionPending
	}
}

func requireAnnotation(annotations map[string]string, key, expected string) error {
	actual, ok := annotations[key]
	if !ok || actual != expected {
		return fmt.Errorf("annotation %s=%q, want %q", key, actual, expected)
	}
	return nil
}

func boundedWorkerMessage(message string) string {
	const limit = 256
	runes := []rune(message)
	if len(runes) <= limit {
		return message
	}
	return string(runes[:limit])
}

func knownManagedMutationStage(stage opsv1alpha1.UpgradeManagedMutationStage) bool {
	switch stage {
	case opsv1alpha1.UpgradeManagedMutationStaging,
		opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
		opsv1alpha1.UpgradeManagedMutationStandbyInstall,
		opsv1alpha1.UpgradeManagedMutationStandbyActivation,
		opsv1alpha1.UpgradeManagedMutationPrimaryActivation,
		opsv1alpha1.UpgradeManagedMutationRollbackActivation:
		return true
	default:
		return false
	}
}

// retryOnConflict stays local to the managed protocol helper so all of its
// fresh-read acknowledgements use the same Kubernetes conflict semantics as
// the existing durable mutation claims.
func retryOnConflict(fn func() error) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, fn)
}
