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
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

const (
	drainSafeLabel          = "operations.cisco.vk/drain-safe"
	drainMaintenanceTaint   = "cisco.vk/device-maintenance"
	drainMaintenanceValue   = "gnoi"
	drainRecoveryExtraLimit = 120 * time.Second
	drainMaximumDuration    = 2 * time.Hour
)

var errDrainSafetyBlocked = errors.New("managed workload drain is blocked")

func rolloutUsesWorkloadDrain(rollout *opsv1alpha1.IOSXESoftwareRollout) bool {
	return rollout != nil && rollout.Spec.Plan.Workloads.Policy == opsv1alpha1.IOSXESoftwareRolloutWorkloadDrain &&
		rollout.Spec.Plan.Workloads.Drain != nil
}

func requiresDrainRecovery(leaf *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	return leaf != nil && leaf.Status.ManagerDrain != nil &&
		leaf.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainSettled
}

// rejectGenericDrainRelease is the final cross-path guard against erasing
// drain provenance through ordinary zero-claim cleanup. Once ManagerDrain is
// published, only SettleDrainedReservation may release its acquisition.
func (r *IOSXESoftwareRolloutReconciler) rejectGenericDrainRelease(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	childUID string,
) error {
	if rollout == nil || target.ChildName == "" {
		return nil
	}
	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}, &leaf)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if childUID != "" && string(leaf.UID) != childUID {
		return fmt.Errorf("refuse generic release against a replacement leaf")
	}
	if leaf.Status.ManagerDrain != nil {
		return fmt.Errorf("%w: retained drain session requires exact drain recovery and settlement", errDrainSafetyBlocked)
	}
	return nil
}

// applyManagerDrainControl preserves the already-promoted mutation grant while
// paused: WorkerControl prevents a new claim, and a later resume can continue
// the same exact session. Before promotion, pause must instead close Eviction
// authority so accepted teardown can converge only through recovery.
func applyManagerDrainControl(
	drain *opsv1alpha1.UpgradeManagerDrainStatus,
	pause, cancel bool,
	controlRevision int64,
	now time.Time,
) {
	if drain == nil || drain.State == opsv1alpha1.UpgradeManagerDrainSettled {
		return
	}
	if cancel || (pause && drain.State != opsv1alpha1.UpgradeManagerDrainPromoted) ||
		drain.State == opsv1alpha1.UpgradeManagerDrainRecovering {
		markManagerDrainRecovering(drain, controlRevision, now)
		return
	}
	if controlRevision > drain.ControlRevision {
		drain.ControlRevision = controlRevision
		drain.UpdatedAt = metav1.NewTime(now)
	}
}

func applyManagerDrainFence(
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	pause, cancel, recoveryRequired bool,
	controlRevision int64,
	now time.Time,
) {
	if !requiresDrainRecovery(leaf) {
		return
	}
	drain := leaf.Status.ManagerDrain
	if len(leaf.Status.ManagedMutationClaims) != 0 && !leafMutationOutcomeResolved(leaf) {
		// Control/admission fence future claims, but an already accepted device
		// mutation keeps the scheduling guard until its outcome is conclusive.
		if controlRevision > drain.ControlRevision {
			drain.ControlRevision = controlRevision
			drain.UpdatedAt = metav1.NewTime(now)
		}
		return
	}
	if recoveryRequired {
		markManagerDrainRecovering(drain, controlRevision, now)
		return
	}
	applyManagerDrainControl(drain, pause, cancel, controlRevision, now)
}

// validateDrainAdmission proves that every active workload currently bound to
// the target is in the campaign's small, explicitly portable drain subset. It
// has no side effects; the exact evidence is rebuilt immediately before the
// manager starts a drain session.
func (r *IOSXESoftwareRolloutReconciler) validateDrainAdmission(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) error {
	_, err := r.snapshotDrainPods(ctx, rollout, target)
	return err
}

func (r *IOSXESoftwareRolloutReconciler) snapshotDrainPods(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) ([]opsv1alpha1.UpgradeDrainPodStatus, error) {
	if !rolloutUsesWorkloadDrain(rollout) {
		return nil, fmt.Errorf("%w: campaign did not explicitly select Drain", errDrainSafetyBlocked)
	}
	drainSpec := rollout.Spec.Plan.Workloads.Drain
	allowed := make(map[string]struct{}, len(drainSpec.Namespaces))
	for _, namespace := range drainSpec.Namespaces {
		allowed[namespace] = struct{}{}
	}
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.MatchingFields{rolloutPodNodeNameIndex: target.NodeName}); err != nil {
		return nil, fmt.Errorf("list drain workloads bound to Node %q: %w", target.NodeName, err)
	}
	candidates := make([]opsv1alpha1.UpgradeDrainPodStatus, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != target.NodeName ||
			(pod.DeletionTimestamp == nil && (pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed)) {
			continue
		}
		if _, ok := allowed[pod.Namespace]; !ok {
			return nil, fmt.Errorf("%w: workload %s/%s is outside the campaign drain namespaces", errDrainSafetyBlocked, pod.Namespace, pod.Name)
		}
		if pod.DeletionTimestamp != nil {
			return nil, fmt.Errorf("%w: terminating workload %s/%s cannot be adopted into a new drain session", errDrainSafetyBlocked, pod.Namespace, pod.Name)
		}
		candidate, err := r.drainPodEvidence(ctx, pod, drainSpec)
		if err != nil {
			return nil, fmt.Errorf("%w: workload %s/%s: %v", errDrainSafetyBlocked, pod.Namespace, pod.Name, err)
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Namespace != candidates[j].Namespace {
			return candidates[i].Namespace < candidates[j].Namespace
		}
		if candidates[i].Name != candidates[j].Name {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].UID < candidates[j].UID
	})
	if len(candidates) > int(drainSpec.MaxPods) {
		return nil, fmt.Errorf("%w: %d workloads exceed maxPods %d", errDrainSafetyBlocked, len(candidates), drainSpec.MaxPods)
	}
	return candidates, nil
}

func (r *IOSXESoftwareRolloutReconciler) drainPodEvidence(
	ctx context.Context,
	pod *corev1.Pod,
	drainSpec *opsv1alpha1.IOSXESoftwareRolloutDrainSpec,
) (opsv1alpha1.UpgradeDrainPodStatus, error) {
	if pod == nil || pod.UID == "" || pod.Namespace == "" || pod.Name == "" {
		return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("Pod identity is incomplete")
	}
	if pod.Labels[drainSafeLabel] != "true" {
		return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("Pod lacks %s=true", drainSafeLabel)
	}
	if pod.Spec.SchedulerName != "" && pod.Spec.SchedulerName != corev1.DefaultSchedulerName {
		return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("non-default scheduler %q is not drain eligible", pod.Spec.SchedulerName)
	}
	if err := validateDrainPodSpec(&pod.Spec); err != nil {
		return opsv1alpha1.UpgradeDrainPodStatus{}, err
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("Pod has no controlling owner")
	}
	controller, workloadController, templates, err := r.resolveDrainController(ctx, pod.Namespace, owner)
	if err != nil {
		return opsv1alpha1.UpgradeDrainPodStatus{}, err
	}
	for name, template := range templates {
		if template.Labels[drainSafeLabel] != "true" {
			return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("%s template lacks %s=true", name, drainSafeLabel)
		}
		if template.Spec.NodeName != "" {
			return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("%s template pins nodeName", name)
		}
		if template.Spec.SchedulerName != "" && template.Spec.SchedulerName != corev1.DefaultSchedulerName {
			return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("%s template uses a non-default scheduler", name)
		}
		if err := validateDrainPodSpec(&template.Spec); err != nil {
			return opsv1alpha1.UpgradeDrainPodStatus{}, fmt.Errorf("%s template: %w", name, err)
		}
	}
	pdbs, err := r.drainPDBEvidence(ctx, pod)
	if err != nil {
		return opsv1alpha1.UpgradeDrainPodStatus{}, err
	}
	grace := int64(30)
	if pod.Spec.TerminationGracePeriodSeconds != nil {
		grace = *pod.Spec.TerminationGracePeriodSeconds
	}
	if grace < 1 {
		grace = 1
	}
	if grace > int64(drainSpec.MaxTerminationGraceSeconds) {
		grace = int64(drainSpec.MaxTerminationGraceSeconds)
	}
	candidate := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
		Controller: controller, WorkloadController: workloadController, PDBs: pdbs,
		TerminationGracePeriodSeconds: grace, Phase: opsv1alpha1.UpgradeDrainPodSelected,
	}
	hash, err := workloaddrain.EligibilityHash(&candidate)
	if err != nil {
		return opsv1alpha1.UpgradeDrainPodStatus{}, err
	}
	candidate.EligibilityHash = hash
	return candidate, nil
}

func validateDrainPodSpec(spec *corev1.PodSpec) error {
	if spec == nil {
		return fmt.Errorf("Pod spec is missing")
	}
	if len(spec.NodeSelector) != 0 {
		return fmt.Errorf("nodeSelector is not supported by the portable drain contract")
	}
	if spec.Affinity != nil {
		if spec.Affinity.NodeAffinity != nil &&
			spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
			return fmt.Errorf("required node affinity is not supported by the portable drain contract")
		}
		if spec.Affinity.PodAffinity != nil &&
			len(spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			return fmt.Errorf("required Pod affinity is not supported by the portable drain contract")
		}
		if spec.Affinity.PodAntiAffinity != nil &&
			len(spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			return fmt.Errorf("required Pod anti-affinity is not supported by the portable drain contract")
		}
	}
	for _, constraint := range spec.TopologySpreadConstraints {
		if constraint.WhenUnsatisfiable == corev1.DoNotSchedule {
			return fmt.Errorf("hard topology spread is not supported by the portable drain contract")
		}
	}
	for _, toleration := range spec.Tolerations {
		matchesNoSchedule := toleration.Effect == "" || toleration.Effect == corev1.TaintEffectNoSchedule
		matchesMaintenance := toleration.Key == drainMaintenanceTaint &&
			(toleration.Operator == corev1.TolerationOpExists || toleration.Value == drainMaintenanceValue)
		matchesEveryTaint := toleration.Key == "" && toleration.Operator == corev1.TolerationOpExists
		if matchesNoSchedule && (matchesMaintenance || matchesEveryTaint) {
			return fmt.Errorf("maintenance-taint toleration can bypass the owned scheduling guard")
		}
	}
	for _, volume := range spec.Volumes {
		source := volume.VolumeSource
		if source.Secret == nil && source.ConfigMap == nil && source.Projected == nil && source.DownwardAPI == nil {
			return fmt.Errorf("volume %q is not in the portable Secret/ConfigMap/Projected/DownwardAPI subset", volume.Name)
		}
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) resolveDrainController(
	ctx context.Context,
	namespace string,
	owner *metav1.OwnerReference,
) (opsv1alpha1.UpgradeDrainObjectReference, *opsv1alpha1.UpgradeDrainObjectReference, map[string]corev1.PodTemplateSpec, error) {
	if owner == nil || owner.UID == "" || owner.Name == "" || owner.APIVersion != appsv1.SchemeGroupVersion.String() {
		return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("controlling owner is not an exact apps/v1 object")
	}
	switch owner.Kind {
	case "ReplicaSet":
		var replicaSet appsv1.ReplicaSet
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: namespace, Name: owner.Name}, &replicaSet); err != nil {
			return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("read ReplicaSet controller: %w", err)
		}
		if replicaSet.UID != owner.UID || replicaSet.Generation < 1 || !replicaSet.DeletionTimestamp.IsZero() {
			return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("ReplicaSet controller incarnation is stale or terminating")
		}
		ref := drainObjectReference(appsv1.SchemeGroupVersion.String(), "ReplicaSet", &replicaSet.ObjectMeta)
		templates := map[string]corev1.PodTemplateSpec{"ReplicaSet": replicaSet.Spec.Template}
		parent := metav1.GetControllerOf(&replicaSet)
		if parent == nil {
			return ref, nil, templates, nil
		}
		if parent.APIVersion != appsv1.SchemeGroupVersion.String() || parent.Kind != "Deployment" || parent.UID == "" {
			return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("ReplicaSet has an unsupported controlling owner")
		}
		var deployment appsv1.Deployment
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: namespace, Name: parent.Name}, &deployment); err != nil {
			return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("read Deployment controller: %w", err)
		}
		if deployment.UID != parent.UID || deployment.Generation < 1 || !deployment.DeletionTimestamp.IsZero() {
			return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("Deployment controller incarnation is stale or terminating")
		}
		workload := drainObjectReference(appsv1.SchemeGroupVersion.String(), "Deployment", &deployment.ObjectMeta)
		templates["Deployment"] = deployment.Spec.Template
		return ref, &workload, templates, nil
	default:
		return opsv1alpha1.UpgradeDrainObjectReference{}, nil, nil, fmt.Errorf("controller kind %q is not a qualified ReplicaSet", owner.Kind)
	}
}

func drainObjectReference(apiVersion, kind string, object *metav1.ObjectMeta) opsv1alpha1.UpgradeDrainObjectReference {
	return opsv1alpha1.UpgradeDrainObjectReference{
		APIVersion: apiVersion, Kind: kind, Namespace: object.Namespace, Name: object.Name,
		UID: string(object.UID), Generation: object.Generation,
	}
}

func (r *IOSXESoftwareRolloutReconciler) drainPDBEvidence(
	ctx context.Context,
	pod *corev1.Pod,
) ([]opsv1alpha1.UpgradeDrainPDBStatus, error) {
	var list policyv1.PodDisruptionBudgetList
	if err := r.reader().List(ctx, &list, client.InNamespace(pod.Namespace)); err != nil {
		return nil, fmt.Errorf("list policy/v1 PodDisruptionBudgets: %w", err)
	}
	matched := make([]opsv1alpha1.UpgradeDrainPDBStatus, 0, 1)
	for i := range list.Items {
		pdb := &list.Items[i]
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			return nil, fmt.Errorf("PodDisruptionBudget %s has an invalid selector: %w", pdb.Name, err)
		}
		if !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		if pdb.UID == "" || pdb.Generation < 1 || !pdb.DeletionTimestamp.IsZero() ||
			pdb.Status.ObservedGeneration != pdb.Generation || pdb.Status.DisruptionsAllowed < 1 ||
			pdb.Status.CurrentHealthy < pdb.Status.DesiredHealthy ||
			pdb.Status.ExpectedPods < pdb.Status.CurrentHealthy || pdb.Status.ExpectedPods < 1 {
			return nil, fmt.Errorf("PodDisruptionBudget %s lacks fresh healthy disruption evidence", pdb.Name)
		}
		matched = append(matched, opsv1alpha1.UpgradeDrainPDBStatus{
			UpgradeDrainObjectReference: drainObjectReference(policyv1.SchemeGroupVersion.String(), "PodDisruptionBudget", &pdb.ObjectMeta),
			ObservedGeneration:          pdb.Status.ObservedGeneration, DisruptionsAllowed: pdb.Status.DisruptionsAllowed,
			CurrentHealthy: pdb.Status.CurrentHealthy, DesiredHealthy: pdb.Status.DesiredHealthy,
			ExpectedPods: pdb.Status.ExpectedPods,
		})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].UID < matched[j].UID })
	// The upstream EvictionREST path rejects a Pod selected by more than one
	// PDB. Freeze exactly the one policy/v1 object the apiserver can enforce.
	if len(matched) != 1 {
		return nil, fmt.Errorf("exactly one matching policy/v1 PodDisruptionBudget is required; found %d", len(matched))
	}
	return matched, nil
}

// reconcileDrainGrant advances at most one externally observable step. This is
// deliberately slower than a bulk drain: every API transition is a durable
// crash boundary, and only the Eviction subresource may initiate termination.
func (r *IOSXESoftwareRolloutReconciler) reconcileDrainGrant(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (bool, error) {
	if !rolloutUsesWorkloadDrain(rollout) {
		return false, fmt.Errorf("drain leaf exists for a campaign without explicit Drain policy")
	}
	if leaf.Status.ManagerDrain == nil {
		if err := currentPolicy.ValidateWorkloadPolicy(rollout.Spec.Plan.Workloads); err != nil {
			return false, fmt.Errorf("drain is no longer administrator-authorized: %w", err)
		}
		if err := r.ensureDrainGrantHandshake(ctx, rollout, currentPolicy, effectivePolicy, target, leaf); err != nil {
			return false, err
		}
		pods, err := r.snapshotDrainPods(ctx, rollout, target)
		if err != nil {
			return false, err
		}
		var node corev1.Node
		if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &node); err != nil {
			return false, fmt.Errorf("read Node before drain guard: %w", err)
		}
		if string(node.UID) != target.NodeUID {
			return false, fmt.Errorf("target Node incarnation changed before drain")
		}
		started := metav1.NewTime(now.UTC())
		deadline := metav1.NewTime(now.Add(time.Duration(rollout.Spec.Plan.Workloads.Drain.TimeoutSeconds) * time.Second).UTC())
		managerDrain := &opsv1alpha1.UpgradeManagerDrainStatus{
			ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
			State:           opsv1alpha1.UpgradeManagerDrainPreparing,
			SessionToken:    uuid.NewString(), ReservationID: leaf.Status.ManagerAdmission.ReservationID,
			PolicyEpoch: effectivePolicy.Epoch, ControlRevision: rollout.Spec.Control.Revision,
			NodeUID: target.NodeUID, NodeUnschedulableBefore: node.Spec.Unschedulable,
			MaintenanceTaintPresentBefore: hasDrainMaintenanceTaint(node.Spec.Taints),
			StartedAt:                     started, DrainDeadline: deadline, UpdatedAt: started, Pods: pods,
		}
		if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
			if current.UID != leaf.UID || current.Status.ManagerDrain != nil {
				return fmt.Errorf("drain leaf changed while publishing its immutable session")
			}
			if err := validateManagerAdmission(rollout, target, current); err != nil {
				return err
			}
			current.Status.ManagerDrain = managerDrain
			return nil
		}); err != nil {
			return false, err
		}
		return false, nil
	}
	drain := leaf.Status.ManagerDrain
	if err := validateManagerDrainBinding(rollout, target, leaf); err != nil {
		return false, err
	}
	if drain.State == opsv1alpha1.UpgradeManagerDrainRecovering || drain.State == opsv1alpha1.UpgradeManagerDrainSettled {
		return false, nil
	}
	if rollout.Spec.Control.Cancel ||
		(rollout.Spec.Control.Pause && drain.State != opsv1alpha1.UpgradeManagerDrainPromoted) ||
		(drain.State != opsv1alpha1.UpgradeManagerDrainPromoted && !now.Before(drain.DrainDeadline.Time)) {
		return false, r.enterDrainRecovery(ctx, leaf, rollout.Spec.Control.Revision, now)
	}
	if rollout.Spec.Control.Pause {
		return false, nil
	}
	if err := currentPolicy.ValidateWorkloadPolicy(rollout.Spec.Plan.Workloads); err != nil {
		return false, r.enterDrainRecovery(ctx, leaf, rollout.Spec.Control.Revision, now)
	}
	if drain.State != opsv1alpha1.UpgradeManagerDrainPromoted {
		controlReady, err := r.drainControlReady(ctx, rollout, target, leaf)
		if err != nil || !controlReady {
			return false, err
		}
	}
	switch drain.State {
	case opsv1alpha1.UpgradeManagerDrainPreparing:
		ready, err := r.drainGuardReady(ctx, rollout, target, leaf)
		if err != nil || !ready {
			return false, err
		}
		if err := r.ensureDrainLeaseIdle(ctx, rollout, target, leaf); err != nil {
			return false, err
		}
		if err := r.ensureDrainLedgerStarted(ctx, rollout, leaf); err != nil {
			return false, err
		}
		allowedUIDs := make(map[string]struct{}, len(drain.Pods))
		for i := range drain.Pods {
			allowedUIDs[drain.Pods[i].UID] = struct{}{}
		}
		if err := r.ensureDrainNodeEmpty(ctx, target.NodeName, allowedUIDs); err != nil {
			return false, r.enterDrainRecovery(ctx, leaf, rollout.Spec.Control.Revision, now)
		}
		for i := range drain.Pods {
			if drain.Pods[i].Phase != opsv1alpha1.UpgradeDrainPodSelected {
				continue
			}
			if err := r.protectDrainPod(ctx, rollout, target, leaf, &drain.Pods[i],
				drain.SessionToken, rollout.Spec.Plan.Workloads.Drain); err != nil {
				return false, r.enterDrainRecovery(ctx, leaf, rollout.Spec.Control.Revision, now)
			}
			if err := r.patchDrainPodPhase(ctx, leaf, drain.Pods[i].UID,
				opsv1alpha1.UpgradeDrainPodSelected, opsv1alpha1.UpgradeDrainPodProtected, now, 0); err != nil {
				// A previous leader can finish the Pod patch after recovery has
				// advanced the durable phase. Compensate its exact session-owned
				// protection when the status CAS proves this protection lost the race.
				terminationAccepted, cleanupErr := r.releaseUnacceptedDrainPodProtection(
					ctx, &drain.Pods[i], drain.SessionToken, false,
				)
				if terminationAccepted {
					cleanupErr = errors.Join(cleanupErr, fmt.Errorf(
						"%w: Pod began terminating while stale protection was being compensated",
						errDrainSafetyBlocked,
					))
				}
				return false, errors.Join(err, cleanupErr)
			}
			return false, nil
		}
		return false, r.patchManagerDrainState(ctx, leaf,
			opsv1alpha1.UpgradeManagerDrainPreparing, opsv1alpha1.UpgradeManagerDrainGuarded, now)
	case opsv1alpha1.UpgradeManagerDrainGuarded:
		if err := r.ensureDrainLedgerStarted(ctx, rollout, leaf); err != nil {
			return false, err
		}
		ready, err := r.drainGuardReady(ctx, rollout, target, leaf)
		if err != nil || !ready {
			return false, err
		}
		return false, r.patchManagerDrainState(ctx, leaf,
			opsv1alpha1.UpgradeManagerDrainGuarded, opsv1alpha1.UpgradeManagerDrainEvicting, now)
	case opsv1alpha1.UpgradeManagerDrainEvicting:
		if err := r.ensureDrainLedgerStarted(ctx, rollout, leaf); err != nil {
			return false, err
		}
		return r.reconcileDrainEvictions(ctx, rollout, target, leaf, now)
	case opsv1alpha1.UpgradeManagerDrainDrained:
		if err := r.ensureDrainLedgerStarted(ctx, rollout, leaf); err != nil {
			return false, err
		}
		return false, r.patchManagerDrainState(ctx, leaf,
			opsv1alpha1.UpgradeManagerDrainDrained, opsv1alpha1.UpgradeManagerDrainPromoting, now)
	case opsv1alpha1.UpgradeManagerDrainPromoting:
		if err := r.revalidateDrainPromotion(ctx, rollout, currentPolicy, target, leaf); err != nil {
			return false, r.enterDrainRecovery(ctx, leaf, rollout.Spec.Control.Revision, now)
		}
		completedAt := drain.UpdatedAt.Time
		if err := r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
			var currentLeaf opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.reader().Get(ctx, client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
				return err
			}
			if currentLeaf.UID != leaf.UID || currentLeaf.Status.ManagerDrain == nil ||
				!currentLeaf.Status.ManagerDrain.UpdatedAt.Equal(&drain.UpdatedAt) {
				return fmt.Errorf("drain leaf changed before reservation promotion")
			}
			if err := r.revalidateDrainPromotion(ctx, rollout, currentPolicy, target, &currentLeaf); err != nil {
				return err
			}
			return topologyrollout.PromoteDrain(ledger, drain.ReservationID,
				rollout.Status.FrozenPlan.Policy.LedgerUID, string(leaf.UID), drain.SessionToken,
				uint64(rollout.Spec.Control.Revision), completedAt)
		}); err != nil {
			return false, fmt.Errorf("promote drained reservation: %w", err)
		}
		if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
			if err := validateManagerDrainBinding(rollout, target, current); err != nil {
				return err
			}
			if current.Status.ManagerDrain.State == opsv1alpha1.UpgradeManagerDrainPromoted &&
				current.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionGranted {
				return nil
			}
			if current.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainPromoting ||
				current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionPending {
				return fmt.Errorf("drain promotion status changed after ledger promotion")
			}
			revision := rollout.Spec.Control.Revision
			current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionGranted
			current.Status.ManagerAdmission.ControlRevision = &revision
			current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
			current.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainPromoted
			current.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now)
			return nil
		}); err != nil {
			return false, err
		}
		return true, nil
	case opsv1alpha1.UpgradeManagerDrainPromoted:
		return leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionGranted, nil
	default:
		return false, fmt.Errorf("unsupported manager drain state %q", drain.State)
	}
}

func (r *IOSXESoftwareRolloutReconciler) ensureDrainGrantHandshake(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if leaf.Status.ManagerAdmission == nil || leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionPending ||
		leaf.Status.ManagerAdmission.PolicyEpoch != effectivePolicy.Epoch || leaf.Status.ManagerAdmission.RevocationReason != "" {
		return fmt.Errorf("leaf has no current pending manager admission for drain")
	}
	control := leaf.Status.WorkerControl
	managerControl := leaf.Status.ManagerControl
	if control == nil || control.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		control.ObservedPolicyEpoch != effectivePolicy.Epoch || control.ObservedControlRevision != rollout.Spec.Control.Revision ||
		control.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady || managerControl == nil ||
		managerControl.Revision != rollout.Spec.Control.Revision || managerControl.Pause || managerControl.Cancel {
		return fmt.Errorf("worker has not acknowledged the exact pending drain admission")
	}
	_, worker, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
	if err != nil {
		return err
	}
	if leaf.Annotations[managedprotocol.AnnotationWorkerUsername] != worker {
		return fmt.Errorf("leaf worker username no longer matches the target")
	}
	_, err = r.requireWorkerRevisionAcknowledgement(ctx, rollout, target, control)
	return err
}

func validateManagerDrainBinding(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if !rolloutUsesWorkloadDrain(rollout) {
		return fmt.Errorf("manager drain is not backed by an explicit campaign Drain policy")
	}
	if err := validateManagerAdmission(rollout, target, leaf); err != nil {
		return err
	}
	drain := leaf.Status.ManagerDrain
	admission := leaf.Status.ManagerAdmission
	if drain == nil || drain.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		drain.SessionToken == "" || drain.ReservationID != admission.ReservationID ||
		drain.PolicyEpoch != admission.PolicyEpoch || drain.NodeUID != target.NodeUID ||
		admission.ControlRevision == nil || *admission.ControlRevision != drain.ControlRevision ||
		leaf.Status.ManagerControl == nil || leaf.Status.ManagerControl.Revision != drain.ControlRevision ||
		drain.StartedAt.IsZero() || drain.DrainDeadline.IsZero() || drain.UpdatedAt.IsZero() ||
		!drain.DrainDeadline.After(drain.StartedAt.Time) || drain.UpdatedAt.Before(&drain.StartedAt) {
		return fmt.Errorf("manager drain does not match the retained admission identity")
	}
	recovering := drain.State == opsv1alpha1.UpgradeManagerDrainRecovering ||
		drain.State == opsv1alpha1.UpgradeManagerDrainSettled
	if recovering != (drain.RecoveryDeadline != nil) ||
		(drain.RecoveryDeadline != nil && !drain.RecoveryDeadline.After(drain.StartedAt.Time)) {
		return fmt.Errorf("manager drain recovery deadline does not match its state")
	}
	if drain.RecoveryDeadline != nil {
		window, err := managerDrainRecoveryWindow(drain)
		if err != nil {
			return err
		}
		if drain.RecoveryDeadline.Time.After(drain.UpdatedAt.Add(window)) {
			return fmt.Errorf("manager drain recovery deadline exceeds its bounded window")
		}
	}
	switch drain.State {
	case opsv1alpha1.UpgradeManagerDrainPreparing, opsv1alpha1.UpgradeManagerDrainGuarded,
		opsv1alpha1.UpgradeManagerDrainEvicting, opsv1alpha1.UpgradeManagerDrainDrained,
		opsv1alpha1.UpgradeManagerDrainPromoting:
		if admission.State != opsv1alpha1.UpgradeManagerAdmissionPending {
			return fmt.Errorf("pre-promotion drain does not retain pending admission")
		}
	case opsv1alpha1.UpgradeManagerDrainPromoted:
		if admission.State != opsv1alpha1.UpgradeManagerAdmissionGranted &&
			admission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked {
			return fmt.Errorf("promoted drain does not retain granted or fenced admission")
		}
	case opsv1alpha1.UpgradeManagerDrainRecovering:
		if admission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			return fmt.Errorf("recovering drain cannot already have settled admission")
		}
	case opsv1alpha1.UpgradeManagerDrainSettled:
		if admission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
			return fmt.Errorf("settled drain does not retain settled admission")
		}
	default:
		return fmt.Errorf("manager drain has unsupported state %q", drain.State)
	}
	parsed, err := uuid.Parse(drain.SessionToken)
	if err != nil || parsed.String() != drain.SessionToken || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		return fmt.Errorf("manager drain session token is not a canonical UUIDv4")
	}
	if len(drain.Pods) > int(rollout.Spec.Plan.Workloads.Drain.MaxPods) {
		return fmt.Errorf("manager drain Pod snapshot exceeds the campaign bound")
	}
	seenUIDs := make(map[string]struct{}, len(drain.Pods))
	for i := range drain.Pods {
		pod := &drain.Pods[i]
		if pod.UID == "" || len(pod.PDBs) != 1 {
			return fmt.Errorf("manager drain Pod %s/%s lacks exact Pod or PDB identity", pod.Namespace, pod.Name)
		}
		if _, duplicate := seenUIDs[pod.UID]; duplicate {
			return fmt.Errorf("manager drain Pod UID %q is duplicated", pod.UID)
		}
		seenUIDs[pod.UID] = struct{}{}
		if err := workloaddrain.VerifyEligibilityHash(pod); err != nil {
			return fmt.Errorf("manager drain Pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}
	return nil
}

func validateDrainPodsComplete(drain *opsv1alpha1.UpgradeManagerDrainStatus) error {
	if drain == nil {
		return fmt.Errorf("manager drain is absent")
	}
	for i := range drain.Pods {
		pod := &drain.Pods[i]
		if err := workloaddrain.VerifyEligibilityHash(pod); err != nil {
			return fmt.Errorf("manager drain Pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
		if pod.Phase != opsv1alpha1.UpgradeDrainPodComplete {
			return fmt.Errorf("manager drain Pod %s/%s has not completed accepted teardown", pod.Namespace, pod.Name)
		}
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) drainControlReady(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) (bool, error) {
	drain := leaf.Status.ManagerDrain
	manager := leaf.Status.ManagerControl
	worker := leaf.Status.WorkerControl
	if drain == nil || drain.ControlRevision != rollout.Spec.Control.Revision || manager == nil ||
		manager.Revision != drain.ControlRevision || manager.Pause || manager.Cancel || worker == nil ||
		worker.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		worker.ObservedPolicyEpoch != drain.PolicyEpoch || worker.ObservedControlRevision != drain.ControlRevision ||
		worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		return false, nil
	}
	if _, err := r.requireWorkerRevisionAcknowledgement(ctx, rollout, target, worker); err != nil {
		return false, err
	}
	return true, nil
}

func (r *IOSXESoftwareRolloutReconciler) ensureDrainLedgerStarted(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	drain := leaf.Status.ManagerDrain
	return r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.BeginDrain(ledger, drain.ReservationID,
			rollout.Status.FrozenPlan.Policy.LedgerUID, string(leaf.UID), drain.SessionToken,
			uint64(drain.ControlRevision), drain.StartedAt.Time)
	})
}

func (r *IOSXESoftwareRolloutReconciler) drainGuardReady(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) (bool, error) {
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &node); err != nil {
		return false, fmt.Errorf("read drain Node guard: %w", err)
	}
	if string(node.UID) != target.NodeUID {
		return false, nil
	}
	drain := leaf.Status.ManagerDrain
	if !drainNodeGuardMatches(&node, drain) {
		return false, nil
	}
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return false, fmt.Errorf("read drain maintenance session: %w", err)
	}
	session := device.Status.MaintenanceSession
	if string(device.UID) != target.DeviceUID ||
		validateDrainSessionIdentity(session, target, leaf, drain) != nil {
		return false, nil
	}
	return session.Purpose == ciskov1.DeviceMaintenancePurposeWorkloadDrain &&
		session.Phase == ciskov1.DeviceMaintenanceSessionActive &&
		session.Lease.Holder == "software-drain/"+string(leaf.UID), nil
}

func drainNodeGuardMatches(node *corev1.Node, drain *opsv1alpha1.UpgradeManagerDrainStatus) bool {
	if node == nil || drain == nil || !node.Spec.Unschedulable || !hasDrainMaintenanceTaint(node.Spec.Taints) {
		return false
	}
	wantCordonOwner := drain.SessionToken
	if drain.NodeUnschedulableBefore {
		wantCordonOwner = ""
	}
	wantTaintOwner := drain.SessionToken
	if drain.MaintenanceTaintPresentBefore {
		wantTaintOwner = ""
	}
	return node.Annotations[managedprotocol.AnnotationDrainCordonOwner] == wantCordonOwner &&
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] == wantTaintOwner
}

func validateDrainSessionIdentity(
	session *ciskov1.DeviceMaintenanceSessionStatus,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	drain *opsv1alpha1.UpgradeManagerDrainStatus,
) error {
	if session == nil || drain == nil || session.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 ||
		session.SessionToken != drain.SessionToken || session.DeviceUID != target.DeviceUID ||
		session.NodeName != target.NodeName || session.NodeUID != target.NodeUID ||
		session.Operation.Namespace != leaf.Namespace || session.Operation.Name != leaf.Name ||
		session.Operation.UID != string(leaf.UID) || session.Lease.Namespace == "" || session.Lease.Name == "" ||
		session.Lease.UID == "" || session.ControlRevision != drain.ControlRevision ||
		!session.RequestedAt.Equal(&drain.StartedAt) || session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() {
		return fmt.Errorf("maintenance session does not match the exact drain, operation, device, Node, and Lease identity")
	}
	expectedHolder := ""
	switch session.Purpose {
	case ciskov1.DeviceMaintenancePurposeWorkloadDrain:
		expectedHolder = "software-drain/" + string(leaf.UID)
	case ciskov1.DeviceMaintenancePurposeSoftwareMutation:
		expectedHolder = "software-upgrade/" + string(leaf.UID)
	default:
		return fmt.Errorf("maintenance session has an unsupported drain purpose")
	}
	if session.Lease.Holder != expectedHolder {
		return fmt.Errorf("maintenance session Lease holder does not match its purpose")
	}
	return nil
}

func hasDrainMaintenanceTaint(taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Key == drainMaintenanceTaint && taint.Value == drainMaintenanceValue && taint.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func (r *IOSXESoftwareRolloutReconciler) protectDrainPod(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	sessionToken string,
	drainSpec *opsv1alpha1.IOSXESoftwareRolloutDrainSpec,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var pod corev1.Pod
		key := types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}
		if err := r.reader().Get(ctx, key, &pod); err != nil {
			return err
		}
		if string(pod.UID) != frozen.UID || pod.DeletionTimestamp != nil {
			return fmt.Errorf("selected Pod incarnation changed or began terminating before protection")
		}
		if err := r.validateFrozenDrainPod(ctx, &pod, frozen, drainSpec); err != nil {
			return err
		}
		owned, err := exactDrainPodProtection(&pod, sessionToken)
		if err != nil {
			return err
		}
		if owned {
			return nil
		}
		if err := r.authorizeDrainPodProtection(ctx, rollout, target, leaf, frozen); err != nil {
			return err
		}
		before := pod.DeepCopy()
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[managedprotocol.AnnotationDrainSession] = sessionToken
		controllerutil.AddFinalizer(&pod, managedprotocol.DrainPodFinalizer)
		return r.Patch(ctx, &pod, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}

// authorizeDrainPodProtection prevents an old Preparing reconcile from adding
// session ownership after recovery or terminal cleanup has advanced the leaf.
// The optimistic Pod patch still closes Pod-side races; this uncached leaf read
// closes the independent manager-status race immediately before that patch.
func (r *IOSXESoftwareRolloutReconciler) authorizeDrainPodProtection(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
) error {
	blocked := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", errDrainSafetyBlocked, fmt.Sprintf(format, args...))
	}
	if r.APIReader == nil || rollout == nil || rollout.UID == "" || leaf == nil || leaf.UID == "" ||
		leaf.Status.ManagerDrain == nil || frozen == nil {
		return blocked("uncached Pod-protection authority is incomplete")
	}
	expectedDrain := leaf.Status.ManagerDrain
	if expectedDrain.State != opsv1alpha1.UpgradeManagerDrainPreparing ||
		frozen.Phase != opsv1alpha1.UpgradeDrainPodSelected {
		return blocked("stale reconcile does not carry Preparing/Selected protection authority")
	}
	var currentRollout opsv1alpha1.IOSXESoftwareRollout
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(rollout), &currentRollout); err != nil {
		return blocked("re-read rollout before Pod protection: %v", err)
	}
	if currentRollout.UID != rollout.UID || !currentRollout.DeletionTimestamp.IsZero() ||
		currentRollout.Spec.Control.Revision != expectedDrain.ControlRevision ||
		currentRollout.Spec.Control.Pause || currentRollout.Spec.Control.Cancel ||
		currentRollout.Status.PolicyTransition != nil {
		return blocked("rollout control no longer authorizes Pod protection")
	}
	switch currentRollout.Status.Phase {
	case opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting, opsv1alpha1.IOSXESoftwareRolloutPhaseSoaking:
	default:
		return blocked("rollout phase %q does not authorize Pod protection", currentRollout.Status.Phase)
	}
	var currentLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		return blocked("re-read rollout leaf before Pod protection: %v", err)
	}
	if currentLeaf.UID != leaf.UID || !currentLeaf.DeletionTimestamp.IsZero() {
		return blocked("rollout leaf incarnation changed or is deleting")
	}
	if err := validateManagedLeafBinding(&currentRollout, target, &currentLeaf); err != nil {
		return blocked("rollout leaf binding changed: %v", err)
	}
	if err := validateManagerDrainBinding(&currentRollout, target, &currentLeaf); err != nil {
		return blocked("manager drain binding changed: %v", err)
	}
	currentDrain := currentLeaf.Status.ManagerDrain
	if currentDrain.ProtocolVersion != expectedDrain.ProtocolVersion ||
		currentDrain.State != opsv1alpha1.UpgradeManagerDrainPreparing ||
		currentDrain.SessionToken != expectedDrain.SessionToken ||
		currentDrain.ReservationID != expectedDrain.ReservationID ||
		currentDrain.PolicyEpoch != expectedDrain.PolicyEpoch ||
		currentDrain.ControlRevision != expectedDrain.ControlRevision ||
		currentDrain.NodeUID != expectedDrain.NodeUID ||
		!currentDrain.StartedAt.Equal(&expectedDrain.StartedAt) ||
		!currentDrain.DrainDeadline.Equal(&expectedDrain.DrainDeadline) ||
		!r.now().Before(currentDrain.DrainDeadline.Time) {
		return blocked("current drain session no longer authorizes Pod protection")
	}
	admission, managerControl, workerControl := currentLeaf.Status.ManagerAdmission,
		currentLeaf.Status.ManagerControl, currentLeaf.Status.WorkerControl
	if admission == nil || admission.State != opsv1alpha1.UpgradeManagerAdmissionPending ||
		admission.ControlRevision == nil || *admission.ControlRevision != currentDrain.ControlRevision ||
		admission.PolicyEpoch != currentDrain.PolicyEpoch || admission.RevocationReason != "" ||
		managerControl == nil || managerControl.Revision != currentDrain.ControlRevision ||
		managerControl.Pause || managerControl.Cancel || workerControl == nil ||
		workerControl.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		workerControl.ObservedPolicyEpoch != currentDrain.PolicyEpoch ||
		workerControl.ObservedControlRevision != currentDrain.ControlRevision ||
		workerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		return blocked("current drain admission or control no longer authorizes Pod protection")
	}
	for i := range currentDrain.Pods {
		candidate := &currentDrain.Pods[i]
		if candidate.UID != frozen.UID {
			continue
		}
		if candidate.Namespace != frozen.Namespace || candidate.Name != frozen.Name ||
			candidate.Phase != opsv1alpha1.UpgradeDrainPodSelected ||
			candidate.EligibilityHash != frozen.EligibilityHash {
			return blocked("current drain no longer carries exact Selected Pod authority")
		}
		return nil
	}
	return blocked("selected Pod is absent from the current drain session")
}

func (r *IOSXESoftwareRolloutReconciler) validateFrozenDrainPod(
	ctx context.Context,
	pod *corev1.Pod,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	drainSpec *opsv1alpha1.IOSXESoftwareRolloutDrainSpec,
) error {
	if string(pod.UID) != frozen.UID || pod.Namespace != frozen.Namespace || pod.Name != frozen.Name {
		return fmt.Errorf("selected Pod UID binding changed")
	}
	if err := workloaddrain.VerifyEligibilityHash(frozen); err != nil {
		return err
	}
	current, err := r.drainPodEvidence(ctx, pod, drainSpec)
	if err != nil {
		return err
	}
	if current.Controller != frozen.Controller || !reflect.DeepEqual(current.WorkloadController, frozen.WorkloadController) ||
		current.TerminationGracePeriodSeconds != frozen.TerminationGracePeriodSeconds {
		return fmt.Errorf("selected Pod controller or termination policy changed")
	}
	if len(current.PDBs) != len(frozen.PDBs) {
		return fmt.Errorf("selected Pod PDB set changed")
	}
	for i := range frozen.PDBs {
		if current.PDBs[i].UpgradeDrainObjectReference != frozen.PDBs[i].UpgradeDrainObjectReference {
			return fmt.Errorf("selected Pod PDB identity or generation changed")
		}
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) reconcileDrainEvictions(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (bool, error) {
	drain := leaf.Status.ManagerDrain
	ready, err := r.drainGuardReady(ctx, rollout, target, leaf)
	if err != nil || !ready {
		return false, err
	}
	allowedUIDs := make(map[string]struct{}, len(drain.Pods))
	for i := range drain.Pods {
		allowedUIDs[drain.Pods[i].UID] = struct{}{}
	}
	if err := r.ensureDrainNodeEmpty(ctx, target.NodeName, allowedUIDs); err != nil {
		return false, err
	}
	for i := range drain.Pods {
		podState := &drain.Pods[i]
		if podState.Phase == opsv1alpha1.UpgradeDrainPodComplete {
			ready, err := r.drainControllerReady(ctx, podState)
			if err != nil || !ready {
				return false, err
			}
			continue
		}
		return false, r.reconcileOneDrainPod(ctx, rollout, target, leaf, podState, now, false)
	}
	if err := r.ensureDrainNodeEmpty(ctx, target.NodeName, nil); err != nil {
		return false, err
	}
	if err := r.ensureDrainLeaseIdle(ctx, rollout, target, leaf); err != nil {
		return false, err
	}
	return false, r.patchManagerDrainState(ctx, leaf,
		opsv1alpha1.UpgradeManagerDrainEvicting, opsv1alpha1.UpgradeManagerDrainDrained, now)
}

func (r *IOSXESoftwareRolloutReconciler) reconcileOneDrainPod(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	now time.Time,
	recovering bool,
) error {
	if err := workloaddrain.VerifyEligibilityHash(frozen); err != nil {
		return err
	}
	key := types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}
	var pod corev1.Pod
	err := r.reader().Get(ctx, key, &pod)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	found := err == nil && string(pod.UID) == frozen.UID
	switch frozen.Phase {
	case opsv1alpha1.UpgradeDrainPodSelected:
		if !recovering {
			return fmt.Errorf("selected Pod reached eviction state without durable protection")
		}
		if !found {
			// An exact selected Pod cannot disappear after protection is committed:
			// the finalizer retains that incarnation. NotFound or an unprotected
			// replacement therefore proves there is no accepted teardown to finish.
			if err == nil {
				owned, protectionErr := exactDrainPodProtection(&pod, leaf.Status.ManagerDrain.SessionToken)
				if protectionErr != nil || owned {
					return fmt.Errorf("%w: replacement Pod carries drain protection", errDrainSafetyBlocked)
				}
			}
			return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
				opsv1alpha1.UpgradeDrainPodSelected, opsv1alpha1.UpgradeDrainPodComplete, now, 0)
		}
		owned, err := exactDrainPodProtection(&pod, leaf.Status.ManagerDrain.SessionToken)
		if err != nil {
			return err
		}
		if pod.DeletionTimestamp != nil {
			if !owned {
				return fmt.Errorf("%w: selected Pod began terminating before durable protection", errDrainSafetyBlocked)
			}
			// Protection was persisted but the status acknowledgement was lost.
			// Preserve that crash boundary first; the Protected path then records
			// accepted termination and requires strict device-clean evidence.
			return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
				opsv1alpha1.UpgradeDrainPodSelected, opsv1alpha1.UpgradeDrainPodProtected, now, 0)
		}
		if owned {
			// Protection can have been committed by a previous leader immediately
			// before its status acknowledgement was lost. Persist that boundary
			// first; the Protected recovery path releases it on a later reconcile.
			return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
				opsv1alpha1.UpgradeDrainPodSelected, opsv1alpha1.UpgradeDrainPodProtected, now, 0)
		}
		if err := r.patchDrainPodPhase(ctx, leaf, frozen.UID,
			opsv1alpha1.UpgradeDrainPodSelected, opsv1alpha1.UpgradeDrainPodComplete, now, 0); err != nil {
			return err
		}
		// Close the inverse cross-object race: a stale Preparing leader can add
		// protection after the read above but before this status CAS. Its own
		// failed phase CAS compensates too; this check covers its crash boundary.
		return r.ensureCompletedDrainPodUnprotected(ctx, frozen, leaf.Status.ManagerDrain.SessionToken)
	case opsv1alpha1.UpgradeDrainPodProtected:
		if !found {
			if !recovering {
				return fmt.Errorf("protected Pod disappeared before an accepted Eviction")
			}
			// Recovery may have removed exact protection immediately before a
			// manager crash. A missing exact Pod, or an unprotected replacement,
			// proves there is no accepted teardown left to finish. Partial or
			// foreign protection on a replacement remains quarantined.
			if err == nil {
				owned, protectionErr := exactDrainPodProtection(&pod, leaf.Status.ManagerDrain.SessionToken)
				if protectionErr != nil || owned {
					return fmt.Errorf("%w: replacement Pod carries drain protection", errDrainSafetyBlocked)
				}
			}
			return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
				opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodReleased, now, 0)
		}
		owned, err := exactDrainPodProtection(&pod, leaf.Status.ManagerDrain.SessionToken)
		if err != nil {
			return err
		}
		if pod.DeletionTimestamp != nil {
			if !owned {
				return fmt.Errorf("%w: terminating protected Pod lost exact drain ownership", errDrainSafetyBlocked)
			}
			// This is the crash/lost-response boundary after an Eviction request:
			// first durably record that accepted request, then observe termination
			// on a later reconcile. The admission contract intentionally forbids
			// skipping the accepted-teardown sequence.
			return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
				opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodEvictionRequested, now, 0)
		}
		if recovering {
			terminationAccepted, err := r.releaseUnacceptedDrainPodProtection(
				ctx, frozen, leaf.Status.ManagerDrain.SessionToken, true,
			)
			if err != nil {
				return err
			}
			if terminationAccepted {
				// Another policy/v1 Eviction won the optimistic Pod update. Preserve
				// protection, record accepted termination, and require the same strict
				// device-clean proof as a manager-initiated Eviction.
				return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
					opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodEvictionRequested, now, 0)
			}
			return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
				opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodReleased, now, 0)
		}
		if !owned {
			return fmt.Errorf("%w: active protected Pod lacks the exact drain marker and finalizer", errDrainSafetyBlocked)
		}
		if err := r.validateFrozenDrainPod(ctx, &pod, frozen, rollout.Spec.Plan.Workloads.Drain); err != nil {
			return fmt.Errorf("%w: selected Pod eligibility is no longer live: %v", errDrainSafetyBlocked, err)
		}
		pod, err = r.authorizeDrainEviction(ctx, rollout, target, leaf, frozen, &pod)
		if err != nil {
			return err
		}
		uid := pod.UID
		resourceVersion := pod.ResourceVersion
		grace := frozen.TerminationGracePeriodSeconds
		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name},
			DeleteOptions: &metav1.DeleteOptions{
				GracePeriodSeconds: &grace,
				Preconditions: &metav1.Preconditions{
					UID: &uid, ResourceVersion: &resourceVersion,
				},
			},
		}
		if err := r.SubResource("eviction").Create(ctx, &pod, eviction); err != nil {
			if apierrors.IsTooManyRequests(err) || apierrors.IsConflict(err) {
				return fmt.Errorf("%w: PDB or concurrent update blocked Eviction for %s/%s: %v", errDrainSafetyBlocked, pod.Namespace, pod.Name, err)
			}
			// A lost response after acceptance is resolved only from the exact
			// Pod's deletionTimestamp; never assume an error accepted eviction.
			var current corev1.Pod
			if getErr := r.reader().Get(ctx, key, &current); getErr == nil && current.UID == pod.UID && current.DeletionTimestamp != nil {
				return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
					opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodEvictionRequested, now, 0)
			}
			return fmt.Errorf("evict %s/%s through policy/v1: %w", pod.Namespace, pod.Name, err)
		}
		return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
			opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodEvictionRequested, now, 0)
	case opsv1alpha1.UpgradeDrainPodEvictionRequested:
		if !found {
			return fmt.Errorf("evicted Pod disappeared despite the manager cleanup finalizer")
		}
		if pod.DeletionTimestamp == nil {
			return nil
		}
		return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
			opsv1alpha1.UpgradeDrainPodEvictionRequested, opsv1alpha1.UpgradeDrainPodTerminationObserved, now, 0)
	case opsv1alpha1.UpgradeDrainPodTerminationObserved:
		_, ok := drainWorkerProvesPodClean(leaf, frozen)
		if !ok {
			return nil
		}
		// The worker publishes inventory before its retained delete mutation
		// Lease is released. Do not accept DeviceClean (and thereby make the
		// manager finalizer removable) until that exact canonical Lease is
		// wholly idle and request-free. A worker crash between publication and
		// its deferred release must leave this terminating Pod available to
		// drive the idempotent cleanup callback.
		if err := r.ensureDrainLeaseIdle(ctx, rollout, target, leaf); err != nil {
			return err
		}
		// DeviceClean is the durable acquisition fence. Re-read WorkerDrain in
		// the status CAS so a publication racing the Lease check cannot attach
		// stale clean evidence to this transition.
		return r.patchDrainPodDeviceClean(ctx, leaf, frozen.UID, now)
	case opsv1alpha1.UpgradeDrainPodDeviceClean:
		currentLeaf, currentPod, ready, err := r.revalidateDrainPodFinalProof(ctx, rollout, target, leaf, frozen.UID)
		if err != nil || !ready {
			return err
		}
		if found {
			if err := r.releaseDrainPodProtection(ctx, currentPod, currentLeaf.Status.ManagerDrain.SessionToken); err != nil {
				return err
			}
		}
		return r.patchDrainPodPhase(ctx, currentLeaf, currentPod.UID,
			opsv1alpha1.UpgradeDrainPodDeviceClean, opsv1alpha1.UpgradeDrainPodReleased, now, currentPod.DeviceCleanInventoryRevision)
	case opsv1alpha1.UpgradeDrainPodReleased:
		if found && frozen.EvictionRequestedAt != nil {
			return nil
		}
		return r.patchDrainPodPhase(ctx, leaf, frozen.UID,
			opsv1alpha1.UpgradeDrainPodReleased, opsv1alpha1.UpgradeDrainPodComplete, now, frozen.DeviceCleanInventoryRevision)
	case opsv1alpha1.UpgradeDrainPodComplete:
		return r.ensureCompletedDrainPodUnprotected(ctx, frozen, leaf.Status.ManagerDrain.SessionToken)
	default:
		return fmt.Errorf("unsupported drain Pod phase %q", frozen.Phase)
	}
}

// authorizeDrainEviction is the final fail-closed authorization point before
// dispatching policy/v1 Eviction. It deliberately uses the uncached APIReader:
// a stale reconcile may retain an Evicting snapshot after another leader has
// committed pause, cancellation, deletion, or Recovering. Pod resourceVersion
// equality binds the fresh authorization to the eligibility check immediately
// above; the Eviction preconditions make a later Pod write fail atomically.
func (r *IOSXESoftwareRolloutReconciler) authorizeDrainEviction(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	observedPod *corev1.Pod,
) (corev1.Pod, error) {
	blocked := func(format string, args ...any) (corev1.Pod, error) {
		return corev1.Pod{}, fmt.Errorf("%w: %s", errDrainSafetyBlocked, fmt.Sprintf(format, args...))
	}
	if r.APIReader == nil {
		return blocked("uncached API reader is unavailable before Eviction")
	}
	if rollout == nil || rollout.Namespace == "" || rollout.Name == "" || rollout.UID == "" ||
		leaf == nil || leaf.Namespace == "" || leaf.Name == "" || leaf.UID == "" ||
		leaf.Status.ManagerDrain == nil || frozen == nil || observedPod == nil {
		return blocked("Eviction authority identity is incomplete")
	}
	expectedDrain := leaf.Status.ManagerDrain
	if expectedDrain.State != opsv1alpha1.UpgradeManagerDrainEvicting ||
		frozen.Phase != opsv1alpha1.UpgradeDrainPodProtected ||
		observedPod.Namespace != frozen.Namespace || observedPod.Name != frozen.Name ||
		string(observedPod.UID) != frozen.UID {
		return blocked("stale reconcile does not carry Evicting/Protected authority")
	}

	// Read the Pod first. Any later Pod write is rejected by the exact
	// resourceVersion precondition on Eviction, whereas ManagerDrain has no
	// cross-resource precondition and is therefore re-read last.
	var currentPod corev1.Pod
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(observedPod), &currentPod); err != nil {
		return blocked("re-read protected Pod before Eviction: %v", err)
	}
	if currentPod.UID != observedPod.UID || string(currentPod.UID) != frozen.UID ||
		currentPod.ResourceVersion == "" || currentPod.ResourceVersion != observedPod.ResourceVersion ||
		currentPod.DeletionTimestamp != nil {
		return blocked("protected Pod incarnation, resourceVersion, or termination state changed before Eviction")
	}
	owned, err := exactDrainPodProtection(&currentPod, expectedDrain.SessionToken)
	if err != nil {
		return blocked("protected Pod no longer has exact session ownership: %v", err)
	}
	if !owned {
		return blocked("protected Pod no longer has its exact session marker and finalizer")
	}

	var currentRollout opsv1alpha1.IOSXESoftwareRollout
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(rollout), &currentRollout); err != nil {
		return blocked("re-read rollout before Eviction: %v", err)
	}
	if currentRollout.UID != rollout.UID || !currentRollout.DeletionTimestamp.IsZero() ||
		currentRollout.Spec.Control.Revision != rollout.Spec.Control.Revision ||
		currentRollout.Spec.Control.Revision != expectedDrain.ControlRevision ||
		currentRollout.Spec.Control.Pause || currentRollout.Spec.Control.Cancel {
		return blocked("rollout incarnation or control no longer authorizes Eviction")
	}
	if currentRollout.Status.PolicyTransition != nil {
		return blocked("rollout policy transition is fencing the previous policy epoch")
	}
	switch currentRollout.Status.Phase {
	case opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting, opsv1alpha1.IOSXESoftwareRolloutPhaseSoaking:
	default:
		return blocked("rollout phase %q does not authorize Eviction", currentRollout.Status.Phase)
	}
	if err := r.revalidatePolicyIdentity(ctx, &currentRollout); err != nil {
		return blocked("administrator policy no longer authorizes workload drain: %v", err)
	}
	readyWorkerRevision, err := r.requireWorkerRevisionAcknowledgement(
		ctx, &currentRollout, target, leaf.Status.WorkerControl,
	)
	if err != nil {
		return blocked("managed worker revision no longer authorizes Eviction: %v", err)
	}

	// This is intentionally the last cross-object GET before dispatch. A
	// Recovering transition committed while policy or worker evidence was being
	// refreshed must win over this stale reconcile.
	var currentLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		return blocked("re-read rollout leaf before Eviction: %v", err)
	}
	if currentLeaf.UID != leaf.UID || !currentLeaf.DeletionTimestamp.IsZero() {
		return blocked("rollout leaf incarnation changed or is deleting")
	}
	if err := validateManagedLeafBinding(&currentRollout, target, &currentLeaf); err != nil {
		return blocked("rollout leaf binding changed: %v", err)
	}
	if err := validateManagerDrainBinding(&currentRollout, target, &currentLeaf); err != nil {
		return blocked("manager drain binding changed: %v", err)
	}
	currentDrain := currentLeaf.Status.ManagerDrain
	if currentDrain.ProtocolVersion != expectedDrain.ProtocolVersion ||
		currentDrain.State != opsv1alpha1.UpgradeManagerDrainEvicting ||
		currentDrain.SessionToken != expectedDrain.SessionToken ||
		currentDrain.ControlRevision != expectedDrain.ControlRevision ||
		currentDrain.PolicyEpoch != expectedDrain.PolicyEpoch ||
		currentDrain.ReservationID != expectedDrain.ReservationID ||
		currentDrain.NodeUID != expectedDrain.NodeUID ||
		currentDrain.NodeUnschedulableBefore != expectedDrain.NodeUnschedulableBefore ||
		currentDrain.MaintenanceTaintPresentBefore != expectedDrain.MaintenanceTaintPresentBefore ||
		!currentDrain.StartedAt.Equal(&expectedDrain.StartedAt) ||
		!currentDrain.DrainDeadline.Equal(&expectedDrain.DrainDeadline) ||
		!r.now().Before(currentDrain.DrainDeadline.Time) {
		return blocked("current drain session, revision, policy, reservation, or deadline no longer authorizes Eviction")
	}
	admission, managerControl, workerControl := currentLeaf.Status.ManagerAdmission,
		currentLeaf.Status.ManagerControl, currentLeaf.Status.WorkerControl
	if admission == nil || admission.State != opsv1alpha1.UpgradeManagerAdmissionPending ||
		admission.ControlRevision == nil || *admission.ControlRevision != currentDrain.ControlRevision ||
		admission.PolicyEpoch != currentDrain.PolicyEpoch || admission.RevocationReason != "" ||
		managerControl == nil || managerControl.Revision != currentDrain.ControlRevision ||
		managerControl.Pause || managerControl.Cancel || workerControl == nil ||
		workerControl.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		workerControl.ObservedPolicyEpoch != currentDrain.PolicyEpoch ||
		workerControl.ObservedControlRevision != currentDrain.ControlRevision ||
		workerControl.ObservedWorkerConfigRevision != readyWorkerRevision ||
		workerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		return blocked("current admission or control acknowledgement no longer authorizes Eviction")
	}
	var currentPodState *opsv1alpha1.UpgradeDrainPodStatus
	for i := range currentDrain.Pods {
		candidate := &currentDrain.Pods[i]
		if candidate.UID != frozen.UID {
			continue
		}
		if currentPodState != nil {
			return blocked("current drain duplicates protected Pod UID %q", frozen.UID)
		}
		currentPodState = candidate
	}
	if currentPodState == nil || currentPodState.Namespace != frozen.Namespace ||
		currentPodState.Name != frozen.Name || currentPodState.Phase != opsv1alpha1.UpgradeDrainPodProtected ||
		currentPodState.EligibilityHash != frozen.EligibilityHash {
		return blocked("current drain no longer carries the exact Protected Pod authority")
	}
	return currentPod, nil
}

func drainWorkerProvesPodClean(
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	pod *opsv1alpha1.UpgradeDrainPodStatus,
) (int64, bool) {
	drain, worker := leaf.Status.ManagerDrain, leaf.Status.WorkerDrain
	if drain == nil || worker == nil || worker.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		worker.ObservedSessionToken != drain.SessionToken || worker.ObservedPolicyEpoch != drain.PolicyEpoch ||
		worker.ObservedControlRevision > drain.ControlRevision || worker.InventoryRevision < 1 ||
		worker.InventoryRevision <= pod.DeletionObservedInventoryRevision ||
		!worker.InventoryComplete || worker.UnknownDeviceWorkloadCount != 0 ||
		(worker.ForeignDeviceWorkloadCount != 0 && drain.State != opsv1alpha1.UpgradeManagerDrainRecovering) ||
		worker.InventoryObservedAt.IsZero() {
		return 0, false
	}
	if leaf.Status.WorkerControl == nil || worker.ObservedWorkerConfigRevision != leaf.Status.WorkerControl.ObservedWorkerConfigRevision {
		return 0, false
	}
	// Publication is causally gated by the worker on this exact durable
	// TerminationObserved record. Do not compare manager/worker wall clocks:
	// skew between them must not strand otherwise exact inventory evidence.
	if pod.DeletionObservedAt == nil || pod.DeletionObservedAt.IsZero() {
		return 0, false
	}
	for _, uid := range worker.RemainingAuthorizedPodUIDs {
		if uid == pod.UID {
			return 0, false
		}
	}
	return worker.InventoryRevision, true
}

func (r *IOSXESoftwareRolloutReconciler) releaseDrainPodProtection(
	ctx context.Context,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	sessionToken string,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var pod corev1.Pod
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}, &pod); err != nil {
			return client.IgnoreNotFound(err)
		}
		if string(pod.UID) != frozen.UID {
			return fmt.Errorf("refuse to release protection from a replacement Pod")
		}
		owned, err := exactDrainPodProtection(&pod, sessionToken)
		if err != nil {
			return err
		}
		if !owned {
			return nil
		}
		before := pod.DeepCopy()
		controllerutil.RemoveFinalizer(&pod, managedprotocol.DrainPodFinalizer)
		delete(pod.Annotations, managedprotocol.AnnotationDrainSession)
		return r.Patch(ctx, &pod, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}

// releaseUnacceptedDrainPodProtection removes protection only while the exact
// Pod is still non-terminating. A concurrent Eviction changes resourceVersion;
// the retry then observes deletionTimestamp and reports accepted termination
// without removing the finalizer. Recovery callers may accept NotFound or an
// entirely unprotected replacement after another recovery attempt already
// removed protection; partial, foreign, or terminating exact ownership remains
// quarantined.
func (r *IOSXESoftwareRolloutReconciler) releaseUnacceptedDrainPodProtection(
	ctx context.Context,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	sessionToken string,
	allowAlreadyReleased bool,
) (bool, error) {
	terminationAccepted := false
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var pod corev1.Pod
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}, &pod); err != nil {
			if allowAlreadyReleased && apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if string(pod.UID) != frozen.UID {
			if allowAlreadyReleased {
				owned, err := exactDrainPodProtection(&pod, sessionToken)
				if err != nil || owned {
					return fmt.Errorf("%w: replacement Pod has partial or foreign drain protection", errDrainSafetyBlocked)
				}
				return nil
			}
			return fmt.Errorf("refuse to release protection from a replacement Pod")
		}
		owned, err := exactDrainPodProtection(&pod, sessionToken)
		if err != nil {
			return err
		}
		if pod.DeletionTimestamp != nil {
			if !owned {
				return fmt.Errorf("terminating protected Pod lost exact drain ownership")
			}
			terminationAccepted = true
			return nil
		}
		if !owned {
			return nil
		}
		before := pod.DeepCopy()
		controllerutil.RemoveFinalizer(&pod, managedprotocol.DrainPodFinalizer)
		delete(pod.Annotations, managedprotocol.AnnotationDrainSession)
		return r.Patch(ctx, &pod, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
	return terminationAccepted, err
}

// ensureCompletedDrainPodUnprotected makes Complete a verifiable state rather
// than a blind status shortcut. It closes the crash boundary in which an old
// Preparing leader commits exact Pod protection after recovery selected the Pod
// for no-op completion, but before that leader discovers its stale phase CAS.
func (r *IOSXESoftwareRolloutReconciler) ensureCompletedDrainPodUnprotected(
	ctx context.Context,
	frozen *opsv1alpha1.UpgradeDrainPodStatus,
	sessionToken string,
) error {
	var pod corev1.Pod
	err := r.reader().Get(ctx, types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}, &pod)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	owned, protectionErr := exactDrainPodProtection(&pod, sessionToken)
	if string(pod.UID) != frozen.UID {
		if protectionErr != nil || owned {
			return fmt.Errorf("replacement Pod has partial or foreign drain protection")
		}
		return nil
	}
	if protectionErr != nil {
		return protectionErr
	}
	if !owned {
		return nil
	}
	if pod.DeletionTimestamp != nil {
		return fmt.Errorf("completed Pod began terminating with exact protection; strict device-clean evidence is required")
	}
	terminationAccepted, err := r.releaseUnacceptedDrainPodProtection(ctx, frozen, sessionToken, true)
	if err != nil {
		return err
	}
	if terminationAccepted {
		return fmt.Errorf("completed Pod began terminating while stale protection was being released; strict device-clean evidence is required")
	}
	return nil
}

// reconcileTerminalDrainProtection keeps terminal rollout audit state
// immutable while still repairing a Pod-side write committed by a stale
// Preparing leader after the normal recovery cleanup. Only an exact Settled
// session and Complete Pod record may remove its own non-terminating marker.
func (r *IOSXESoftwareRolloutReconciler) reconcileTerminalDrainProtection(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
) error {
	if rollout == nil || rollout.Status.FrozenPlan == nil {
		return nil
	}
	for i := range rollout.Status.FrozenPlan.Targets {
		target := rollout.Status.FrozenPlan.Targets[i]
		if target.ChildName == "" {
			return fmt.Errorf("%w: terminal drain target has no retained leaf name", errDrainSafetyBlocked)
		}
		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}, &leaf)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read terminal drain leaf %s/%s: %w", rollout.Namespace, target.ChildName, err)
		}
		if leaf.Status.ManagerDrain == nil {
			continue
		}
		if err := validateManagedLeafBinding(rollout, target, &leaf); err != nil {
			return fmt.Errorf("%w: terminal drain leaf binding changed: %v", errDrainSafetyBlocked, err)
		}
		if err := validateManagerDrainBinding(rollout, target, &leaf); err != nil {
			return fmt.Errorf("%w: terminal manager drain binding changed: %v", errDrainSafetyBlocked, err)
		}
		drain := leaf.Status.ManagerDrain
		if drain.State != opsv1alpha1.UpgradeManagerDrainSettled {
			return fmt.Errorf("%w: terminal rollout retains drain state %q", errDrainSafetyBlocked, drain.State)
		}
		for j := range drain.Pods {
			pod := &drain.Pods[j]
			if pod.Phase != opsv1alpha1.UpgradeDrainPodComplete {
				return fmt.Errorf("%w: settled drain Pod %s/%s is in phase %q",
					errDrainSafetyBlocked, pod.Namespace, pod.Name, pod.Phase)
			}
			if err := r.ensureCompletedDrainPodUnprotected(ctx, pod, drain.SessionToken); err != nil {
				return fmt.Errorf("repair terminal drain Pod %s/%s protection: %w", pod.Namespace, pod.Name, err)
			}
		}
	}
	return nil
}

func exactDrainPodProtection(pod *corev1.Pod, sessionToken string) (bool, error) {
	marker := pod.Annotations[managedprotocol.AnnotationDrainSession]
	hasFinalizer := controllerutil.ContainsFinalizer(pod, managedprotocol.DrainPodFinalizer)
	if marker == "" && !hasFinalizer {
		return false, nil
	}
	if marker == sessionToken && hasFinalizer {
		return true, nil
	}
	return false, fmt.Errorf("Pod has partial or foreign drain protection")
}

func (r *IOSXESoftwareRolloutReconciler) patchDrainPodPhase(
	ctx context.Context,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	uid string,
	expected opsv1alpha1.UpgradeDrainPodPhase,
	phase opsv1alpha1.UpgradeDrainPodPhase,
	now time.Time,
	inventoryRevision int64,
) error {
	return r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID || current.Status.ManagerDrain == nil ||
			current.Status.ManagerDrain.SessionToken != leaf.Status.ManagerDrain.SessionToken {
			return fmt.Errorf("drain session changed while recording Pod progress")
		}
		for i := range current.Status.ManagerDrain.Pods {
			pod := &current.Status.ManagerDrain.Pods[i]
			if pod.UID != uid {
				continue
			}
			if pod.Phase == phase {
				return nil
			}
			if pod.Phase != expected {
				return fmt.Errorf("drain Pod %q phase changed from expected %q to %q before advancing to %q",
					uid, expected, pod.Phase, phase)
			}
			pod.Phase = phase
			timestamp := metav1.NewTime(now)
			switch phase {
			case opsv1alpha1.UpgradeDrainPodProtected:
				pod.ProtectedAt = &timestamp
			case opsv1alpha1.UpgradeDrainPodEvictionRequested:
				pod.EvictionRequestedAt = &timestamp
			case opsv1alpha1.UpgradeDrainPodTerminationObserved:
				pod.DeletionObservedAt = &timestamp
				if current.Status.WorkerDrain != nil && current.Status.WorkerDrain.InventoryRevision > 0 {
					pod.DeletionObservedInventoryRevision = current.Status.WorkerDrain.InventoryRevision
				}
			case opsv1alpha1.UpgradeDrainPodDeviceClean:
				pod.DeviceCleanAt = &timestamp
				pod.DeviceCleanInventoryRevision = inventoryRevision
			case opsv1alpha1.UpgradeDrainPodReleased:
				pod.ReleasedAt = &timestamp
			}
			current.Status.ManagerDrain.UpdatedAt = timestamp
			return nil
		}
		return fmt.Errorf("selected drain Pod UID %q is absent", uid)
	})
}

func (r *IOSXESoftwareRolloutReconciler) patchManagerDrainState(
	ctx context.Context,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	expected opsv1alpha1.UpgradeManagerDrainState,
	state opsv1alpha1.UpgradeManagerDrainState,
	now time.Time,
) error {
	return r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID || current.Status.ManagerDrain == nil ||
			current.Status.ManagerDrain.SessionToken != leaf.Status.ManagerDrain.SessionToken {
			return fmt.Errorf("drain session changed while advancing manager state")
		}
		if current.Status.ManagerDrain.State == state {
			return nil
		}
		if current.Status.ManagerDrain.State != expected {
			return fmt.Errorf("manager drain state changed from expected %q to %q before advancing to %q",
				expected, current.Status.ManagerDrain.State, state)
		}
		current.Status.ManagerDrain.State = state
		current.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now)
		return nil
	})
}

func (r *IOSXESoftwareRolloutReconciler) enterDrainRecovery(
	ctx context.Context,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	controlRevision int64,
	now time.Time,
) error {
	return r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID || current.Status.ManagerDrain == nil ||
			current.Status.ManagerDrain.SessionToken != leaf.Status.ManagerDrain.SessionToken {
			return fmt.Errorf("drain session changed while entering recovery")
		}
		if current.Status.ManagerDrain.State == opsv1alpha1.UpgradeManagerDrainSettled {
			return nil
		}
		markManagerDrainRecovering(current.Status.ManagerDrain, controlRevision, now)
		return nil
	})
}

func markManagerDrainRecovering(drain *opsv1alpha1.UpgradeManagerDrainStatus, controlRevision int64, now time.Time) {
	if drain == nil || drain.State == opsv1alpha1.UpgradeManagerDrainSettled {
		return
	}
	previousState := drain.State
	previousRevision := drain.ControlRevision
	changed := previousState != opsv1alpha1.UpgradeManagerDrainRecovering
	drain.State = opsv1alpha1.UpgradeManagerDrainRecovering
	if controlRevision > drain.ControlRevision {
		drain.ControlRevision = controlRevision
		changed = true
	}
	if drain.RecoveryDeadline == nil {
		deadline := metav1.NewTime(now.Add(safeManagerDrainRecoveryWindow(drain)))
		drain.RecoveryDeadline = &deadline
		changed = true
	} else if previousState == opsv1alpha1.UpgradeManagerDrainRecovering &&
		controlRevision > previousRevision {
		// A newer campaign control revision is the explicit, auditable recovery
		// renewal. It extends teardown/inventory authority by at most one bounded
		// window; Recovering remains terminal for new Evictions and mutations.
		deadline := metav1.NewTime(now.Add(safeManagerDrainRecoveryWindow(drain)))
		if deadline.After(drain.RecoveryDeadline.Time) {
			drain.RecoveryDeadline = &deadline
			changed = true
		}
	}
	if changed {
		drain.UpdatedAt = metav1.NewTime(now)
	}
}

func managerDrainRecoveryWindow(drain *opsv1alpha1.UpgradeManagerDrainStatus) (time.Duration, error) {
	if drain == nil || drain.StartedAt.IsZero() || drain.DrainDeadline.IsZero() {
		return 0, fmt.Errorf("manager drain recovery window has incomplete timing evidence")
	}
	duration := drain.DrainDeadline.Sub(drain.StartedAt.Time)
	if duration <= 0 || duration > drainMaximumDuration {
		return 0, fmt.Errorf("manager drain duration is outside the bounded recovery contract")
	}
	return duration + drainRecoveryExtraLimit, nil
}

func safeManagerDrainRecoveryWindow(drain *opsv1alpha1.UpgradeManagerDrainStatus) time.Duration {
	window, err := managerDrainRecoveryWindow(drain)
	if err == nil {
		return window
	}
	// Construction is already guarded by the CRD and runtime binding checks.
	// Keep this mutation helper bounded even if it is called with corrupt state.
	return 5*time.Minute + drainRecoveryExtraLimit
}

// reconcileDrainRecoveryCleanup never starts an Eviction. It exists so a
// cancellation or policy/source fence can still finish teardown already
// accepted by Kubernetes and remove protection from Pods never evicted.
func (r *IOSXESoftwareRolloutReconciler) reconcileDrainRecoveryCleanup(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) error {
	if leaf == nil || leaf.Status.ManagerDrain == nil ||
		leaf.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainRecovering {
		return nil
	}
	for i := range leaf.Status.ManagerDrain.Pods {
		pod := &leaf.Status.ManagerDrain.Pods[i]
		if pod.Phase == opsv1alpha1.UpgradeDrainPodComplete {
			if err := r.ensureCompletedDrainPodUnprotected(ctx, pod, leaf.Status.ManagerDrain.SessionToken); err != nil {
				return err
			}
			continue
		}
		return r.reconcileOneDrainPod(ctx, rollout, target, leaf, pod, now, true)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) drainControllerReady(
	ctx context.Context,
	pod *opsv1alpha1.UpgradeDrainPodStatus,
) (bool, error) {
	ref := pod.Controller
	if pod.WorkloadController != nil {
		ref = *pod.WorkloadController
	}
	switch ref.Kind {
	case "Deployment":
		var object appsv1.Deployment
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &object); err != nil {
			return false, err
		}
		if string(object.UID) != ref.UID || !object.DeletionTimestamp.IsZero() {
			return false, nil
		}
		if err := validateRecoveredDrainTemplate("Deployment", object.Spec.Selector, &object.Spec.Template); err != nil {
			return false, err
		}
		desired := int32(1)
		if object.Spec.Replicas != nil {
			desired = *object.Spec.Replicas
		}
		// Do not let old available replicas mask unavailable replacements during
		// a rolling-update surge. Once total replicas is back at the desired
		// count, UpdatedReplicas proves every remaining replica is from the
		// current template; the readiness counters then apply to that same set.
		return object.Status.ObservedGeneration >= object.Generation && object.Status.Replicas == desired &&
			object.Status.UpdatedReplicas >= desired && object.Status.ReadyReplicas >= desired &&
			object.Status.AvailableReplicas >= desired && object.Status.UnavailableReplicas == 0, nil
	case "ReplicaSet":
		var object appsv1.ReplicaSet
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &object); err != nil {
			return false, err
		}
		if string(object.UID) != ref.UID || !object.DeletionTimestamp.IsZero() {
			return false, nil
		}
		if err := validateRecoveredDrainTemplate("ReplicaSet", object.Spec.Selector, &object.Spec.Template); err != nil {
			return false, err
		}
		desired := int32(1)
		if object.Spec.Replicas != nil {
			desired = *object.Spec.Replicas
		}
		return object.Status.ObservedGeneration >= object.Generation && object.Status.Replicas == desired &&
			object.Status.ReadyReplicas >= desired && object.Status.AvailableReplicas >= desired, nil
	default:
		return false, fmt.Errorf("unsupported frozen workload controller %q", ref.Kind)
	}
}

func validateRecoveredDrainTemplate(
	kind string,
	selector *metav1.LabelSelector,
	template *corev1.PodTemplateSpec,
) error {
	if selector == nil || template == nil || template.Labels[drainSafeLabel] != "true" {
		return fmt.Errorf("current %s template is not explicitly drain-safe", kind)
	}
	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil || !parsed.Matches(labels.Set(template.Labels)) {
		return fmt.Errorf("current %s selector does not match its replacement template", kind)
	}
	if template.Spec.NodeName != "" {
		return fmt.Errorf("current %s template pins nodeName", kind)
	}
	if template.Spec.SchedulerName != "" && template.Spec.SchedulerName != corev1.DefaultSchedulerName {
		return fmt.Errorf("current %s template uses a non-default scheduler", kind)
	}
	if err := validateDrainPodSpec(&template.Spec); err != nil {
		return fmt.Errorf("current %s template is not portable: %w", kind, err)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) currentDrainTemplateLabels(
	ctx context.Context,
	pod *opsv1alpha1.UpgradeDrainPodStatus,
) (map[string]string, error) {
	ref := pod.Controller
	if pod.WorkloadController != nil {
		ref = *pod.WorkloadController
	}
	switch ref.Kind {
	case "Deployment":
		var object appsv1.Deployment
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &object); err != nil {
			return nil, err
		}
		if string(object.UID) != ref.UID || !object.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("frozen Deployment identity is no longer current")
		}
		if err := validateRecoveredDrainTemplate("Deployment", object.Spec.Selector, &object.Spec.Template); err != nil {
			return nil, err
		}
		return object.Spec.Template.Labels, nil
	case "ReplicaSet":
		var object appsv1.ReplicaSet
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, &object); err != nil {
			return nil, err
		}
		if string(object.UID) != ref.UID || !object.DeletionTimestamp.IsZero() {
			return nil, fmt.Errorf("frozen ReplicaSet identity is no longer current")
		}
		if err := validateRecoveredDrainTemplate("ReplicaSet", object.Spec.Selector, &object.Spec.Template); err != nil {
			return nil, err
		}
		return object.Spec.Template.Labels, nil
	default:
		return nil, fmt.Errorf("unsupported frozen workload controller %q", ref.Kind)
	}
}

func (r *IOSXESoftwareRolloutReconciler) ensureDrainNodeEmpty(
	ctx context.Context,
	nodeName string,
	allowedUIDs map[string]struct{},
) error {
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.MatchingFields{rolloutPodNodeNameIndex: nodeName}); err != nil {
		return err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != nodeName ||
			(pod.DeletionTimestamp == nil && (pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed)) {
			continue
		}
		if _, allowed := allowedUIDs[string(pod.UID)]; allowed {
			continue
		}
		return fmt.Errorf("%w: new or foreign workload %s/%s is bound to the guarded Node", errDrainSafetyBlocked, pod.Namespace, pod.Name)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) ensureDrainLeaseIdle(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &node); err != nil {
		return err
	}
	if string(node.UID) != target.NodeUID {
		return fmt.Errorf("drain Node incarnation changed while checking the mutation Lease")
	}
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return err
	}
	session := device.Status.MaintenanceSession
	if string(device.UID) != target.DeviceUID {
		return fmt.Errorf("%w: drain CiscoDevice incarnation changed", errDrainSafetyBlocked)
	}
	if err := validateDrainSessionIdentity(session, target, leaf, leaf.Status.ManagerDrain); err != nil {
		return fmt.Errorf("%w: drain maintenance session identity is invalid: %v", errDrainSafetyBlocked, err)
	}
	return r.ensureMaintenanceLeaseIdle(ctx, &device, &node, session.Lease)
}

func (r *IOSXESoftwareRolloutReconciler) ensureMaintenanceLeaseIdle(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	leaseRef ciskov1.DeviceMaintenanceLeaseReference,
) error {
	if device == nil || node == nil || string(device.UID) == "" || string(node.UID) == "" ||
		leaseRef.Namespace == "" || leaseRef.Name == "" || leaseRef.UID == "" {
		return fmt.Errorf("%w: maintenance Lease identity is incomplete", errDrainSafetyBlocked)
	}
	var lease coordv1.Lease
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: leaseRef.Namespace, Name: leaseRef.Name}, &lease); err != nil {
		return err
	}
	if string(lease.UID) != leaseRef.UID {
		return fmt.Errorf("drain mutation Lease incarnation changed")
	}
	expectedAnnotations, expectedLabels := managedMutationLeaseMetadata(
		device, node.Name, string(node.UID), node.Annotations[managedprotocol.AnnotationWorkerUsername],
	)
	if err := validateManagedMutationLeaseMetadata(&lease, expectedAnnotations, expectedLabels); err != nil {
		return fmt.Errorf("%w: mutation Lease binding is no longer canonical: %v", errDrainSafetyBlocked, err)
	}
	if err := validateManagedMutationLeaseSpec(&lease.Spec, ""); err != nil {
		return fmt.Errorf("%w: mutation Lease is not wholly idle: %v", errDrainSafetyBlocked, err)
	}
	if hasMaintenanceRequestAnnotations(lease.Annotations) {
		return fmt.Errorf("%w: mutation Lease retains maintenance request annotations", errDrainSafetyBlocked)
	}
	return nil
}

// deletionDrainSettlementSuperseded recognizes recovery evidence for a
// campaign that had already settled, released its topology acquisition, and
// was then deleted after a later campaign replaced the CiscoDevice's
// single-slot Settled maintenance acknowledgement. It grants no authority and
// never rewrites the ledger. A non-exact current session is treated as
// observed so deletion does not replay an obsolete release fence while the
// successor is active or malformed.
func (r *IOSXESoftwareRolloutReconciler) deletionDrainSettlementSuperseded(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) (bool, bool, string, error) {
	if err := validateManagerDrainBinding(rollout, target, leaf); err != nil {
		return false, false, "", err
	}
	drain := leaf.Status.ManagerDrain
	admission := leaf.Status.ManagerAdmission
	if drain.State != opsv1alpha1.UpgradeManagerDrainSettled ||
		admission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		return false, false, "", nil
	}

	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return false, false, "", err
	}
	blocked := func(message string) (bool, bool, string, error) {
		return false, true, message, nil
	}
	if string(device.UID) != target.DeviceUID {
		return blocked("device incarnation changed before successor settlement could be proved")
	}
	session := device.Status.MaintenanceSession
	if session == nil {
		return blocked("the maintenance session disappeared before successor settlement could be proved")
	}
	if validateDrainSessionIdentity(session, target, leaf, drain) == nil {
		return false, false, "", nil
	}
	session = session.DeepCopy()
	// A partial rewrite or identity reuse is not successor evidence. Block it
	// without replaying the old release fence into the successor's ledger state.
	if session.SessionToken == drain.SessionToken || session.Operation.UID == string(leaf.UID) {
		return blocked("the maintenance session partially reuses the old drain identity")
	}
	if err := validateSettledSuccessorSession(session, rollout.Namespace, target); err != nil {
		return blocked(err.Error())
	}

	if err := validateDrainPodsComplete(drain); err != nil {
		return false, true, "", err
	}
	for i := range drain.Pods {
		if err := r.ensureCompletedDrainPodUnprotected(ctx, &drain.Pods[i], drain.SessionToken); err != nil {
			return blocked("old drain Pod protection is not fully released")
		}
	}
	_, ledger, err := r.ledgerStore(rollout).Read(ctx)
	if err != nil {
		return false, true, "", err
	}
	if _, exists := ledger.Reservations[drain.ReservationID]; exists {
		return blocked("old drain reservation is still present")
	}

	// Re-read every mutable authority after the Pod and ledger proofs. A device
	// delete/recreate or a newly published maintenance session must win this
	// race and keep the old campaign finalizer fail-closed.
	var freshDevice ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &freshDevice); err != nil {
		return false, true, "", err
	}
	var freshNode corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &freshNode); err != nil {
		return false, true, "", err
	}
	if string(freshDevice.UID) != target.DeviceUID || string(freshNode.UID) != target.NodeUID {
		return blocked("device or Node incarnation changed while proving successor settlement")
	}
	if !reflect.DeepEqual(freshDevice.Status.MaintenanceSession, session) {
		return blocked("newer settled maintenance session changed while proving successor settlement")
	}
	if err := validateDrainSchedulingRestored(&freshDevice, &freshNode, target, leaf); err != nil {
		return blocked(err.Error())
	}
	if freshDevice.Status.TopologyLock != nil &&
		(freshDevice.Status.TopologyLock.CampaignUID == string(rollout.UID) ||
			freshDevice.Status.TopologyLock.ReservationID == drain.ReservationID ||
			freshDevice.Status.TopologyLock.AcquisitionID == admission.TopologyLockID) {
		return blocked("old drain topology lock is still present")
	}
	if err := r.ensureMaintenanceLeaseIdle(ctx, &freshDevice, &freshNode,
		freshDevice.Status.MaintenanceSession.Lease); err != nil {
		return blocked(err.Error())
	}
	// Read the successor operation only after all replaceable device, Node,
	// Lease, Pod, and ledger evidence. Its UID and terminal settled status are
	// immutable, so this final binding cannot be satisfied by a same-name
	// delete/recreate observed earlier in the proof.
	var successor opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.reader().Get(ctx, types.NamespacedName{
		Namespace: session.Operation.Namespace, Name: session.Operation.Name,
	}, &successor); err != nil {
		return blocked("newer settled maintenance operation is unavailable")
	}
	if err := validateSettledSuccessorBinding(session, target, &successor); err != nil {
		return blocked(err.Error())
	}
	return true, true, "newer settled maintenance session proves the old drain acknowledgement was consumed", nil
}

func validateSettledSuccessorSession(
	session *ciskov1.DeviceMaintenanceSessionStatus,
	namespace string,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) error {
	if session == nil || session.Phase != ciskov1.DeviceMaintenanceSessionSettled {
		return fmt.Errorf("a newer maintenance session is not yet settled")
	}
	if session.DeviceUID != target.DeviceUID || session.NodeName != target.NodeName ||
		session.NodeUID != target.NodeUID || session.Operation.Namespace != namespace ||
		session.Operation.Name == "" || session.Operation.UID == "" || session.SessionToken == "" ||
		session.Lease.Namespace == "" || session.Lease.Name == "" || session.Lease.UID == "" ||
		session.ControlRevision < 0 || session.RequestedAt.IsZero() || session.AcknowledgedAt == nil ||
		session.AcknowledgedAt.IsZero() || session.AcknowledgedAt.Before(&session.RequestedAt) {
		return fmt.Errorf("newer settled maintenance-session identity is incomplete or belongs to another device or Node incarnation")
	}
	expectedHolder := "software-upgrade/" + session.Operation.UID
	switch session.ProtocolVersion {
	case "":
		if session.Purpose != "" || session.Lease.Holder != expectedHolder {
			return fmt.Errorf("newer settled legacy maintenance-session protocol or holder is invalid")
		}
	case ciskov1.DeviceMaintenanceProtocolRolloutV1:
		if session.Purpose != ciskov1.DeviceMaintenancePurposeSoftwareMutation ||
			session.Lease.Holder != expectedHolder {
			return fmt.Errorf("newer settled rollout maintenance-session purpose or holder is invalid")
		}
	case ciskov1.DeviceMaintenanceProtocolPDBDrainV1:
		parsed, err := uuid.Parse(session.SessionToken)
		if err != nil || parsed.String() != session.SessionToken || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
			return fmt.Errorf("newer settled drain maintenance-session token is invalid")
		}
		switch session.Purpose {
		case ciskov1.DeviceMaintenancePurposeWorkloadDrain:
			expectedHolder = "software-drain/" + session.Operation.UID
		case ciskov1.DeviceMaintenancePurposeSoftwareMutation:
		default:
			return fmt.Errorf("newer settled drain maintenance-session purpose is invalid")
		}
		if session.Lease.Holder != expectedHolder {
			return fmt.Errorf("newer settled drain maintenance-session holder is invalid")
		}
	default:
		return fmt.Errorf("newer settled maintenance-session protocol is unsupported")
	}
	return nil
}

func validateSettledSuccessorBinding(
	session *ciskov1.DeviceMaintenanceSessionStatus,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	successor *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if session == nil || successor == nil || string(successor.UID) != session.Operation.UID ||
		successor.Namespace != session.Operation.Namespace || successor.Name != session.Operation.Name ||
		successor.Spec.DeviceRef.Name != target.DeviceName || !terminalManagedLeaf(successor.Status.Phase) {
		return fmt.Errorf("newer settled maintenance operation identity or terminal outcome is invalid")
	}
	admission := successor.Status.ManagerAdmission
	// A non-drain operation may receive a later cancellation/control fence
	// before its original maintenance session settles, so its retained
	// admission revision is monotonic rather than identical. The PDB protocol
	// binds all three revisions exactly below.
	if admission == nil || admission.ProtocolVersion != opsv1alpha1.ManagedUpgradeProtocolRolloutV1 ||
		admission.State != opsv1alpha1.UpgradeManagerAdmissionSettled ||
		admission.LeafUID != string(successor.UID) || admission.DeviceUID != target.DeviceUID ||
		admission.NodeUID != target.NodeUID || session.Operation.UID != admission.LeafUID ||
		session.DeviceUID != admission.DeviceUID || session.NodeUID != admission.NodeUID ||
		admission.ControlRevision == nil || *admission.ControlRevision < session.ControlRevision {
		return fmt.Errorf("newer settled maintenance operation does not retain an exact settled admission binding")
	}
	if session.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 {
		if successor.Status.ManagerDrain != nil {
			return fmt.Errorf("newer non-drain maintenance session unexpectedly retains manager drain state")
		}
		return nil
	}
	drain := successor.Status.ManagerDrain
	if drain == nil || drain.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		drain.State != opsv1alpha1.UpgradeManagerDrainSettled || drain.SessionToken != session.SessionToken ||
		drain.ControlRevision != session.ControlRevision || !drain.StartedAt.Equal(&session.RequestedAt) ||
		drain.ReservationID == "" || drain.ReservationID != admission.ReservationID ||
		drain.PolicyEpoch != admission.PolicyEpoch || drain.NodeUID != target.NodeUID ||
		drain.NodeUID != session.NodeUID || *admission.ControlRevision != drain.ControlRevision {
		return fmt.Errorf("newer settled drain session does not match its exact manager drain and admission binding")
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) revalidateDrainPromotion(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if err := currentPolicy.ValidateWorkloadPolicy(rollout.Spec.Plan.Workloads); err != nil {
		return err
	}
	if err := r.revalidatePolicyIdentity(ctx, rollout); err != nil {
		return err
	}
	if err := r.verifyFrozenSource(ctx, rollout); err != nil {
		return err
	}
	if err := r.revalidateFrozenTarget(ctx, rollout, currentPolicy, target); err != nil {
		return err
	}
	if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
		return err
	}
	if err := validateManagerDrainBinding(rollout, target, leaf); err != nil {
		return err
	}
	if leaf.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainPromoting ||
		leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionPending ||
		leaf.Status.ManagerControl == nil || leaf.Status.ManagerControl.Pause || leaf.Status.ManagerControl.Cancel ||
		leaf.Status.ManagerControl.Revision != rollout.Spec.Control.Revision {
		return fmt.Errorf("drain promotion handshake changed")
	}
	if err := validateDrainPodsComplete(leaf.Status.ManagerDrain); err != nil {
		return err
	}
	worker := leaf.Status.WorkerControl
	if worker == nil || worker.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		worker.ObservedPolicyEpoch != leaf.Status.ManagerAdmission.PolicyEpoch ||
		worker.ObservedControlRevision != rollout.Spec.Control.Revision ||
		worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		return fmt.Errorf("worker no longer acknowledges the exact pending drain admission")
	}
	if _, err := r.requireWorkerRevisionAcknowledgement(ctx, rollout, target, worker); err != nil {
		return err
	}
	guardReady, err := r.drainGuardReady(ctx, rollout, target, leaf)
	if err != nil {
		return err
	}
	if !guardReady {
		return fmt.Errorf("exact drain scheduling guard or maintenance session is no longer active")
	}
	if err := r.ensureDrainNodeEmpty(ctx, target.NodeName, nil); err != nil {
		return err
	}
	return r.ensureDrainLeaseIdle(ctx, rollout, target, leaf)
}

// trySettleDrainedLeaf enters recovery before releasing any scheduling guard,
// then proves exact workload recovery and uses only the session-bound ledger
// settlement operation.
func (r *IOSXESoftwareRolloutReconciler) trySettleDrainedLeaf(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	summary opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
	now time.Time,
) (bool, string, string, error) {
	drain := leaf.Status.ManagerDrain
	if drain == nil {
		return false, "DrainIdentityMissing", "manager drain status is absent", nil
	}
	if drain.State == opsv1alpha1.UpgradeManagerDrainSettled &&
		leaf.Status.ManagerAdmission != nil && leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
		if err := r.finishSettledDrain(ctx, rollout, target, leaf); err != nil {
			return false, "DrainSettlementAcknowledgement", err.Error(), nil
		}
		return true, "HealthGatePassed", "drain, mutation, and workload recovery are settled", nil
	}
	outcomeResolved := len(leaf.Status.ManagedMutationClaims) == 0 &&
		leaf.Status.ManagerAdmission != nil && leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionGranted
	if !outcomeResolved {
		outcomeResolved = leafMutationOutcomeResolved(leaf)
	}
	if !outcomeResolved {
		return false, "MutationOutcomeUnresolved", "worker has not produced conclusive mutation-outcome evidence", nil
	}
	if drain.State != opsv1alpha1.UpgradeManagerDrainRecovering {
		if err := r.enterDrainRecovery(ctx, leaf, rollout.Spec.Control.Revision, now); err != nil {
			return false, "DrainRecoveryFailed", "", err
		}
		return false, "DrainRecovering", "device outcome is resolved; restoring session-owned scheduling guards and workloads", nil
	}
	if err := r.ensureDrainLedgerRevoked(ctx, rollout, leaf); err != nil {
		return false, "DrainLedgerRevocation", err.Error(), nil
	}
	for i := range drain.Pods {
		if drain.Pods[i].Phase == opsv1alpha1.UpgradeDrainPodComplete {
			if err := r.ensureCompletedDrainPodUnprotected(ctx, &drain.Pods[i], drain.SessionToken); err != nil {
				return false, "DrainCleanupBlocked", err.Error(), nil
			}
			continue
		}
		if err := r.reconcileOneDrainPod(ctx, rollout, target, leaf, &drain.Pods[i], now, true); err != nil {
			return false, "DrainCleanupBlocked", err.Error(), nil
		}
		return false, "DrainRecovering", "reconciling accepted teardown and releasing manager-owned Pod protection", nil
	}
	if err := r.ensureDrainGuardRestored(ctx, rollout, target, leaf); err != nil {
		return false, "DrainGuardRecovery", err.Error(), nil
	}
	for i := range drain.Pods {
		ready, err := r.drainControllerReady(ctx, &drain.Pods[i])
		if err != nil || !ready {
			if err != nil {
				return false, "WorkloadRecovery", err.Error(), nil
			}
			return false, "WorkloadRecovery", "waiting for the exact native controller to restore ready replicas", nil
		}
		if err := r.validateRecoveredPDBs(ctx, &drain.Pods[i]); err != nil {
			return false, "WorkloadRecovery", err.Error(), nil
		}
	}
	operationTime := drain.StartedAt.Time
	if leaf.Status.CompletionTime != nil {
		operationTime = leaf.Status.CompletionTime.Time
	}
	effectivePolicy, policyErr := effectiveAdmissionPolicy(rollout, currentPolicy, now)
	var healthy bool
	var detail string
	var err error
	if policyErr == nil {
		healthy, detail, err = r.targetPostMutationHealthy(ctx, rollout, currentPolicy, effectivePolicy, target, operationTime)
	} else if allowFrozenDrainRecoveryHealth(rollout, currentPolicy, leaf) {
		// An incompatible administrator-policy change fences all future authority, but it
		// must not make an old, fully recovered drain acquisition immortal. This
		// fallback grants nothing: it proves only the exact frozen target identity,
		// fresh post-operation health, and restored workloads before settlement.
		healthy, detail, err = r.targetFrozenDrainRecoveryHealthy(ctx, rollout, target, operationTime, now)
	} else {
		return false, "PolicyUnavailable", "", policyErr
	}
	if err != nil || !healthy {
		if err != nil {
			detail = err.Error()
		}
		return false, "PostMutationHealthGate", detail, nil
	}
	soak := time.Duration(defaultInt32(rollout.Spec.Plan.Health.WaveSoakSeconds, 300)) * time.Second
	if target.CanaryCohort != "" {
		soak = time.Duration(defaultInt32(rollout.Spec.Plan.Health.CanarySoakSeconds, 600)) * time.Second
	}
	deadline, started := continuousHealthySoakDeadline(summary, operationTime, soak)
	if !started {
		return false, "HealthyPostMutationSoak", "target and replacement workloads are healthy; starting continuous recovery soak", nil
	}
	if now.Before(deadline) {
		return false, "HealthyPostMutationSoak", fmt.Sprintf("healthy workload recovery soak remains until %s", deadline.UTC().Format(time.RFC3339)), nil
	}
	policyEpoch := leaf.Status.ManagerAdmission.PolicyEpoch
	lockID := leaf.Status.ManagerAdmission.TopologyLockID
	if err := r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.SettleDrainedReservation(ledger, drain.ReservationID,
			rollout.Status.FrozenPlan.Policy.LedgerUID, lockID, string(leaf.UID), drain.SessionToken,
			policyEpoch, true, true, true)
	}); err != nil {
		return false, "LedgerSettlementFailed", "", err
	}
	if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if err := validateManagerDrainBinding(rollout, target, current); err != nil {
			return err
		}
		if current.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainRecovering {
			return fmt.Errorf("manager drain left recovery before settlement")
		}
		current.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainSettled
		current.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now)
		current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
		current.Status.ManagerAdmission.RevocationReason = ""
		current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
		return nil
	}); err != nil {
		return false, "LeafSettlementFailed", "", err
	}
	// Keep the topology lock until the CiscoDevice controller has observed the
	// settled leaf, published the exact Settled session, and verified guard
	// restoration. A later reconcile then releases only this acquisition.
	return false, "DrainSettlementAcknowledgement", "ledger and leaf are settled; waiting for CiscoDevice settlement acknowledgement", nil
}

func allowFrozenDrainRecoveryHealth(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) bool {
	if rollout == nil || rollout.Status.FrozenPlan == nil || currentPolicy == nil ||
		currentPolicy.PolicyUID == "" || currentPolicy.ResourceVersion == "" ||
		leaf == nil || leaf.Status.ManagerDrain == nil ||
		leaf.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainRecovering ||
		leaf.Status.ManagerAdmission == nil || leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked {
		return false
	}
	admission := leaf.Status.ManagerAdmission
	if admission.RevocationReason == "AdministratorPolicyChanged" {
		return true
	}
	if admission.RevocationReason != "PolicyEpochTransition" ||
		rollout.Status.EffectivePolicy == nil || rollout.Status.PolicyTransition == nil {
		return false
	}
	effective := rollout.Status.EffectivePolicy
	transition := rollout.Status.PolicyTransition
	if admission.PolicyEpoch != effective.Epoch || leaf.Status.ManagerDrain.PolicyEpoch != effective.Epoch ||
		transition.Epoch != effective.Epoch+1 || effective.Policy.UID == "" || effective.Policy.LedgerUID == "" ||
		transition.Policy.UID != effective.Policy.UID || transition.Policy.LedgerUID != effective.Policy.LedgerUID ||
		transition.Policy.StructuralHash != effective.Policy.StructuralHash {
		return false
	}
	currentSnapshot, err := freezePolicy(currentPolicy)
	if err != nil {
		return false
	}
	return currentSnapshot.UID == transition.Policy.UID &&
		currentSnapshot.LedgerUID == transition.Policy.LedgerUID &&
		currentSnapshot.StructuralHash == transition.Policy.StructuralHash &&
		currentSnapshot.SemanticHash == transition.Policy.SemanticHash &&
		currentSnapshot.ResourceVersion == transition.Policy.ResourceVersion
}

func (r *IOSXESoftwareRolloutReconciler) targetFrozenDrainRecoveryHealthy(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	operationCompletedAt, now time.Time,
) (bool, string, error) {
	if rollout == nil || rollout.Status.FrozenPlan == nil {
		return false, "frozen campaign identity is absent", nil
	}
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return false, "", err
	}
	if string(device.UID) != target.DeviceUID || device.Generation != target.DeviceGeneration ||
		device.Spec.Driver != ciskov1.DeviceDriverXE || string(device.Spec.Driver) != target.Driver {
		return false, "frozen CiscoDevice incarnation or driver changed during recovery", nil
	}
	identity, projection := device.Status.NodeIdentity, device.Status.TopologyProjection
	if identity == nil || projection == nil || identity.DeviceUID != target.DeviceUID ||
		identity.NodeName != target.NodeName || identity.NodeUID != target.NodeUID ||
		projection.EffectiveLabelHash != target.ProjectionHash {
		return false, "frozen Node identity or topology projection changed during recovery", nil
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &node); err != nil {
		return false, "", err
	}
	physicalID, physicalErr := stablePhysicalIdentity(&device, &node)
	if physicalErr != nil || physicalID != target.PhysicalIdentity || string(node.UID) != target.NodeUID ||
		!managedNodeMatchesDevice(&node, &device) ||
		node.Annotations[managedprotocol.AnnotationNodeUID] != target.NodeUID ||
		node.Annotations[managedprotocol.AnnotationWorkerProtocol] != managedprotocol.Version ||
		strings.TrimSpace(node.Annotations[managedprotocol.AnnotationWorkerUsername]) == "" {
		return false, "frozen physical or managed Node binding changed during recovery", nil
	}
	if device.Labels[managedprotocol.ImageFamilyLabel] != target.ImageFamily ||
		device.Labels[managedprotocol.QualificationCohortLabel] != target.QualificationCohort {
		return false, "frozen image-family or qualification identity changed during recovery", nil
	}
	for _, value := range target.Topology {
		if device.Labels[value.Key] != value.Value || node.Labels[value.Key] != value.Value {
			return false, fmt.Sprintf("frozen topology %q changed during recovery", value.Key), nil
		}
	}
	if device.Status.Phase != "Ready" || nodeReadyCondition(&node) == nil ||
		nodeReadyCondition(&node).Status != corev1.ConditionTrue ||
		!deviceConditionCurrentTrue(&device, ciskov1.CiscoDeviceConditionNodeIdentityReady) ||
		!deviceConditionCurrentTrue(&device, ciskov1.CiscoDeviceConditionTopologyReady) ||
		!deviceConditionCurrentTrue(&device, ciskov1.CiscoDeviceConditionGNOIConfigurationReady) ||
		!managedWorkerReadyForProjection(&node, projection) || hasTopologyInitializationGuard(&node) {
		return false, "frozen target or managed worker is not healthy after recovery", nil
	}
	if _, err := r.currentReadyWorkerRevision(ctx, &device); err != nil {
		return false, "managed worker revision is not ready after recovery", nil
	}
	conditionTypes := []string{
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	}
	observed, err := managedDeviceHealthObservedAt(&device, &node, now, conditionTypes...)
	if err != nil {
		return false, "", err
	}
	freshness := minPositive(
		int(rollout.Status.FrozenPlan.Policy.HealthFreshnessSeconds),
		int(defaultInt32(rollout.Spec.Plan.Health.MaxObservationAgeSeconds, 300)),
	)
	if freshness == 0 || observed.Before(now.Add(-time.Duration(freshness)*time.Second)) {
		return false, "frozen target health evidence is stale after recovery", nil
	}
	if !isPostOperationObservation(observed, operationCompletedAt) {
		return false, "waiting for a post-operation frozen target health observation", nil
	}
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(rollout.Namespace)); err != nil {
		return false, "", err
	}
	for i := range devices.Items {
		other := &devices.Items[i]
		if other.UID == device.UID {
			continue
		}
		declared, declaredErr := topology.CanonicalPhysicalIdentity(other.Spec.PhysicalIdentity)
		observedIdentity := ""
		if other.Status.NodeIdentity != nil {
			observedIdentity = other.Status.NodeIdentity.PhysicalIdentity
		}
		if (declaredErr == nil && declared == target.PhysicalIdentity) || observedIdentity == target.PhysicalIdentity {
			return false, "frozen physical identity is now enrolled by another CiscoDevice", nil
		}
	}
	return true, "frozen target is healthy after drain recovery", nil
}

// ensureDrainLedgerRevoked fences a drain reservation without laundering its
// session provenance through the generic revoke path. A Bound reservation is
// the one legitimate pre-BeginDrain abort boundary; exact drain settlement
// handles it after recovery evidence is complete.
func (r *IOSXESoftwareRolloutReconciler) ensureDrainLedgerRevoked(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	drain := leaf.Status.ManagerDrain
	admission := leaf.Status.ManagerAdmission
	if drain == nil || admission == nil || drain.ControlRevision < 0 {
		return fmt.Errorf("drain ledger revocation identity is incomplete")
	}
	return r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		reservation, exists := ledger.Reservations[drain.ReservationID]
		if !exists {
			// The later SettleDrainedReservation replay validates the exact
			// session-bound release fence before any topology lock is released.
			return nil
		}
		if reservation.PolicyEpoch != drain.PolicyEpoch || reservation.TopologyLockID != admission.TopologyLockID ||
			reservation.ChildUID != string(leaf.UID) {
			return fmt.Errorf("drain reservation identity changed before recovery")
		}
		if reservation.State == topologyrollout.ReservationBound && reservation.DrainSessionToken == "" &&
			reservation.DrainStartedAt == "" && reservation.DrainCompletedAt == "" {
			return nil
		}
		return topologyrollout.RevokeDrain(ledger, drain.ReservationID,
			rollout.Status.FrozenPlan.Policy.LedgerUID, string(leaf.UID), drain.SessionToken,
			uint64(drain.ControlRevision))
	})
}

func (r *IOSXESoftwareRolloutReconciler) finishSettledDrain(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if err := validateManagerDrainBinding(rollout, target, leaf); err != nil {
		return err
	}
	drain := leaf.Status.ManagerDrain
	admission := leaf.Status.ManagerAdmission
	if drain.State != opsv1alpha1.UpgradeManagerDrainSettled ||
		admission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		return fmt.Errorf("leaf drain settlement is incomplete")
	}
	if err := validateDrainPodsComplete(drain); err != nil {
		return err
	}
	for i := range drain.Pods {
		if err := r.ensureCompletedDrainPodUnprotected(ctx, &drain.Pods[i], drain.SessionToken); err != nil {
			return fmt.Errorf("verify completed drain Pod protection: %w", err)
		}
	}
	// Replaying the exact drain settlement validates the durable release fence
	// when the reservation was removed before a manager crash.
	if err := r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.SettleDrainedReservation(ledger, drain.ReservationID,
			rollout.Status.FrozenPlan.Policy.LedgerUID, admission.TopologyLockID,
			string(leaf.UID), drain.SessionToken, admission.PolicyEpoch, true, true, true)
	}); err != nil {
		return fmt.Errorf("verify drained reservation release fence: %w", err)
	}
	if err := r.ensureDrainGuardRestored(ctx, rollout, target, leaf); err != nil {
		return err
	}
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return err
	}
	session := device.Status.MaintenanceSession
	if validateDrainSessionIdentity(session, target, leaf, drain) != nil ||
		session.Phase != ciskov1.DeviceMaintenanceSessionSettled {
		return fmt.Errorf("waiting for exact CiscoDevice Settled drain acknowledgement")
	}
	if err := r.ensureDrainLeaseIdle(ctx, rollout, target, leaf); err != nil {
		return fmt.Errorf("verify settled drain Lease fence: %w", err)
	}
	lockEpoch, lockID, err := r.topologyLockAcquisitionForRelease(
		ctx, rollout, target, admission.PolicyEpoch, admission.TopologyLockID,
	)
	if err != nil {
		return err
	}
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, lockEpoch, lockID); err != nil {
		return err
	}
	return r.releaseDeviceTopologyLock(ctx, rollout, target, lockEpoch, lockID)
}

func (r *IOSXESoftwareRolloutReconciler) ensureDrainGuardRestored(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &node); err != nil {
		return err
	}
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return err
	}
	if err := validateDrainSchedulingRestored(&device, &node, target, leaf); err != nil {
		return err
	}
	session := device.Status.MaintenanceSession
	if err := validateDrainSessionIdentity(session, target, leaf, leaf.Status.ManagerDrain); err != nil ||
		(session.Phase != ciskov1.DeviceMaintenanceSessionRecovering && session.Phase != ciskov1.DeviceMaintenanceSessionSettled) {
		return fmt.Errorf("waiting for exact maintenance session recovery acknowledgement")
	}
	return nil
}

func validateDrainSchedulingRestored(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if device == nil || node == nil || leaf == nil || leaf.Status.ManagerDrain == nil ||
		string(device.UID) != target.DeviceUID || string(node.UID) != target.NodeUID {
		return fmt.Errorf("waiting for exact device and Node scheduling identity to be restored")
	}
	drain := leaf.Status.ManagerDrain
	if string(node.UID) != drain.NodeUID ||
		hasDrainMaintenanceTaint(node.Spec.Taints) != drain.MaintenanceTaintPresentBefore {
		return fmt.Errorf("waiting for exact pre-drain scheduling state to be restored")
	}
	wantUnschedulable := drain.NodeUnschedulableBefore ||
		device.Annotations[managedprotocol.AnnotationDrainCordonHold] == "true"
	if node.Spec.Unschedulable != wantUnschedulable ||
		node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" ||
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		return fmt.Errorf("waiting for the drain-owned cordon to restore pre-drain or explicit operator-held state")
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) validateRecoveredPDBs(
	ctx context.Context,
	pod *opsv1alpha1.UpgradeDrainPodStatus,
) error {
	templateLabels, err := r.currentDrainTemplateLabels(ctx, pod)
	if err != nil {
		return err
	}
	for _, frozen := range pod.PDBs {
		var pdb policyv1.PodDisruptionBudget
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}, &pdb); err != nil {
			return err
		}
		selector, selectorErr := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if string(pdb.UID) != frozen.UID || !pdb.DeletionTimestamp.IsZero() || selectorErr != nil ||
			!selector.Matches(labels.Set(templateLabels)) ||
			pdb.Status.ObservedGeneration != pdb.Generation || pdb.Status.CurrentHealthy < pdb.Status.DesiredHealthy ||
			pdb.Status.ExpectedPods < pdb.Status.CurrentHealthy {
			return fmt.Errorf("PodDisruptionBudget %s/%s does not protect the current healthy replacement template", pdb.Namespace, pdb.Name)
		}
	}
	return nil
}
