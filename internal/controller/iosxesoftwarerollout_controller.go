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
	"net/url"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

const (
	rolloutSafetyFinalizer = "ops.cisco.vk/iosxesoftwarerollout-safety"
	rolloutPollInterval    = 10 * time.Second
)

// IOSXESoftwareRolloutReconciler is a fleet planner and admission controller.
// It deliberately has no device client: immutable IOSXESoftwareUpgrade leaves
// remain the only device-side executors.
type IOSXESoftwareRolloutReconciler struct {
	client.Client
	APIReader               client.Reader
	Scheme                  *runtime.Scheme
	TopologyPolicyNamespace string
	TopologyPolicyName      string
	Recorder                record.EventRecorder
	Now                     func() time.Time
}

func (r *IOSXESoftwareRolloutReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *IOSXESoftwareRolloutReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func (r *IOSXESoftwareRolloutReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	defer func() {
		metricResult := "complete"
		if retErr != nil {
			metricResult = "error"
		} else if result.Requeue || result.RequeueAfter > 0 {
			metricResult = "requeue"
		}
		topologyrollout.RecordReconcile(metricResult, retErr)
	}()
	var rollout opsv1alpha1.IOSXESoftwareRollout
	if err := r.reader().Get(ctx, req.NamespacedName, &rollout); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	now := r.now()
	ctx, _ = correlation.ApplyAnnotations(ctx, rollout.Annotations, now)
	planHash := ""
	if rollout.Status.FrozenPlan != nil {
		planHash = rollout.Status.FrozenPlan.Hash
	}
	ctx, span := correlation.Start(
		ctx,
		otel.Tracer("cisco-virtual-kubelet/iosxe-software-rollout-controller"),
		"cvk.iosxesoftwarerollout.reconcile",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("k8s.namespace.name", rollout.Namespace),
			attribute.String("k8s.resource.name", rollout.Name),
			attribute.String("k8s.resource.uid", string(rollout.UID)),
			attribute.String("k8s.resource.kind", "IOSXESoftwareRollout"),
			attribute.String("cvk.rollout.plan_hash", planHash),
			attribute.String("cvk.rollout.phase", string(rollout.Status.Phase)),
		),
	)
	defer func() {
		span.SetAttributes(
			attribute.String("cvk.reconcile.result", reconcileResultAttribute(result)),
			attribute.String("cvk.rollout.phase", string(rollout.Status.Phase)),
		)
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(codes.Error, "reconcile")
		}
		span.End()
	}()

	if !rollout.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &rollout, now)
	}
	if !hasExactString(rollout.Finalizers, rolloutSafetyFinalizer) {
		before := rollout.DeepCopy()
		rollout.Finalizers = append(rollout.Finalizers, rolloutSafetyFinalizer)
		if err := r.Patch(ctx, &rollout, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, fmt.Errorf("add rollout safety finalizer: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	// Successful and cancelled campaigns have no retained safety work left.
	// Preserve their immutable audit result across later policy or source
	// changes; deletion still runs through the finalizer path above.
	if rollout.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded ||
		rollout.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled {
		return ctrl.Result{}, nil
	}
	// Pause and cancellation are claim fences, so propagate them before any
	// dependency that can be unavailable (administrator policy, ledger, or
	// image Secret). Settlement can wait for those dependencies; revocation of
	// future device mutations cannot.
	if rollout.Status.FrozenPlan != nil && (rollout.Spec.Control.Pause || rollout.Spec.Control.Cancel) {
		if err := r.propagateControl(ctx, &rollout, rollout.Spec.Control.Pause, rollout.Spec.Control.Cancel, now); err != nil {
			return ctrl.Result{}, err
		}
		if rollout.Spec.Control.Cancel {
			if err := r.ensureCancellationFences(ctx, &rollout, rollout.Spec.Control.Revision, now); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	policyKey := types.NamespacedName{Namespace: r.TopologyPolicyNamespace, Name: r.TopologyPolicyName}
	if rollout.Status.FrozenPlan != nil {
		// Read identity before parsing or bootstrapping. A deleted, malformed, or
		// replaced policy must not leave an already-published grant claimable just
		// because policy parsing failed before the drift check.
		var policyObject corev1.ConfigMap
		if err := r.reader().Get(ctx, policyKey, &policyObject); err != nil {
			return r.reconcilePolicyUnavailable(ctx, &rollout,
				fmt.Sprintf("administrator policy is unavailable: %v", err), now)
		}
		frozen := rollout.Status.FrozenPlan.Policy
		if frozen.UID != string(policyObject.UID) {
			return r.reconcilePolicyChanged(ctx, &rollout, &topologyrollout.ParsedAdminPolicy{
				PolicyUID: string(policyObject.UID), ResourceVersion: policyObject.ResourceVersion,
			}, now)
		}
	}

	policy, err := topologyrollout.BootstrapAdminPolicy(
		ctx, r.Client, r.reader(),
		policyKey,
	)
	if err != nil {
		if rollout.Status.FrozenPlan != nil {
			return r.reconcilePolicyUnavailable(ctx, &rollout,
				fmt.Sprintf("administrator policy or reservation ledger is unavailable: %v", err), now)
		}
		return r.failRollout(ctx, &rollout, "PolicyUnavailable", err.Error(), false)
	}

	if rollout.Status.FrozenPlan == nil {
		frozen, targets, err := r.buildFrozenPlan(ctx, &rollout, policy, now)
		if err != nil {
			return r.failRollout(ctx, &rollout, "PlanningFailed", err.Error(), true)
		}
		before := rollout.DeepCopy()
		rollout.Status.ObservedGeneration = rollout.Generation
		rollout.Status.FrozenPlan = frozen
		rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
			Epoch: 1, Policy: frozen.Policy, UpdatedAt: metav1.NewTime(now),
		}
		rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhaseAwaitingApproval
		rollout.Status.Targets = targets
		rollout.Status.Counts = rolloutCounts(targets)
		rollout.Status.Message = "frozen plan created; approval of the exact plan hash is required"
		setRolloutCondition(&rollout, "Planned", metav1.ConditionTrue, "PlanFrozen", rollout.Status.Message, now)
		setRolloutCondition(&rollout, "Approved", metav1.ConditionFalse, "ApprovalRequired", rollout.Status.Message, now)
		if err := r.Status().Patch(ctx, &rollout,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("publish frozen rollout plan: %w", err)
		}
		r.emitRolloutEvent(&rollout, corev1.EventTypeNormal, "PlanFrozen", rollout.Status.Message)
		for _, target := range targets {
			topologyrollout.RecordTargetTransition("", string(target.Phase), target.Reason)
		}
		return ctrl.Result{}, nil
	}
	if result, handled, err := r.reconcilePolicyEpoch(ctx, &rollout, policy, now); handled || err != nil {
		return result, err
	}

	if rollout.Spec.Approval == nil {
		return r.updateRolloutSummary(ctx, &rollout, opsv1alpha1.IOSXESoftwareRolloutPhaseAwaitingApproval,
			"waiting for approval of "+rollout.Status.FrozenPlan.Hash, now)
	}
	if rollout.Spec.Approval.PlanHash != rollout.Status.FrozenPlan.Hash {
		return r.failRollout(ctx, &rollout, "ApprovalHashMismatch", "approval does not name the immutable frozen plan hash", true)
	}
	if err := r.recordApproval(ctx, &rollout, now); err != nil {
		return ctrl.Result{}, err
	}
	// Re-read after status mutation so every later optimistic patch starts from
	// the resourceVersion that actually contains the validated approval.
	if err := r.reader().Get(ctx, req.NamespacedName, &rollout); err != nil {
		return ctrl.Result{}, err
	}

	if rollout.Spec.Control.Cancel {
		return r.reconcileCancellation(ctx, &rollout, policy, now)
	}
	if rollout.Spec.Control.Pause {
		if err := r.propagateControl(ctx, &rollout, true, false, now); err != nil {
			return ctrl.Result{}, err
		}
		return r.updateRolloutSummary(ctx, &rollout, opsv1alpha1.IOSXESoftwareRolloutPhasePaused, "campaign is paused; no new mutation can be claimed", now)
	}

	if err := r.propagateControl(ctx, &rollout, false, false, now); err != nil {
		return ctrl.Result{}, err
	}
	return r.reconcileExecution(ctx, &rollout, policy, now)
}

func (r *IOSXESoftwareRolloutReconciler) buildFrozenPlan(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	policy *topologyrollout.ParsedAdminPolicy,
	now time.Time,
) (*opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus, []opsv1alpha1.IOSXESoftwareRolloutTargetStatus, error) {
	control := rollout.Spec.Control
	if control.Revision != 0 || control.Pause || control.Cancel || control.RequestedBy != "" ||
		control.RequestedAt != nil || control.Reason != "" {
		return nil, nil, fmt.Errorf("a rollout must be created with neutral control revision zero before planning")
	}
	selectorSpec := rollout.Spec.Plan.Targets.Selector.AsLabelSelector()
	selector, err := metav1.LabelSelectorAsSelector(&selectorSpec)
	if err != nil {
		return nil, nil, fmt.Errorf("target selector: %w", err)
	}
	if selector.Empty() != rollout.Spec.Plan.Targets.AllowAll {
		return nil, nil, fmt.Errorf("empty selector requires allowAll=true and allowAll requires an empty selector")
	}

	var listed ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &listed, client.InNamespace(rollout.Namespace)); err != nil {
		return nil, nil, fmt.Errorf("list target devices: %w", err)
	}
	devices := make([]ciskov1.CiscoDevice, 0, len(listed.Items))
	for i := range listed.Items {
		candidate := &listed.Items[i]
		if selector.Matches(labels.Set(candidate.Labels)) {
			devices = append(devices, *candidate.DeepCopy())
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	requestedCap := int(rollout.Spec.Plan.Targets.MaxTargets)
	if requestedCap == 0 {
		requestedCap = opsv1alpha1.MaxIOSXESoftwareRolloutTargets
	}
	effectiveCap := minInt(requestedCap, policy.Config.MaxCampaignTargets, opsv1alpha1.MaxIOSXESoftwareRolloutTargets)
	if len(devices) == 0 || len(devices) > effectiveCap {
		return nil, nil, fmt.Errorf("selector resolved %d targets; effective limit is %d", len(devices), effectiveCap)
	}

	cohorts, err := canaryAssignments(rollout.Spec.Plan.Canaries, devices)
	if err != nil {
		return nil, nil, err
	}
	source, err := r.freezeSource(ctx, rollout)
	if err != nil {
		return nil, nil, err
	}
	policySnapshot, err := freezePolicy(policy)
	if err != nil {
		return nil, nil, err
	}

	planned := make([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget, 0, len(devices))
	summaries := make([]opsv1alpha1.IOSXESoftwareRolloutTargetStatus, 0, len(devices))
	physicalIDs := map[string]string{}
	childNames := map[string]string{}
	for i := range devices {
		device := &devices[i]
		if !policy.Selector.Matches(labels.Set(device.Labels)) {
			return nil, nil, fmt.Errorf("target %s is outside the administrator managed fleet", device.Name)
		}
		target, err := r.freezeTarget(ctx, rollout, device, policy, cohorts[device.Name], now)
		if err != nil {
			return nil, nil, fmt.Errorf("target %s: %w", device.Name, err)
		}
		if other, duplicate := physicalIDs[target.PhysicalIdentity]; duplicate {
			return nil, nil, fmt.Errorf("targets %s and %s resolve to the same physical identity", other, device.Name)
		}
		physicalIDs[target.PhysicalIdentity] = device.Name
		if other, duplicate := childNames[target.ChildName]; duplicate {
			return nil, nil, fmt.Errorf("targets %s and %s resolve to the same retained child name", other, device.Name)
		}
		childNames[target.ChildName] = device.Name
		planned = append(planned, target)
		summaries = append(summaries, opsv1alpha1.IOSXESoftwareRolloutTargetStatus{
			DeviceName: device.Name, DeviceUID: string(device.UID), LeafName: target.ChildName,
			Phase: opsv1alpha1.IOSXESoftwareRolloutTargetPlanned, LastTransitionTime: metav1.NewTime(now),
		})
	}

	frozen := &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
		CreatedAt: metav1.NewTime(now), CampaignGeneration: rollout.Generation,
		Policy: policySnapshot, Source: source, Targets: planned,
	}
	canonical := struct {
		CampaignUID string                                          `json:"campaignUID"`
		Generation  int64                                           `json:"generation"`
		Plan        opsv1alpha1.IOSXESoftwareRolloutPlan            `json:"plan"`
		Policy      opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot  `json:"policy"`
		Source      opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot  `json:"source"`
		Targets     []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget `json:"targets"`
	}{string(rollout.UID), rollout.Generation, rollout.Spec.Plan, policySnapshot, source, planned}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, nil, fmt.Errorf("encode frozen plan: %w", err)
	}
	if len(encoded) > opsv1alpha1.MaxIOSXESoftwareRolloutPlanBytes {
		return nil, nil, fmt.Errorf("frozen plan is %d bytes; maximum is %d", len(encoded), opsv1alpha1.MaxIOSXESoftwareRolloutPlanBytes)
	}
	digest := sha256.Sum256(encoded)
	frozen.Hash = "sha256:" + hex.EncodeToString(digest[:])
	frozen.EncodedSizeBytes = int32(len(encoded))
	return frozen, summaries, nil
}

func (r *IOSXESoftwareRolloutReconciler) freezeSource(ctx context.Context, rollout *opsv1alpha1.IOSXESoftwareRollout) (opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot, error) {
	source := rollout.Spec.Plan.Source
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Hostname() == "" {
		return opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{}, fmt.Errorf("source URL must be an absolute credential-free URL without query or fragment")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "sftp" {
		return opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{}, fmt.Errorf("source scheme %q is not supported", parsed.Scheme)
	}
	snapshot := opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{URL: source.URL, SHA256: source.SHA256}
	if parsed.Scheme == "sftp" {
		if source.URLSecretRef == nil || source.URLSecretRef.Name == "" {
			return snapshot, fmt.Errorf("SFTP source requires an endpoint-bound Secret")
		}
		var secret corev1.Secret
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: source.URLSecretRef.Name}, &secret); err != nil {
			return snapshot, fmt.Errorf("read source Secret: %w", err)
		}
		if secret.UID == "" {
			return snapshot, fmt.Errorf("source Secret must have a Kubernetes UID")
		}
		if err := softwareupgrade.ValidateURLSecretEndpoint(&secret, source.URL); err != nil {
			return snapshot, fmt.Errorf("source Secret endpoint authorization is invalid: %w", err)
		}
		snapshot.SecretName = secret.Name
		snapshot.SecretUID = string(secret.UID)
	} else if source.URLSecretRef != nil {
		return snapshot, fmt.Errorf("HTTPS source credentials are not accepted by the managed rollout API")
	}
	return snapshot, nil
}

func (r *IOSXESoftwareRolloutReconciler) freezeTarget(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	device *ciskov1.CiscoDevice,
	policy *topologyrollout.ParsedAdminPolicy,
	cohort string,
	now time.Time,
) (opsv1alpha1.IOSXESoftwareRolloutPlannedTarget, error) {
	if device.Spec.Driver != ciskov1.DeviceDriverXE {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("driver %s is unsupported by IOSXESoftwareRollout", device.Spec.Driver)
	}
	if device.UID == "" || device.Status.NodeIdentity == nil || device.Status.TopologyProjection == nil {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("managed Node identity and topology projection are not ready")
	}
	identity := device.Status.NodeIdentity
	if identity.DeviceUID != string(device.UID) {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("device identity status is stale")
	}
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: identity.NodeName}, &node); err != nil {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("read bound Node: %w", err)
	}
	if string(node.UID) != identity.NodeUID || !managedNodeMatchesDevice(&node, device) ||
		node.Annotations[managedprotocol.AnnotationNodeUID] != identity.NodeUID {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("bound Node incarnation does not match CiscoDevice status")
	}
	if node.Annotations[managedprotocol.AnnotationWorkerProtocol] != managedprotocol.Version ||
		strings.TrimSpace(node.Annotations[managedprotocol.AnnotationWorkerUsername]) == "" {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("bound Node has not completed the %s managed worker handoff", managedprotocol.Version)
	}
	if device.Status.Phase != "Ready" ||
		!deviceConditionCurrentTrue(device, ciskov1.CiscoDeviceConditionNodeIdentityReady) ||
		!deviceConditionCurrentTrue(device, ciskov1.CiscoDeviceConditionTopologyReady) {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("device identity and topology are not ready")
	}
	if !deviceConditionCurrentTrue(device, ciskov1.CiscoDeviceConditionGNOIConfigurationReady) {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("device gNOI configuration is not ready")
	}
	if _, err := r.currentReadyWorkerRevision(ctx, device); err != nil {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("managed worker revision is not ready: %w", err)
	}
	readyCondition := nodeReadyCondition(&node)
	if readyCondition == nil || readyCondition.Status != corev1.ConditionTrue {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("bound Node is not Ready")
	}
	if !managedWorkerReadyForProjection(&node, device.Status.TopologyProjection) {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("bound Node has no current managed writer-ready proof")
	}
	if hasTopologyInitializationGuard(&node) {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("bound Node still carries the topology initialization guard")
	}
	freshnessSeconds := minPositive(
		policy.Config.HealthFreshnessSeconds,
		int(defaultInt32(rollout.Spec.Plan.Health.MaxObservationAgeSeconds, 300)),
	)
	healthConditionTypes := []string{
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	}
	healthObserved, err := managedDeviceHealthObservedAt(device, &node, now, healthConditionTypes...)
	if err != nil {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("managed device health observation is invalid: %w", err)
	}
	if healthObserved.Before(now.Add(-time.Duration(freshnessSeconds) * time.Second)) {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("managed device health observation is stale")
	}
	for _, key := range policy.Config.ProjectedTopologyKeys {
		if node.Labels[key] != device.Labels[key] {
			return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("projected Node topology %q does not match CiscoDevice authority", key)
		}
	}
	physicalID, err := stablePhysicalIdentity(device, &node)
	if err != nil {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, err
	}
	if device.Labels[managedprotocol.ImageFamilyLabel] != rollout.Spec.Plan.Source.ImageFamily {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("%s must equal source imageFamily %q", managedprotocol.ImageFamilyLabel, rollout.Spec.Plan.Source.ImageFamily)
	}
	qualificationCohort := strings.TrimSpace(device.Labels[managedprotocol.QualificationCohortLabel])
	if qualificationCohort == "" {
		return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("required qualification label %s is missing", managedprotocol.QualificationCohortLabel)
	}
	topologyValues := make([]opsv1alpha1.IOSXESoftwareRolloutTopologyValue, 0, len(policy.Config.RequiredTopologyKeys))
	for _, key := range policy.Config.RequiredTopologyKeys {
		value := device.Labels[key]
		if value == "" {
			return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, fmt.Errorf("required topology value %q is missing", key)
		}
		topologyValues = append(topologyValues, opsv1alpha1.IOSXESoftwareRolloutTopologyValue{Key: key, Value: value})
	}
	sort.Slice(topologyValues, func(i, j int) bool { return topologyValues[i].Key < topologyValues[j].Key })
	wave := int32(1)
	if cohort != "" {
		wave = 0
	}
	return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		DeviceName: device.Name, DeviceUID: string(device.UID), DeviceGeneration: device.Generation,
		PhysicalIdentity: physicalID,
		NodeName:         identity.NodeName, NodeUID: identity.NodeUID, Driver: string(device.Spec.Driver),
		ImageFamily: rollout.Spec.Plan.Source.ImageFamily, QualificationCohort: qualificationCohort,
		WorkerProtocolVersion: managedprotocol.Version,
		ProjectionHash:        device.Status.TopologyProjection.EffectiveLabelHash, Topology: topologyValues,
		CanaryCohort: cohort, Wave: wave, ChildName: rolloutChildName(rollout, device),
	}, nil
}

func hasTopologyInitializationGuard(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Key == managedprotocol.TopologyInitializingTaint {
			return true
		}
	}
	return false
}

func freezePolicy(policy *topologyrollout.ParsedAdminPolicy) (opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot, error) {
	if policy == nil {
		return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{}, fmt.Errorf("administrator policy is nil")
	}
	semanticHash, structuralHash, err := topologyrollout.AdminPolicyHashes(policy.Config)
	if err != nil {
		return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{}, fmt.Errorf("hash administrator policy: %w", err)
	}
	keys := map[string]struct{}{}
	for key := range policy.Config.DomainMaxUnavailable {
		keys[key] = struct{}{}
	}
	for key := range policy.Config.DomainMaxConcurrentTransfers {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	domains := make([]opsv1alpha1.IOSXESoftwareRolloutDomainBudget, 0, len(ordered))
	for _, key := range ordered {
		domain := opsv1alpha1.IOSXESoftwareRolloutDomainBudget{TopologyKey: key}
		if value, ok := policy.Config.DomainMaxConcurrentTransfers[key]; ok {
			domain.MaxConcurrentTransfers = ptr.To(int32(value))
		}
		if value, ok := policy.Config.DomainMaxUnavailable[key]; ok {
			domain.MaxUnavailable = ptr.To(int32(value))
		}
		domains = append(domains, domain)
	}
	return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{
		Name: policy.Name, Namespace: policy.Namespace,
		UID: policy.PolicyUID, ResourceVersion: policy.ResourceVersion,
		SemanticHash: semanticHash, StructuralHash: structuralHash,
		MaxTargets:             int32(policy.Config.MaxCampaignTargets),
		MaxConcurrentTransfers: int32(policy.Config.GlobalMaxConcurrentTransfers),
		MaxUnavailable:         int32(policy.Config.GlobalMaxUnavailable), Domains: domains,
		HealthFreshnessSeconds: int32(policy.Config.HealthFreshnessSeconds),
		LedgerNamespace:        policy.Namespace, LedgerName: policy.Config.LedgerName, LedgerUID: policy.LedgerUID,
		MaxActiveReservations: int32(policy.Config.MaxActiveReservations), MaxLedgerSizeBytes: int32(policy.Config.MaxLedgerBytes),
	}, nil
}

func canaryAssignments(
	cohorts []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort,
	devices []ciskov1.CiscoDevice,
) (map[string]string, error) {
	targets := make(map[string]string, len(devices))
	requiredCohorts := make(map[string]struct{}, len(devices))
	for i := range devices {
		cohort := strings.TrimSpace(devices[i].Labels[managedprotocol.QualificationCohortLabel])
		if cohort == "" {
			return nil, fmt.Errorf("target %q is missing required qualification label %s", devices[i].Name, managedprotocol.QualificationCohortLabel)
		}
		targets[devices[i].Name] = cohort
		requiredCohorts[cohort] = struct{}{}
	}
	assignments := map[string]string{}
	coveredCohorts := map[string]struct{}{}
	for _, cohort := range cohorts {
		if _, duplicate := coveredCohorts[cohort.Name]; duplicate {
			return nil, fmt.Errorf("canary cohort %q is repeated", cohort.Name)
		}
		for _, name := range cohort.Devices {
			targetCohort, ok := targets[name]
			if !ok {
				return nil, fmt.Errorf("canary cohort %q names non-target device %q", cohort.Name, name)
			}
			if targetCohort != cohort.Name {
				return nil, fmt.Errorf("canary device %q belongs to qualification cohort %q, not %q", name, targetCohort, cohort.Name)
			}
			if prior, duplicate := assignments[name]; duplicate {
				return nil, fmt.Errorf("device %q appears in canary cohorts %q and %q", name, prior, cohort.Name)
			}
			assignments[name] = cohort.Name
		}
		if len(cohort.Devices) > 0 {
			coveredCohorts[cohort.Name] = struct{}{}
		}
	}
	if len(assignments) == 0 {
		return nil, fmt.Errorf("at least one explicit canary device is required")
	}
	for cohort := range requiredCohorts {
		if _, covered := coveredCohorts[cohort]; !covered {
			return nil, fmt.Errorf("qualification cohort %q has no explicit canary", cohort)
		}
	}
	return assignments, nil
}

func stablePhysicalIdentity(device *ciskov1.CiscoDevice, node *corev1.Node) (string, error) {
	if device == nil || device.Status.NodeIdentity == nil {
		return "", fmt.Errorf("CiscoDevice has no manager-bound physical identity")
	}
	declared, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
	if err != nil {
		return "", fmt.Errorf("declared physical identity is invalid: %w", err)
	}
	if device.Status.NodeIdentity.PhysicalIdentity != declared {
		return "", fmt.Errorf("manager-bound physical identity %q does not match declaration %q",
			device.Status.NodeIdentity.PhysicalIdentity, declared)
	}
	if node == nil {
		return "", fmt.Errorf("bound Node is absent while checking physical identity")
	}
	observed, err := topology.ObservedPhysicalIdentity(
		device.Spec.PhysicalIdentity,
		node.Status.NodeInfo.MachineID,
		node.Status.NodeInfo.SystemUUID,
	)
	if err != nil {
		return "", fmt.Errorf("bound Node physical identity is invalid: %w", err)
	}
	return observed, nil
}

func rolloutChildName(rollout *opsv1alpha1.IOSXESoftwareRollout, device *ciskov1.CiscoDevice) string {
	digest := sha256.Sum256([]byte(string(rollout.UID) + "/" + string(device.UID)))
	suffix := "-" + hex.EncodeToString(digest[:4])
	base := rollout.Name + "-" + device.Name
	if len(base) > 253-len(suffix) {
		base = strings.TrimRight(base[:253-len(suffix)], "-.")
	}
	return base + suffix
}

func reservationID(rolloutUID, deviceUID string) string {
	digest := sha256.Sum256([]byte(rolloutUID + "/" + deviceUID))
	return hex.EncodeToString(digest[:16])
}

func (r *IOSXESoftwareRolloutReconciler) recordApproval(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	now time.Time,
) error {
	if rollout.Status.Approval != nil && rollout.Status.Approval.Valid &&
		rollout.Status.Approval.PlanHash == rollout.Status.FrozenPlan.Hash {
		return nil
	}
	before := rollout.DeepCopy()
	rollout.Status.Approval = &opsv1alpha1.IOSXESoftwareRolloutApprovalStatus{
		PlanHash: rollout.Spec.Approval.PlanHash, Valid: true,
		Reason: "ExactPlanApproved", ValidatedAt: metav1.NewTime(now),
	}
	setRolloutCondition(rollout, "Approved", metav1.ConditionTrue, "ExactPlanApproved", "exact frozen plan hash was approved", now)
	if err := r.Status().Patch(ctx, rollout,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("record rollout approval: %w", err)
	}
	r.emitRolloutEvent(rollout, corev1.EventTypeNormal, "RolloutApproved", "exact frozen plan hash was approved")
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) reconcileExecution(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	now time.Time,
) (ctrl.Result, error) {
	effectivePolicy, err := effectiveAdmissionPolicy(rollout, currentPolicy, now)
	if err != nil {
		return r.failRollout(ctx, rollout, "PolicyChangedIncompatibly", err.Error(), false)
	}
	if err := r.verifyFrozenSource(ctx, rollout); err != nil {
		return r.reconcileSourceChanged(ctx, rollout, err.Error(), now)
	}

	children, err := r.rolloutChildren(ctx, rollout)
	if err != nil {
		return ctrl.Result{}, err
	}
	summaries := indexTargetSummaries(rollout.Status.Targets)
	terminalFailurePresent := false
	for _, leaf := range children {
		if terminalLeafFailure(leaf.Status.Phase) {
			terminalFailurePresent = true
			break
		}
	}
	if terminalFailurePresent {
		// Revoke and settle every not-yet-claimed target before reporting the
		// terminal campaign phase. Workers independently scan durable sibling
		// failures at each claim, and every manager admission path does the same,
		// so a crash anywhere in this multi-object fence cannot authorize a new
		// device mutation.
		if err := r.ensureFailureFences(ctx, rollout, children, now); err != nil {
			return ctrl.Result{RequeueAfter: rolloutPollInterval}, err
		}
		children, err = r.rolloutChildren(ctx, rollout)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	if terminalFailurePresent && rollout.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseFailed {
		for _, target := range rollout.Status.FrozenPlan.Targets {
			if leaf, ok := children[target.ChildName]; ok && terminalLeafFailure(leaf.Status.Phase) {
				summary := summaries[target.DeviceUID]
				summary.LeafUID, summary.LeafName = string(leaf.UID), leaf.Name
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetFailed,
					string(leaf.Status.Phase), leaf.Status.Message, now)
				summaries[target.DeviceUID] = summary
			}
		}
		return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhaseFailed,
			"one or more targets failed; campaign-wide admission is stopped", now)
	}
	allCanariesSettled := true
	allTargetsSettled := true
	hasFailure := false
	hasRunning := false
	hasSoaking := false
	progressionBlocked := false
	rearmedPolicyLeaf := false

	for _, target := range rollout.Status.FrozenPlan.Targets {
		leaf, exists := children[target.ChildName]
		if !exists {
			allTargetsSettled = false
			if target.CanaryCohort != "" {
				allCanariesSettled = false
			}
			continue
		}
		if err := validateManagedLeafBinding(rollout, target, &leaf); err != nil {
			return r.failRollout(ctx, rollout, "ChildIdentityConflict", err.Error(), false)
		}
		if leaf.Status.ManagerAdmission == nil {
			if err := r.ensureChildAdmission(ctx, rollout, target, &leaf, now); err != nil {
				return ctrl.Result{}, err
			}
			summary := summaries[target.DeviceUID]
			summary.LeafUID = string(leaf.UID)
			transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetWaitingForAdmission,
				"AdmissionBindingRecovered", "manager restored the durable reservation binding; waiting for worker acknowledgement", now)
			summaries[target.DeviceUID] = summary
			allTargetsSettled = false
			if target.CanaryCohort != "" {
				allCanariesSettled = false
			}
			continue
		}
		summary := summaries[target.DeviceUID]
		summary.LeafUID = string(leaf.UID)
		summary.LeafName = leaf.Name
		if !terminalFailurePresent && !rearmedPolicyLeaf && leaf.Status.ManagerAdmission != nil &&
			leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked &&
			leaf.Status.ManagerAdmission.RevocationReason == "PolicyEpochTransition" &&
			leaf.Status.ManagerAdmission.PolicyEpoch < effectivePolicy.Epoch && len(leaf.Status.ManagedMutationClaims) == 0 {
			if err := r.rearmPolicyEpochLeaf(ctx, rollout, currentPolicy, effectivePolicy, target, &leaf, now); err != nil {
				if errors.Is(err, topologyrollout.ErrBudgetExceeded) || errors.Is(err, topologyrollout.ErrTargetUnavailable) ||
					errors.Is(err, errWorkloadsRunning) || errors.Is(err, topologyrollout.ErrStaleControlRevision) {
					transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetBlocked,
						"PolicyEpochRearmBlocked", err.Error(), now)
					progressionBlocked = true
				} else {
					return ctrl.Result{}, err
				}
			} else {
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetWaitingForAdmission,
					"PolicyEpochRearmed", "reservation rebound; waiting for worker acknowledgement of the new policy epoch", now)
				rearmedPolicyLeaf = true
			}
			allTargetsSettled = false
			if target.CanaryCohort != "" {
				allCanariesSettled = false
			}
		} else if terminalLeafPhase(leaf.Status.Phase) {
			settled, gateReason, gateMessage, settleErr := r.trySettleLeaf(ctx, rollout, currentPolicy, target, &leaf, summary, now)
			if settleErr != nil {
				return ctrl.Result{}, settleErr
			}
			if terminalLeafFailure(leaf.Status.Phase) && settled {
				hasFailure = true
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetFailed, string(leaf.Status.Phase), leaf.Status.Message, now)
			} else if settled {
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetSucceeded, "HealthGatePassed", leaf.Status.Message, now)
			} else {
				if terminalLeafFailure(leaf.Status.Phase) {
					hasFailure = true
				}
				if gateReason == "HealthyPostMutationSoak" {
					transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetSoaking, gateReason, gateMessage, now)
					hasSoaking = true
				} else {
					transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetBlocked, gateReason, gateMessage, now)
				}
				progressionBlocked = true
				allTargetsSettled = false
				if target.CanaryCohort != "" {
					allCanariesSettled = false
				}
			}
		} else {
			allTargetsSettled = false
			if target.CanaryCohort != "" {
				allCanariesSettled = false
			}
			if leaf.Status.ManagerAdmission != nil && leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionPending && !terminalFailurePresent {
				granted, grantErr := r.tryGrantLeaf(ctx, rollout, currentPolicy, effectivePolicy, target, &leaf, now)
				if grantErr != nil {
					return ctrl.Result{}, grantErr
				}
				if granted {
					transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetAdmitted, "ReservationGranted", "worker acknowledged the managed protocol and admission was granted", now)
					hasRunning = true
				} else {
					transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetWaitingForAdmission, "WorkerProtocolPending", "waiting for worker protocol acknowledgement", now)
				}
			} else {
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetRunning, string(leaf.Status.Phase), leaf.Status.Message, now)
				hasRunning = true
			}
		}
		summaries[target.DeviceUID] = summary
	}

	if hasFailure {
		return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhaseFailed,
			"one or more targets failed; no additional targets will be admitted", now)
	}
	if allTargetsSettled {
		return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded,
			"all targets completed and passed their post-mutation health gates", now)
	}

	canaryResume := canaryResumeAuthorized(rollout)
	if allCanariesSettled && pauseAfterCanary(rollout) && !canaryResume {
		message := "canaries passed; increment spec.control.revision with pause=false to authorize wider rollout"
		return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhasePaused,
			message, now, metav1.Condition{
				Type: "CanaryResumeRequired", Status: metav1.ConditionTrue, ObservedGeneration: rollout.Generation,
				Reason: "CanariesPassed", Message: message, LastTransitionTime: metav1.NewTime(now),
			})
	}
	var completionConditions []metav1.Condition
	if allCanariesSettled && pauseAfterCanary(rollout) && canaryResume {
		if condition := meta.FindStatusCondition(rollout.Status.Conditions, "CanaryResumeRequired"); condition != nil &&
			condition.Status == metav1.ConditionTrue {
			completionConditions = append(completionConditions, metav1.Condition{
				Type: "CanaryResumeRequired", Status: metav1.ConditionFalse, ObservedGeneration: rollout.Generation,
				Reason: "ResumeAuthorized", Message: "a newer control revision authorized the wider rollout",
				LastTransitionTime: metav1.NewTime(now),
			})
		}
	}

	admitted := false
	for _, target := range rollout.Status.FrozenPlan.Targets {
		if progressionBlocked {
			break
		}
		if _, exists := children[target.ChildName]; exists {
			continue
		}
		if !allCanariesSettled && target.Wave > 0 {
			continue
		}
		if err := r.admitTarget(ctx, rollout, currentPolicy, effectivePolicy, target, now); err != nil {
			if errors.Is(err, topologyrollout.ErrBudgetExceeded) || errors.Is(err, topologyrollout.ErrTargetUnavailable) || errors.Is(err, errWorkloadsRunning) {
				summary := summaries[target.DeviceUID]
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetBlocked, "AdmissionBlocked", err.Error(), now)
				summaries[target.DeviceUID] = summary
				continue
			}
			return ctrl.Result{}, err
		}
		admitted = true
		break // one reservation/child transaction per reconciliation
	}
	phase := opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting
	message := "campaign is executing within frozen topology budgets"
	if !admitted {
		message = "campaign is waiting for a reservation, worker acknowledgement, or health gate"
		if hasSoaking && !hasRunning {
			phase = opsv1alpha1.IOSXESoftwareRolloutPhaseSoaking
			message = "campaign is holding reservations during the continuous post-mutation health soak"
		}
	}
	return r.patchExecutionStatus(ctx, rollout, summaries, phase, message, now, completionConditions...)
}

func pauseAfterCanary(rollout *opsv1alpha1.IOSXESoftwareRollout) bool {
	return rollout.Spec.Plan.PauseAfterCanary == nil || *rollout.Spec.Plan.PauseAfterCanary
}

func canaryResumeAuthorized(rollout *opsv1alpha1.IOSXESoftwareRollout) bool {
	condition := meta.FindStatusCondition(rollout.Status.Conditions, "CanaryResumeRequired")
	if condition == nil {
		return false
	}
	if condition.Status == metav1.ConditionFalse && condition.Reason == "ResumeAuthorized" {
		return true
	}
	return condition.Status == metav1.ConditionTrue &&
		rollout.Status.Control != nil &&
		rollout.Generation > condition.ObservedGeneration &&
		rollout.Spec.Control.Revision > rollout.Status.Control.RequestedRevision &&
		!rollout.Spec.Control.Pause && !rollout.Spec.Control.Cancel
}
