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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

var errWorkloadsRunning = errors.New("workloads are running on the target Node")

const (
	rolloutTargetDeviceNameIndex = "status.frozenPlan.targets.deviceName"
	rolloutTargetNodeNameIndex   = "status.frozenPlan.targets.nodeName"
	rolloutSourceSecretNameIndex = "status.frozenPlan.source.secretName"
	rolloutPodNodeNameIndex      = "spec.nodeName"
)

func (r *IOSXESoftwareRolloutReconciler) SetupWithManager(mgr ctrl.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(context.Background(), // ctxlint:allow manager field-index registration root
		&opsv1alpha1.IOSXESoftwareRollout{},
		rolloutTargetDeviceNameIndex, rolloutTargetDeviceNameIndexValues); err != nil {
		return fmt.Errorf("index rollout target device names: %w", err)
	}
	if err := indexer.IndexField(context.Background(), // ctxlint:allow manager field-index registration root
		&opsv1alpha1.IOSXESoftwareRollout{},
		rolloutTargetNodeNameIndex, rolloutTargetNodeNameIndexValues); err != nil {
		return fmt.Errorf("index rollout target Node names: %w", err)
	}
	if err := indexer.IndexField(context.Background(), // ctxlint:allow manager field-index registration root
		&opsv1alpha1.IOSXESoftwareRollout{},
		rolloutSourceSecretNameIndex, rolloutSourceSecretNameIndexValues); err != nil {
		return fmt.Errorf("index rollout source Secret names: %w", err)
	}
	// The manager normally uses its uncached APIReader, for which spec.nodeName
	// is a server-supported Pod field selector. Register the same index so a
	// reconciler deliberately constructed with only the cached client retains
	// identical fail-closed workload checks.
	if err := indexer.IndexField(context.Background(), // ctxlint:allow manager field-index registration root
		&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues); err != nil {
		return fmt.Errorf("index rollout Pod Node names: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.IOSXESoftwareRollout{}).
		Watches(&opsv1alpha1.IOSXESoftwareUpgrade{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, object client.Object) []reconcile.Request {
				annotations := object.GetAnnotations()
				namespace := annotations[managedprotocol.AnnotationCampaignNamespace]
				name := annotations[managedprotocol.AnnotationCampaignName]
				if namespace == "" || name == "" {
					return nil
				}
				return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}}
			},
		)).
		Watches(&ciskov1.CiscoDevice{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, object client.Object) []reconcile.Request {
				return r.rolloutRequestsByField(ctx, object.GetNamespace(), rolloutTargetDeviceNameIndex, object.GetName())
			},
		)).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, object client.Object) []reconcile.Request {
				return r.rolloutRequestsByField(ctx, "", rolloutTargetNodeNameIndex, object.GetName())
			},
		)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, object client.Object) []reconcile.Request {
				return r.rolloutRequestsByField(ctx, object.GetNamespace(), rolloutSourceSecretNameIndex, object.GetName())
			},
		)).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, object client.Object) []reconcile.Request {
				if object.GetNamespace() != r.TopologyPolicyNamespace || object.GetName() != r.TopologyPolicyName {
					return nil
				}
				return r.rolloutRequests(ctx, "")
			},
		)).
		Complete(r)
}

func rolloutTargetDeviceNameIndexValues(object client.Object) []string {
	return rolloutTargetIndexValues(object, func(target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) string {
		return target.DeviceName
	})
}

func rolloutTargetNodeNameIndexValues(object client.Object) []string {
	return rolloutTargetIndexValues(object, func(target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) string {
		return target.NodeName
	})
}

func rolloutTargetIndexValues(
	object client.Object,
	value func(opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) string,
) []string {
	rollout, ok := object.(*opsv1alpha1.IOSXESoftwareRollout)
	if !ok || rollout.Status.FrozenPlan == nil {
		return nil
	}
	values := make([]string, 0, len(rollout.Status.FrozenPlan.Targets))
	for _, target := range rollout.Status.FrozenPlan.Targets {
		if indexed := value(target); indexed != "" {
			values = append(values, indexed)
		}
	}
	return values
}

func rolloutSourceSecretNameIndexValues(object client.Object) []string {
	rollout, ok := object.(*opsv1alpha1.IOSXESoftwareRollout)
	if !ok || rollout.Status.FrozenPlan == nil || rollout.Status.FrozenPlan.Source.SecretName == "" {
		return nil
	}
	return []string{rollout.Status.FrozenPlan.Source.SecretName}
}

func rolloutPodNodeNameIndexValues(object client.Object) []string {
	pod, ok := object.(*corev1.Pod)
	if !ok || pod.Spec.NodeName == "" {
		return nil
	}
	return []string{pod.Spec.NodeName}
}

func (r *IOSXESoftwareRolloutReconciler) rolloutRequestsByField(
	ctx context.Context,
	namespace, field, value string,
) []reconcile.Request {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return r.rolloutRequests(ctx, namespace, client.MatchingFields{field: value})
}

func (r *IOSXESoftwareRolloutReconciler) rolloutRequests(
	ctx context.Context,
	namespace string,
	options ...client.ListOption,
) []reconcile.Request {
	if namespace != "" {
		options = append(options, client.InNamespace(namespace))
	}
	var rollouts opsv1alpha1.IOSXESoftwareRolloutList
	if err := r.Client.List(ctx, &rollouts, options...); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "map dependency to IOSXESoftwareRollout")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(rollouts.Items))
	for i := range rollouts.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&rollouts.Items[i])})
	}
	return requests
}

func effectiveAdmissionPolicy(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	current *topologyrollout.ParsedAdminPolicy,
	now time.Time,
) (topologyrollout.Policy, error) {
	if rollout == nil || rollout.Status.FrozenPlan == nil || rollout.Status.EffectivePolicy == nil || current == nil {
		return topologyrollout.Policy{}, fmt.Errorf("rollout policy inputs are incomplete")
	}
	frozen := rollout.Status.FrozenPlan.Policy
	effective := rollout.Status.EffectivePolicy
	if frozen.Namespace != current.Namespace || frozen.Name != current.Name || frozen.UID != current.PolicyUID {
		return topologyrollout.Policy{}, fmt.Errorf("administrator policy identity changed after approval")
	}
	if frozen.LedgerNamespace != current.Namespace || frozen.LedgerName != current.Config.LedgerName || frozen.LedgerUID != current.LedgerUID {
		return topologyrollout.Policy{}, fmt.Errorf("reservation ledger identity changed after approval")
	}
	currentSnapshot, err := freezePolicy(current)
	if err != nil {
		return topologyrollout.Policy{}, err
	}
	if err := validateFrozenRequiredTopologyKeys(rollout.Status.FrozenPlan.Targets, current.Config.RequiredTopologyKeys); err != nil {
		return topologyrollout.Policy{}, err
	}
	requestedCap := int(rollout.Spec.Plan.Targets.MaxTargets)
	if requestedCap == 0 {
		requestedCap = opsv1alpha1.MaxIOSXESoftwareRolloutTargets
	}
	if cap := minInt(requestedCap, current.Config.MaxCampaignTargets, int(effective.Policy.MaxTargets), opsv1alpha1.MaxIOSXESoftwareRolloutTargets); len(rollout.Status.FrozenPlan.Targets) > cap {
		return topologyrollout.Policy{}, fmt.Errorf("administrator target ceiling tightened to %d for a %d-target approved plan", cap, len(rollout.Status.FrozenPlan.Targets))
	}
	if currentSnapshot.StructuralHash != effective.Policy.StructuralHash ||
		currentSnapshot.SemanticHash != effective.Policy.SemanticHash {
		return topologyrollout.Policy{}, fmt.Errorf("administrator policy is not published in effective epoch %d", effective.Epoch)
	}
	if effective.Epoch < 1 || effective.Policy.UID != frozen.UID || effective.Policy.LedgerUID != frozen.LedgerUID {
		return topologyrollout.Policy{}, fmt.Errorf("effective policy epoch identity is invalid")
	}
	if cap := minInt(requestedCap, int(effective.Policy.MaxTargets), opsv1alpha1.MaxIOSXESoftwareRolloutTargets); len(rollout.Status.FrozenPlan.Targets) > cap {
		return topologyrollout.Policy{}, fmt.Errorf("effective target ceiling %d is below the frozen target count", cap)
	}
	policy := topologyrollout.Policy{
		UID: effective.Policy.UID, Version: effective.Policy.ResourceVersion, Epoch: effective.Epoch,
		GlobalMaxConcurrentTransfers: int(effective.Policy.MaxConcurrentTransfers),
		GlobalMaxUnavailable:         int(effective.Policy.MaxUnavailable),
		MaxActiveRecords:             int(effective.Policy.MaxActiveReservations),
		MaxSerializedBytes:           int(effective.Policy.MaxLedgerSizeBytes),
		DomainTransferBudgets:        map[string]int{}, DomainBudgets: map[string]int{},
	}
	freshness := minPositive(int(effective.Policy.HealthFreshnessSeconds), int(defaultInt32(rollout.Spec.Plan.Health.MaxObservationAgeSeconds, 300)))
	policy.RequiredHealthFreshBy = now.Add(-time.Duration(freshness) * time.Second)
	for _, domain := range effective.Policy.Domains {
		if domain.MaxConcurrentTransfers != nil {
			policy.DomainTransferBudgets[domain.TopologyKey] = int(*domain.MaxConcurrentTransfers)
		}
		if domain.MaxUnavailable != nil {
			policy.DomainBudgets[domain.TopologyKey] = int(*domain.MaxUnavailable)
		}
	}
	return policy, nil
}

func strictestPolicySnapshot(previous, frozen, current opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot) (opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot, error) {
	if previous.UID != frozen.UID || current.UID != frozen.UID || previous.LedgerUID != frozen.LedgerUID || current.LedgerUID != frozen.LedgerUID {
		return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{}, fmt.Errorf("policy or ledger identity changed")
	}
	out := current
	out.MaxTargets = int32(minPositive(int(previous.MaxTargets), int(frozen.MaxTargets), int(current.MaxTargets)))
	out.MaxConcurrentTransfers = int32(minPositive(int(previous.MaxConcurrentTransfers), int(frozen.MaxConcurrentTransfers), int(current.MaxConcurrentTransfers)))
	out.MaxUnavailable = int32(minPositive(int(previous.MaxUnavailable), int(frozen.MaxUnavailable), int(current.MaxUnavailable)))
	out.HealthFreshnessSeconds = int32(minPositive(int(previous.HealthFreshnessSeconds), int(frozen.HealthFreshnessSeconds), int(current.HealthFreshnessSeconds)))
	out.MaxActiveReservations = int32(minPositive(int(previous.MaxActiveReservations), int(frozen.MaxActiveReservations), int(current.MaxActiveReservations)))
	out.MaxLedgerSizeBytes = int32(minPositive(int(previous.MaxLedgerSizeBytes), int(frozen.MaxLedgerSizeBytes), int(current.MaxLedgerSizeBytes)))
	previousDomains := policyDomainsByKey(previous.Domains)
	frozenDomains := policyDomainsByKey(frozen.Domains)
	currentDomains := policyDomainsByKey(current.Domains)
	if len(previousDomains) != len(frozenDomains) || len(currentDomains) != len(frozenDomains) {
		return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{}, fmt.Errorf("policy risk-domain set changed")
	}
	for i := range out.Domains {
		key := out.Domains[i].TopologyKey
		prior, priorOK := previousDomains[key]
		approved, approvedOK := frozenDomains[key]
		if !priorOK || !approvedOK {
			return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{}, fmt.Errorf("policy risk-domain %q changed", key)
		}
		transfer, err := strictestOptionalLimit(prior.MaxConcurrentTransfers, approved.MaxConcurrentTransfers, out.Domains[i].MaxConcurrentTransfers)
		if err != nil {
			return out, fmt.Errorf("transfer budget for %q: %w", key, err)
		}
		unavailable, err := strictestOptionalLimit(prior.MaxUnavailable, approved.MaxUnavailable, out.Domains[i].MaxUnavailable)
		if err != nil {
			return out, fmt.Errorf("unavailable budget for %q: %w", key, err)
		}
		out.Domains[i].MaxConcurrentTransfers, out.Domains[i].MaxUnavailable = transfer, unavailable
	}
	return out, nil
}

func policyDomainsByKey(domains []opsv1alpha1.IOSXESoftwareRolloutDomainBudget) map[string]opsv1alpha1.IOSXESoftwareRolloutDomainBudget {
	out := make(map[string]opsv1alpha1.IOSXESoftwareRolloutDomainBudget, len(domains))
	for _, domain := range domains {
		out[domain.TopologyKey] = domain
	}
	return out
}

func strictestOptionalLimit(values ...*int32) (*int32, error) {
	if len(values) == 0 || values[0] == nil {
		for _, value := range values {
			if value != nil {
				return nil, fmt.Errorf("budget presence changed")
			}
		}
		return nil, nil
	}
	ints := make([]int, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, fmt.Errorf("budget presence changed")
		}
		ints = append(ints, int(*value))
	}
	result := int32(minPositive(ints...))
	return &result, nil
}

func (r *IOSXESoftwareRolloutReconciler) reconcilePolicyEpoch(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	current *topologyrollout.ParsedAdminPolicy,
	now time.Time,
) (ctrl.Result, bool, error) {
	if rollout == nil || rollout.Status.FrozenPlan == nil || current == nil {
		return ctrl.Result{}, true, fmt.Errorf("rollout policy epoch inputs are incomplete")
	}
	frozen := rollout.Status.FrozenPlan.Policy
	currentSnapshot, err := freezePolicy(current)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	if frozen.UID != currentSnapshot.UID || frozen.Name != currentSnapshot.Name || frozen.Namespace != currentSnapshot.Namespace ||
		frozen.LedgerUID != currentSnapshot.LedgerUID || frozen.LedgerName != currentSnapshot.LedgerName ||
		frozen.LedgerNamespace != currentSnapshot.LedgerNamespace {
		result, err := r.reconcilePolicyChanged(ctx, rollout, current, now)
		return result, true, err
	}
	if frozen.StructuralHash == "" || currentSnapshot.StructuralHash != frozen.StructuralHash {
		result, err := r.reconcilePolicyChanged(ctx, rollout, current, now)
		return result, true, err
	}
	requestedCap := int(rollout.Spec.Plan.Targets.MaxTargets)
	if requestedCap == 0 {
		requestedCap = opsv1alpha1.MaxIOSXESoftwareRolloutTargets
	}
	if len(rollout.Status.FrozenPlan.Targets) > minInt(requestedCap, int(currentSnapshot.MaxTargets), opsv1alpha1.MaxIOSXESoftwareRolloutTargets) {
		result, err := r.reconcilePolicyChanged(ctx, rollout, current, now)
		return result, true, err
	}

	if rollout.Status.EffectivePolicy == nil {
		// Compatibility recovery is safe only while the exact policy used to
		// produce the frozen plan is still present. New plans always publish this
		// field atomically with frozenPlan.
		if frozen.ResourceVersion != currentSnapshot.ResourceVersion || frozen.SemanticHash == "" {
			result, err := r.reconcilePolicyChanged(ctx, rollout, current, now)
			return result, true, err
		}
		before := rollout.DeepCopy()
		rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
			Epoch: 1, Policy: frozen, UpdatedAt: metav1.NewTime(now),
		}
		if err := r.Status().Patch(ctx, rollout,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, true, fmt.Errorf("recover rollout effective policy epoch: %w", err)
		}
		return ctrl.Result{Requeue: true}, true, nil
	}
	effective := rollout.Status.EffectivePolicy
	if effective.Epoch < 1 || effective.Policy.UID != frozen.UID || effective.Policy.LedgerUID != frozen.LedgerUID ||
		effective.Policy.StructuralHash != frozen.StructuralHash {
		return ctrl.Result{}, true, fmt.Errorf("published effective policy epoch is invalid")
	}

	if transition := rollout.Status.PolicyTransition; transition != nil {
		if transition.Epoch != effective.Epoch+1 || transition.Policy.UID != effective.Policy.UID ||
			transition.Policy.LedgerUID != effective.Policy.LedgerUID || transition.Policy.StructuralHash != effective.Policy.StructuralHash {
			return ctrl.Result{}, true, fmt.Errorf("persisted policy transition identity is invalid")
		}
		strictest, err := strictestPolicySnapshot(effective.Policy, frozen, transition.Policy)
		if err != nil || !reflect.DeepEqual(strictest, transition.Policy) {
			return ctrl.Result{}, true, fmt.Errorf("persisted policy transition weakens an effective ceiling")
		}
		if err := r.ensurePolicyEpochFences(ctx, rollout, now); err != nil {
			return ctrl.Result{RequeueAfter: rolloutPollInterval}, true, err
		}
		before := rollout.DeepCopy()
		rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
			Epoch: transition.Epoch, Policy: transition.Policy, UpdatedAt: metav1.NewTime(now),
		}
		rollout.Status.PolicyTransition = nil
		setRolloutCondition(rollout, "PolicyTransition", metav1.ConditionFalse, "EpochPublished",
			fmt.Sprintf("administrator policy epoch %d is effective", transition.Epoch), now)
		if err := r.Status().Patch(ctx, rollout,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, true, fmt.Errorf("publish effective policy epoch: %w", err)
		}
		return ctrl.Result{Requeue: true}, true, nil
	}

	if currentSnapshot.SemanticHash == effective.Policy.SemanticHash {
		if currentSnapshot.ResourceVersion == effective.Policy.ResourceVersion {
			return ctrl.Result{}, false, nil
		}
		// Metadata-only churn changes no admission semantics and therefore does
		// not invalidate acknowledgements or reservations in this epoch.
		before := rollout.DeepCopy()
		rollout.Status.EffectivePolicy.Policy.ResourceVersion = currentSnapshot.ResourceVersion
		rollout.Status.EffectivePolicy.UpdatedAt = metav1.NewTime(now)
		if err := r.Status().Patch(ctx, rollout,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, true, fmt.Errorf("record metadata-only policy update: %w", err)
		}
		return ctrl.Result{Requeue: true}, true, nil
	}

	next, err := strictestPolicySnapshot(effective.Policy, frozen, currentSnapshot)
	if err != nil {
		result, reconcileErr := r.reconcilePolicyChanged(ctx, rollout, current, now)
		return result, true, errors.Join(err, reconcileErr)
	}
	if len(rollout.Status.FrozenPlan.Targets) > int(next.MaxTargets) {
		result, err := r.reconcilePolicyChanged(ctx, rollout, current, now)
		return result, true, err
	}
	before := rollout.DeepCopy()
	rollout.Status.PolicyTransition = &opsv1alpha1.IOSXESoftwareRolloutPolicyTransitionStatus{
		Epoch: effective.Epoch + 1, Policy: next, StartedAt: metav1.NewTime(now),
	}
	rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhasePaused
	rollout.Status.Message = fmt.Sprintf("administrator policy transition to epoch %d is fencing unclaimed work", effective.Epoch+1)
	setRolloutCondition(rollout, "PolicyTransition", metav1.ConditionTrue, "FencingPreviousEpoch", rollout.Status.Message, now)
	if err := r.Status().Patch(ctx, rollout,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("start administrator policy transition: %w", err)
	}
	return ctrl.Result{Requeue: true}, true, nil
}

func validateFrozenRequiredTopologyKeys(
	targets []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	currentKeys []string,
) error {
	if len(targets) == 0 {
		return fmt.Errorf("approved plan has no frozen targets")
	}
	current := make(map[string]struct{}, len(currentKeys))
	for _, key := range currentKeys {
		current[key] = struct{}{}
	}
	for _, target := range targets {
		frozen := make(map[string]struct{}, len(target.Topology))
		for _, value := range target.Topology {
			if _, duplicate := frozen[value.Key]; duplicate {
				return fmt.Errorf("approved target %q contains duplicate topology key %q", target.DeviceName, value.Key)
			}
			frozen[value.Key] = struct{}{}
		}
		if len(frozen) != len(current) {
			return fmt.Errorf("administrator required-topology set changed after approval")
		}
		for key := range current {
			if _, ok := frozen[key]; !ok {
				return fmt.Errorf("administrator required topology %q was not included in approved target %q", key, target.DeviceName)
			}
		}
	}
	return nil
}

func minPositive(values ...int) int {
	minimum := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if minimum == 0 || value < minimum {
			minimum = value
		}
	}
	return minimum
}

func minInt(values ...int) int { return minPositive(values...) }

func defaultInt32(value, fallback int32) int32 {
	if value > 0 {
		return value
	}
	return fallback
}

func (r *IOSXESoftwareRolloutReconciler) verifyFrozenSource(ctx context.Context, rollout *opsv1alpha1.IOSXESoftwareRollout) error {
	source := rollout.Status.FrozenPlan.Source
	if source.SecretName == "" {
		if source.SecretUID != "" {
			return fmt.Errorf("frozen source has a Secret identity without a name")
		}
		return nil
	}
	if source.SecretUID == "" {
		return fmt.Errorf("frozen source Secret identity is incomplete")
	}
	var secret corev1.Secret
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: source.SecretName}, &secret); err != nil {
		return fmt.Errorf("read frozen source Secret: %w", err)
	}
	if string(secret.UID) != source.SecretUID {
		return fmt.Errorf("source Secret incarnation changed")
	}
	if err := softwareupgrade.ValidateURLSecretEndpoint(&secret, source.URL); err != nil {
		return fmt.Errorf("source Secret endpoint authorization changed: %w", err)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) rolloutChildren(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
) (map[string]opsv1alpha1.IOSXESoftwareUpgrade, error) {
	var list opsv1alpha1.IOSXESoftwareUpgradeList
	if err := r.reader().List(ctx, &list,
		client.InNamespace(rollout.Namespace),
		client.MatchingLabels{managedprotocol.AnnotationCampaignUID: string(rollout.UID)},
	); err != nil {
		return nil, fmt.Errorf("list rollout leaves: %w", err)
	}
	if len(list.Items) > len(rollout.Status.FrozenPlan.Targets) {
		return nil, fmt.Errorf("campaign has %d leaves for %d frozen targets", len(list.Items), len(rollout.Status.FrozenPlan.Targets))
	}
	expectedNames := make(map[string]struct{}, len(rollout.Status.FrozenPlan.Targets))
	for _, target := range rollout.Status.FrozenPlan.Targets {
		expectedNames[target.ChildName] = struct{}{}
	}
	children := make(map[string]opsv1alpha1.IOSXESoftwareUpgrade, len(list.Items))
	for i := range list.Items {
		item := list.Items[i]
		if _, expected := expectedNames[item.Name]; !expected {
			return nil, fmt.Errorf("campaign UID label appears on unexpected leaf %s/%s", item.Namespace, item.Name)
		}
		if _, duplicate := children[item.Name]; duplicate {
			return nil, fmt.Errorf("duplicate leaf name %q", item.Name)
		}
		children[item.Name] = item
	}
	return children, nil
}

func expectedLeafAnnotations(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	workerUsername string,
) map[string]string {
	annotations := map[string]string{
		managedprotocol.AnnotationManaged:           "true",
		managedprotocol.AnnotationCampaignNamespace: rollout.Namespace,
		managedprotocol.AnnotationCampaignName:      rollout.Name,
		managedprotocol.AnnotationCampaignUID:       string(rollout.UID),
		managedprotocol.AnnotationPlanHash:          rollout.Status.FrozenPlan.Hash,
		managedprotocol.AnnotationLedgerUID:         rollout.Status.FrozenPlan.Policy.LedgerUID,
		managedprotocol.AnnotationReservationID:     reservationID(string(rollout.UID), target.DeviceUID),
		managedprotocol.AnnotationDeviceNamespace:   rollout.Namespace,
		managedprotocol.AnnotationDeviceName:        target.DeviceName,
		managedprotocol.AnnotationDeviceUID:         target.DeviceUID,
		managedprotocol.AnnotationDeviceGeneration:  strconv.FormatInt(target.DeviceGeneration, 10),
		managedprotocol.AnnotationNodeName:          target.NodeName,
		managedprotocol.AnnotationNodeUID:           target.NodeUID,
		managedprotocol.AnnotationWorkerUsername:    workerUsername,
		managedprotocol.AnnotationWorkerProtocol:    managedprotocol.Version,
	}
	if rollout.Status.FrozenPlan.Source.SecretUID != "" {
		annotations[managedprotocol.AnnotationSourceSecretUID] = rollout.Status.FrozenPlan.Source.SecretUID
	}
	return annotations
}

func rolloutLeafAnnotations(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	workerUsername string,
	now time.Time,
) map[string]string {
	annotations := expectedLeafAnnotations(rollout, target, workerUsername)
	for key, value := range correlation.SanitizedAnnotationsAt(rollout.Annotations, now) {
		annotations[key] = value
	}
	return annotations
}

func validateManagedLeafBinding(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if leaf == nil || leaf.Name != target.ChildName || leaf.Namespace != rollout.Namespace || leaf.UID == "" {
		return fmt.Errorf("leaf %q has incomplete Kubernetes identity", target.ChildName)
	}
	if strings.TrimSpace(leaf.Annotations[managedprotocol.AnnotationWorkerUsername]) == "" {
		return fmt.Errorf("leaf %s/%s has no bound worker username", leaf.Namespace, leaf.Name)
	}
	for key, value := range expectedLeafAnnotations(rollout, target, leaf.Annotations[managedprotocol.AnnotationWorkerUsername]) {
		if leaf.Annotations[key] != value {
			return fmt.Errorf("leaf %s/%s annotation %q does not match the frozen binding", leaf.Namespace, leaf.Name, key)
		}
	}
	if leaf.Labels[managedprotocol.AnnotationCampaignUID] != string(rollout.UID) {
		return fmt.Errorf("leaf %s/%s campaign label does not match", leaf.Namespace, leaf.Name)
	}
	if leaf.Status.ManagerAdmission != nil && leaf.Status.ManagerAdmission.LeafUID != "" && leaf.Status.ManagerAdmission.LeafUID != string(leaf.UID) {
		return fmt.Errorf("leaf %s/%s manager admission names a different UID", leaf.Namespace, leaf.Name)
	}
	return nil
}

func expectedLeafSpec(rollout *opsv1alpha1.IOSXESoftwareRollout, target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) opsv1alpha1.IOSXESoftwareUpgradeSpec {
	source := rollout.Status.FrozenPlan.Source
	imageSource := opsv1alpha1.UpgradeImageSource{URL: source.URL, SHA256: source.SHA256}
	if source.SecretName != "" {
		imageSource.URLSecretRef = &corev1.LocalObjectReference{Name: source.SecretName}
	}
	rollback := rollout.Spec.Plan.RollbackOnFailure == nil || *rollout.Spec.Plan.RollbackOnFailure
	return opsv1alpha1.IOSXESoftwareUpgradeSpec{
		DeviceRef:             configv1alpha1.DeviceRef{Name: target.DeviceName},
		ImageSource:           imageSource,
		TargetVersion:         rollout.Spec.Plan.TargetVersion,
		Strategy:              opsv1alpha1.UpgradeStrategyReload,
		RollbackOnFailure:     &rollback,
		MaintenanceWindow:     rollout.Spec.Plan.MaintenanceWindow.DeepCopy(),
		InstallTimeoutSeconds: defaultInt32(rollout.Spec.Plan.InstallTimeoutSeconds, 3600),
		RebootTimeoutSeconds:  defaultInt32(rollout.Spec.Plan.RebootTimeoutSeconds, 1800),
	}
}

func (r *IOSXESoftwareRolloutReconciler) currentWorkerUsername(ctx context.Context, target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) (string, error) {
	var node corev1.Node
	if err := r.reader().Get(ctx, types.NamespacedName{Name: target.NodeName}, &node); err != nil {
		return "", fmt.Errorf("read target Node: %w", err)
	}
	if string(node.UID) != target.NodeUID || node.Annotations[managedprotocol.AnnotationWorkerProtocol] != managedprotocol.Version {
		return "", fmt.Errorf("target Node incarnation or managed protocol changed")
	}
	username := node.Annotations[managedprotocol.AnnotationWorkerUsername]
	if username == "" {
		return "", fmt.Errorf("target Node has no bound worker username")
	}
	return username, nil
}

func campaignDomainLimits(rollout *opsv1alpha1.IOSXESoftwareRollout, policy topologyrollout.Policy) (map[string]int, map[string]int, error) {
	transfers := map[string]int{}
	unavailable := map[string]int{}
	for _, domain := range rollout.Spec.Plan.Budgets.Domains {
		if _, ok := policy.DomainTransferBudgets[domain.TopologyKey]; !ok && domain.MaxConcurrentTransfers != nil {
			return nil, nil, fmt.Errorf("campaign transfer domain %q is not administrator-controlled", domain.TopologyKey)
		}
		if _, ok := policy.DomainBudgets[domain.TopologyKey]; !ok && domain.MaxUnavailable != nil {
			return nil, nil, fmt.Errorf("campaign unavailable domain %q is not administrator-controlled", domain.TopologyKey)
		}
		if domain.MaxConcurrentTransfers != nil {
			transfers[domain.TopologyKey] = int(*domain.MaxConcurrentTransfers)
		}
		if domain.MaxUnavailable != nil {
			unavailable[domain.TopologyKey] = int(*domain.MaxUnavailable)
		}
	}
	return transfers, unavailable, nil
}

func targetDomains(target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) map[string]string {
	out := make(map[string]string, len(target.Topology))
	for _, topology := range target.Topology {
		out[topology.Key] = topology.Value
	}
	return out
}

func (r *IOSXESoftwareRolloutReconciler) ledgerStore(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
) topologyrollout.Store {
	frozen := rollout.Status.FrozenPlan.Policy
	return topologyrollout.Store{
		Client: r.Client, APIReader: r.reader(),
		Key: types.NamespacedName{Namespace: frozen.LedgerNamespace, Name: frozen.LedgerName}, ExpectedUID: types.UID(frozen.LedgerUID),
	}
}

func (r *IOSXESoftwareRolloutReconciler) admitTarget(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	now time.Time,
) error {
	_, workerUsername, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
	if err != nil {
		return err
	}
	if err := r.ensureNoRunningWorkloads(ctx, target.NodeName); err != nil {
		return err
	}
	if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
		return err
	}
	request, err := rolloutReservationRequest(rollout, effectivePolicy, target)
	if err != nil {
		return err
	}
	lockID, err := r.acquireDeviceTopologyLock(ctx, rollout, target, effectivePolicy.Epoch, now)
	if err != nil {
		return err
	}
	request.TopologyLockID = lockID
	store := r.ledgerStore(rollout)
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		// Store.Mutate reruns this callback after every ConfigMap CAS conflict.
		// Refresh all cross-object admission evidence on each attempt instead of
		// authorizing from a stale health/control snapshot.
		members, currentWorker, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
		if err != nil {
			return err
		}
		if currentWorker != workerUsername {
			return fmt.Errorf("target worker identity changed during reservation")
		}
		if err := r.ensureNoRunningWorkloads(ctx, target.NodeName); err != nil {
			return err
		}
		if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
			return err
		}
		if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, effectivePolicy.Epoch, lockID); err != nil {
			return err
		}
		return topologyrollout.Reserve(ledger, effectivePolicy, members, request)
	}); err != nil {
		return errors.Join(err, r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, "",
			uint64(rollout.Spec.Control.Revision), effectivePolicy.Epoch, lockID))
	}
	if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, effectivePolicy.Epoch, lockID); err != nil {
		return errors.Join(err, r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, "",
			uint64(rollout.Spec.Control.Revision), effectivePolicy.Epoch, lockID))
	}
	// A transition may win immediately after the ledger CAS. Re-read the
	// campaign before creating any child, and remove only this epoch's still-
	// unbound reservation if execution authority changed.
	if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
		releaseErr := r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, "",
			uint64(rollout.Spec.Control.Revision), effectivePolicy.Epoch, lockID)
		return errors.Join(err, releaseErr)
	}

	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace, Name: target.ChildName,
			Labels:      map[string]string{managedprotocol.AnnotationCampaignUID: string(rollout.UID)},
			Annotations: rolloutLeafAnnotations(rollout, target, workerUsername, now),
		},
		Spec: expectedLeafSpec(rollout, target),
	}
	if err := r.Create(ctx, leaf); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create retained rollout leaf %s/%s: %w", leaf.Namespace, leaf.Name, err)
		}
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(leaf), leaf); err != nil {
			return fmt.Errorf("read existing rollout leaf: %w", err)
		}
	}
	if err := validateManagedLeafBinding(rollout, target, leaf); err != nil {
		return err
	}
	if leaf.Annotations[managedprotocol.AnnotationWorkerUsername] != workerUsername {
		return fmt.Errorf("existing leaf %s/%s is bound to a different worker username", leaf.Namespace, leaf.Name)
	}
	if !reflect.DeepEqual(leaf.Spec, expectedLeafSpec(rollout, target)) {
		return fmt.Errorf("leaf %s/%s spec does not equal the immutable frozen target", leaf.Namespace, leaf.Name)
	}
	if leaf.Status.ManagerAdmission != nil &&
		(leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked ||
			leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled) {
		if len(leaf.Status.ManagedMutationClaims) == 0 {
			revision := uint64(rollout.Spec.Control.Revision)
			if leaf.Status.ManagerAdmission.ControlRevision != nil && *leaf.Status.ManagerAdmission.ControlRevision >= 0 &&
				uint64(*leaf.Status.ManagerAdmission.ControlRevision) > revision {
				revision = uint64(*leaf.Status.ManagerAdmission.ControlRevision)
			}
			if err := r.releaseUnclaimedReservation(ctx, rollout, target, string(leaf.UID), revision,
				leaf.Status.ManagerAdmission.TopologyLockID); err != nil {
				return err
			}
		}
		return topologyrollout.ErrStaleControlRevision
	}
	return r.ensureChildAdmission(ctx, rollout, target, leaf, now)
}

func rolloutReservationRequest(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) (topologyrollout.ReservationRequest, error) {
	transferLimits, unavailableLimits, err := campaignDomainLimits(rollout, effectivePolicy)
	if err != nil {
		return topologyrollout.ReservationRequest{}, err
	}
	return topologyrollout.ReservationRequest{
		ID: reservationID(string(rollout.UID), target.DeviceUID), CampaignUID: string(rollout.UID),
		PlanHash: rollout.Status.FrozenPlan.Hash, PolicyUID: effectivePolicy.UID,
		PolicyVersion: effectivePolicy.Version, PolicyEpoch: effectivePolicy.Epoch,
		PhysicalID: target.PhysicalIdentity, DeviceUID: target.DeviceUID, NodeUID: target.NodeUID,
		ChildNamespace: rollout.Namespace, ChildName: target.ChildName, Domains: targetDomains(target),
		CampaignMaxConcurrentTransfers: int(defaultInt32(rollout.Spec.Plan.Budgets.MaxConcurrentTransfers, 1)),
		CampaignMaxUnavailable:         int(defaultInt32(rollout.Spec.Plan.Budgets.MaxUnavailable, 1)),
		CampaignTransferLimits:         transferLimits, CampaignLimits: unavailableLimits,
		ControlRevision: uint64(rollout.Spec.Control.Revision),
	}, nil
}

func (r *IOSXESoftwareRolloutReconciler) rearmPolicyEpochLeaf(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) error {
	if rollout.Status.PolicyTransition != nil || rollout.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseFailed ||
		rollout.Spec.Control.Pause || rollout.Spec.Control.Cancel || leaf == nil || leaf.Status.ManagerAdmission == nil ||
		leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
		leaf.Status.ManagerAdmission.RevocationReason != "PolicyEpochTransition" ||
		leaf.Status.ManagerAdmission.PolicyEpoch >= effectivePolicy.Epoch || len(leaf.Status.ManagedMutationClaims) != 0 {
		return topologyrollout.ErrStaleControlRevision
	}
	if err := validateManagerAdmission(rollout, target, leaf); err != nil {
		return err
	}
	members, workerUsername, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
	if err != nil {
		return err
	}
	if leaf.Annotations[managedprotocol.AnnotationWorkerUsername] != workerUsername {
		return fmt.Errorf("retained leaf worker identity changed before policy-epoch rearm")
	}
	if err := r.ensureNoRunningWorkloads(ctx, target.NodeName); err != nil {
		return err
	}
	if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
		return err
	}
	request, err := rolloutReservationRequest(rollout, effectivePolicy, target)
	if err != nil {
		return err
	}
	lockID, err := r.acquireDeviceTopologyLock(ctx, rollout, target, effectivePolicy.Epoch, now)
	if err != nil {
		return err
	}
	request.TopologyLockID = lockID
	store := r.ledgerStore(rollout)
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		var currentLeaf opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
			return err
		}
		if currentLeaf.UID != leaf.UID || currentLeaf.Status.ManagerAdmission == nil ||
			currentLeaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
			currentLeaf.Status.ManagerAdmission.RevocationReason != "PolicyEpochTransition" ||
			currentLeaf.Status.ManagerAdmission.PolicyEpoch >= effectivePolicy.Epoch ||
			len(currentLeaf.Status.ManagedMutationClaims) != 0 {
			return topologyrollout.ErrStaleControlRevision
		}
		freshMembers, currentWorker, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
		if err != nil {
			return err
		}
		if currentWorker != workerUsername {
			return fmt.Errorf("worker identity changed during policy-epoch reservation")
		}
		if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, effectivePolicy.Epoch, lockID); err != nil {
			return err
		}
		members = freshMembers
		return topologyrollout.Reserve(ledger, effectivePolicy, members, request)
	}); err != nil {
		return errors.Join(err, r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, string(leaf.UID),
			uint64(rollout.Spec.Control.Revision), effectivePolicy.Epoch, lockID))
	}
	cleanup := func(cause error) error {
		releaseErr := r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, string(leaf.UID),
			uint64(rollout.Spec.Control.Revision), effectivePolicy.Epoch, lockID)
		return errors.Join(cause, releaseErr)
	}
	if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, effectivePolicy.Epoch, lockID); err != nil {
		return cleanup(err)
	}
	if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
		return cleanup(err)
	}
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.BindChild(ledger, request.ID, string(leaf.UID))
	}); err != nil {
		return cleanup(err)
	}
	if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID || current.Status.ManagerAdmission == nil ||
			current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
			current.Status.ManagerAdmission.RevocationReason != "PolicyEpochTransition" ||
			current.Status.ManagerAdmission.PolicyEpoch >= effectivePolicy.Epoch || len(current.Status.ManagedMutationClaims) != 0 {
			return topologyrollout.ErrStaleControlRevision
		}
		if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
			return err
		}
		current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionPending
		current.Status.ManagerAdmission.PolicyResourceVersion = rollout.Status.EffectivePolicy.Policy.ResourceVersion
		current.Status.ManagerAdmission.PolicyEpoch = effectivePolicy.Epoch
		current.Status.ManagerAdmission.TopologyLockID = lockID
		current.Status.ManagerAdmission.RevocationReason = ""
		current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
		return nil
	}); err != nil {
		return cleanup(err)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) ensureChildAdmission(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) error {
	policySnapshot, policyEpoch, err := rolloutEffectivePolicyBinding(rollout)
	if err != nil {
		return err
	}
	reservation := reservationID(string(rollout.UID), target.DeviceUID)
	store := r.ledgerStore(rollout)
	topologyLockID := ""
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		current, ok := ledger.Reservations[reservation]
		if !ok || current.PolicyEpoch != policyEpoch || current.TopologyLockID == "" {
			return fmt.Errorf("%w: reservation has no matching topology-lock acquisition", topologyrollout.ErrLedgerIdentity)
		}
		topologyLockID = current.TopologyLockID
		return topologyrollout.BindChild(ledger, reservation, string(leaf.UID))
	}); err != nil {
		return fmt.Errorf("bind rollout leaf to reservation: %w", err)
	}
	return r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID {
			return fmt.Errorf("leaf incarnation changed while binding admission")
		}
		if current.Status.ManagerAdmission != nil {
			return validateManagerAdmission(rollout, target, current)
		}
		revision := rollout.Spec.Control.Revision
		current.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
			ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
			State:           opsv1alpha1.UpgradeManagerAdmissionPending,
			CampaignUID:     string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
			PolicyUID:             policySnapshot.UID,
			PolicyResourceVersion: policySnapshot.ResourceVersion,
			PolicyEpoch:           policyEpoch,
			LedgerUID:             rollout.Status.FrozenPlan.Policy.LedgerUID,
			ReservationID:         reservation,
			TopologyLockID:        topologyLockID,
			LeafUID:               string(current.UID), DeviceUID: target.DeviceUID, DeviceGeneration: target.DeviceGeneration,
			PhysicalIdentity: target.PhysicalIdentity, NodeUID: target.NodeUID,
			ControlRevision: &revision, UpdatedAt: metav1.NewTime(now),
		}
		current.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
			Revision: revision, Pause: rollout.Spec.Control.Pause, Cancel: rollout.Spec.Control.Cancel,
			UpdatedAt: metav1.NewTime(now), Reason: "CampaignControl",
		}
		return nil
	})
}

func validateManagerAdmission(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	policySnapshot, policyEpoch, err := rolloutEffectivePolicyBinding(rollout)
	if err != nil {
		return err
	}
	admission := leaf.Status.ManagerAdmission
	if admission == nil || admission.ProtocolVersion != opsv1alpha1.ManagedUpgradeProtocolRolloutV1 ||
		admission.CampaignUID != string(rollout.UID) || admission.PlanHash != rollout.Status.FrozenPlan.Hash ||
		admission.PolicyUID != rollout.Status.FrozenPlan.Policy.UID ||
		strings.TrimSpace(admission.PolicyResourceVersion) == "" || admission.PolicyEpoch < 1 || admission.PolicyEpoch > policyEpoch ||
		admission.LedgerUID != rollout.Status.FrozenPlan.Policy.LedgerUID ||
		admission.ReservationID != reservationID(string(rollout.UID), target.DeviceUID) ||
		(admission.TopologyLockID != "" && !topologyLockAcquisitionIDValid(admission.TopologyLockID)) ||
		((admission.State == opsv1alpha1.UpgradeManagerAdmissionPending ||
			admission.State == opsv1alpha1.UpgradeManagerAdmissionGranted) && admission.TopologyLockID == "") ||
		admission.LeafUID != string(leaf.UID) || admission.DeviceUID != target.DeviceUID ||
		admission.DeviceGeneration != target.DeviceGeneration ||
		admission.PhysicalIdentity != target.PhysicalIdentity || admission.NodeUID != target.NodeUID ||
		admission.ControlRevision == nil {
		return fmt.Errorf("leaf %s/%s manager admission does not match the approved identity binding", leaf.Namespace, leaf.Name)
	}
	if admission.PolicyUID != policySnapshot.UID ||
		(admission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked) != (admission.RevocationReason != "") {
		return fmt.Errorf("leaf %s/%s has an invalid policy-epoch revocation binding", leaf.Namespace, leaf.Name)
	}
	if admission.PolicyEpoch < policyEpoch && len(leaf.Status.ManagedMutationClaims) == 0 &&
		(admission.State == opsv1alpha1.UpgradeManagerAdmissionPending || admission.State == opsv1alpha1.UpgradeManagerAdmissionGranted) {
		return fmt.Errorf("leaf %s/%s retains unclaimed authority from stale policy epoch %d", leaf.Namespace, leaf.Name, admission.PolicyEpoch)
	}
	for _, claim := range leaf.Status.ManagedMutationClaims {
		if claim.PolicyEpoch != admission.PolicyEpoch {
			return fmt.Errorf("leaf %s/%s claim policy epoch does not match its admission", leaf.Namespace, leaf.Name)
		}
	}
	return nil
}

func rolloutEffectivePolicyBinding(rollout *opsv1alpha1.IOSXESoftwareRollout) (opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot, int64, error) {
	if rollout == nil || rollout.Status.FrozenPlan == nil || rollout.Status.EffectivePolicy == nil || rollout.Status.EffectivePolicy.Epoch < 1 {
		return opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{}, 0, fmt.Errorf("rollout has no published effective policy epoch")
	}
	return rollout.Status.EffectivePolicy.Policy, rollout.Status.EffectivePolicy.Epoch, nil
}

func (r *IOSXESoftwareRolloutReconciler) patchLeafManagerFields(
	ctx context.Context,
	key types.NamespacedName,
	mutate func(*opsv1alpha1.IOSXESoftwareUpgrade) error,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var current opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.reader().Get(ctx, key, &current); err != nil {
			return err
		}
		before := current.DeepCopy()
		if err := mutate(&current); err != nil {
			return err
		}
		if reflect.DeepEqual(before.Status.ManagerAdmission, current.Status.ManagerAdmission) &&
			reflect.DeepEqual(before.Status.ManagerControl, current.Status.ManagerControl) {
			return nil
		}
		if err := r.Status().Patch(ctx, &current,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			if apierrors.IsConflict(err) {
				return err
			}
			return fmt.Errorf("patch manager-owned leaf status: %w", err)
		}
		return nil
	})
}

func (r *IOSXESoftwareRolloutReconciler) tryGrantLeaf(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (bool, error) {
	if err := validateManagerAdmission(rollout, target, leaf); err != nil {
		return false, err
	}
	if leaf.Status.ManagerAdmission.PolicyEpoch != effectivePolicy.Epoch ||
		leaf.Status.ManagerAdmission.RevocationReason != "" {
		return false, nil
	}
	control := leaf.Status.WorkerControl
	if control == nil || control.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
		control.ObservedPolicyEpoch != effectivePolicy.Epoch ||
		control.ObservedControlRevision != rollout.Spec.Control.Revision ||
		control.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
		return false, nil
	}
	managerControl := leaf.Status.ManagerControl
	if managerControl == nil || managerControl.Revision != rollout.Spec.Control.Revision ||
		managerControl.Pause || managerControl.Cancel {
		return false, nil
	}
	if rollout.Spec.Control.Pause || rollout.Spec.Control.Cancel {
		return false, nil
	}
	_, workerUsername, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
	if err != nil {
		return false, err
	}
	workerRevision, err := r.requireWorkerRevisionAcknowledgement(ctx, rollout, target, control)
	if err != nil {
		return false, err
	}
	if leaf.Annotations[managedprotocol.AnnotationWorkerUsername] != workerUsername {
		return false, fmt.Errorf("leaf worker username no longer matches the bound Node")
	}
	if err := r.ensureNoRunningWorkloads(ctx, target.NodeName); err != nil {
		return false, err
	}
	if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
		return false, err
	}
	store := r.ledgerStore(rollout)
	reservation := reservationID(string(rollout.UID), target.DeviceUID)
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		var currentLeaf opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
			return fmt.Errorf("re-read leaf before ledger grant: %w", err)
		}
		if currentLeaf.UID != leaf.UID {
			return fmt.Errorf("leaf incarnation changed before ledger grant")
		}
		if err := validateManagerAdmission(rollout, target, &currentLeaf); err != nil {
			return err
		}
		if currentLeaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionPending ||
			currentLeaf.Status.ManagerAdmission.PolicyEpoch != effectivePolicy.Epoch ||
			currentLeaf.Status.WorkerControl == nil ||
			currentLeaf.Status.WorkerControl.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
			currentLeaf.Status.WorkerControl.ObservedPolicyEpoch != effectivePolicy.Epoch ||
			currentLeaf.Status.WorkerControl.ObservedControlRevision != rollout.Spec.Control.Revision ||
			currentLeaf.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady ||
			currentLeaf.Status.ManagerControl == nil ||
			currentLeaf.Status.ManagerControl.Revision != rollout.Spec.Control.Revision ||
			currentLeaf.Status.ManagerControl.Pause || currentLeaf.Status.ManagerControl.Cancel {
			return fmt.Errorf("leaf grant handshake changed before ledger grant")
		}
		_, currentWorker, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
		if err != nil {
			return err
		}
		if currentWorker != workerUsername || currentLeaf.Annotations[managedprotocol.AnnotationWorkerUsername] != currentWorker {
			return fmt.Errorf("leaf worker identity changed before ledger grant")
		}
		currentWorkerRevision, err := r.requireWorkerRevisionAcknowledgement(
			ctx, rollout, target, currentLeaf.Status.WorkerControl,
		)
		if err != nil {
			return err
		}
		if currentWorkerRevision != workerRevision {
			return fmt.Errorf("worker configuration revision changed before ledger grant")
		}
		if err := r.ensureNoRunningWorkloads(ctx, target.NodeName); err != nil {
			return err
		}
		if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
			return err
		}
		admission := currentLeaf.Status.ManagerAdmission
		currentReservation, ok := ledger.Reservations[reservation]
		if !ok || currentReservation.PolicyEpoch != admission.PolicyEpoch ||
			currentReservation.TopologyLockID != admission.TopologyLockID {
			return fmt.Errorf("%w: leaf admission no longer matches its topology-lock reservation", topologyrollout.ErrLedgerIdentity)
		}
		if err := r.revalidateDeviceTopologyLock(ctx, rollout, target,
			admission.PolicyEpoch, admission.TopologyLockID); err != nil {
			return err
		}
		return topologyrollout.Grant(ledger, reservation, rollout.Status.FrozenPlan.Policy.LedgerUID,
			string(leaf.UID), uint64(rollout.Spec.Control.Revision))
	}); err != nil {
		return false, fmt.Errorf("grant rollout reservation: %w", err)
	}
	if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		_, currentWorker, err := r.revalidateAdmission(ctx, rollout, currentPolicy, effectivePolicy, target)
		if err != nil {
			return err
		}
		if currentWorker != workerUsername || current.Annotations[managedprotocol.AnnotationWorkerUsername] != currentWorker {
			return fmt.Errorf("leaf worker identity changed before admission grant")
		}
		currentWorkerRevision, err := r.requireWorkerRevisionAcknowledgement(
			ctx, rollout, target, current.Status.WorkerControl,
		)
		if err != nil {
			return err
		}
		if currentWorkerRevision != workerRevision {
			return fmt.Errorf("worker configuration revision changed before admission grant")
		}
		if err := r.ensureNoRunningWorkloads(ctx, target.NodeName); err != nil {
			return err
		}
		if err := r.revalidateCampaignExecution(ctx, rollout); err != nil {
			return err
		}
		if err := validateManagerAdmission(rollout, target, current); err != nil {
			return err
		}
		if current.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionGranted {
			return nil
		}
		if current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionPending {
			return fmt.Errorf("cannot grant leaf admission from %s", current.Status.ManagerAdmission.State)
		}
		if current.Status.WorkerControl == nil ||
			current.Status.WorkerControl.ObservedAdmissionState != opsv1alpha1.UpgradeManagerAdmissionPending ||
			current.Status.WorkerControl.ObservedPolicyEpoch != effectivePolicy.Epoch ||
			current.Status.WorkerControl.ObservedControlRevision != rollout.Spec.Control.Revision ||
			current.Status.WorkerControl.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady {
			return fmt.Errorf("worker acknowledgement changed before grant")
		}
		if current.Status.ManagerControl == nil || current.Status.ManagerControl.Revision != rollout.Spec.Control.Revision ||
			current.Status.ManagerControl.Pause || current.Status.ManagerControl.Cancel {
			return fmt.Errorf("manager control changed before grant")
		}
		revision := rollout.Spec.Control.Revision
		current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionGranted
		current.Status.ManagerAdmission.ControlRevision = &revision
		current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
		return nil
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (r *IOSXESoftwareRolloutReconciler) revalidateCampaignExecution(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
) error {
	if rollout == nil || rollout.UID == "" || rollout.Status.FrozenPlan == nil || rollout.Spec.Control.Revision < 0 {
		return fmt.Errorf("campaign execution identity is incomplete")
	}
	var current opsv1alpha1.IOSXESoftwareRollout
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
		return fmt.Errorf("re-read campaign execution control: %w", err)
	}
	if current.UID != rollout.UID || current.Status.FrozenPlan == nil ||
		current.Status.FrozenPlan.Hash != rollout.Status.FrozenPlan.Hash ||
		current.Spec.Approval == nil || current.Spec.Approval.PlanHash != rollout.Status.FrozenPlan.Hash {
		return fmt.Errorf("campaign identity, plan, or approval changed before admission")
	}
	if current.Status.PolicyTransition != nil || current.Status.EffectivePolicy == nil || rollout.Status.EffectivePolicy == nil ||
		current.Status.EffectivePolicy.Epoch != rollout.Status.EffectivePolicy.Epoch ||
		current.Status.EffectivePolicy.Policy.SemanticHash != rollout.Status.EffectivePolicy.Policy.SemanticHash {
		return fmt.Errorf("campaign policy epoch changed or is transitioning before admission")
	}
	if current.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseFailed ||
		current.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling ||
		current.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled ||
		current.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded {
		return fmt.Errorf("campaign phase %s does not permit admission", current.Status.Phase)
	}
	if !reflect.DeepEqual(current.Spec.Control, rollout.Spec.Control) || current.Spec.Control.Pause || current.Spec.Control.Cancel {
		return fmt.Errorf("campaign control changed before admission")
	}
	children, err := r.rolloutChildren(ctx, &current)
	if err != nil {
		return fmt.Errorf("re-read campaign leaves before admission: %w", err)
	}
	for i := range children {
		leaf := children[i]
		if terminalLeafFailure(leaf.Status.Phase) {
			return fmt.Errorf("campaign leaf %s/%s is terminal in phase %s; admission is fenced",
				leaf.Namespace, leaf.Name, leaf.Status.Phase)
		}
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) revalidatePolicyIdentity(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
) error {
	if rollout == nil || rollout.Status.FrozenPlan == nil || rollout.Status.EffectivePolicy == nil {
		return fmt.Errorf("campaign has no frozen policy identity")
	}
	frozen := rollout.Status.FrozenPlan.Policy
	var policy corev1.ConfigMap
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: frozen.Namespace, Name: frozen.Name}, &policy); err != nil {
		return fmt.Errorf("re-read administrator policy identity: %w", err)
	}
	parsed, err := topologyrollout.ParseAdminPolicy(&policy)
	if err != nil {
		return fmt.Errorf("re-parse administrator policy before admission: %w", err)
	}
	snapshot, err := freezePolicy(parsed)
	if err != nil {
		return err
	}
	effective := rollout.Status.EffectivePolicy.Policy
	if snapshot.UID != frozen.UID || snapshot.LedgerUID != frozen.LedgerUID ||
		snapshot.StructuralHash != effective.StructuralHash || snapshot.SemanticHash != effective.SemanticHash {
		return fmt.Errorf("administrator policy semantics changed before admission")
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) revalidateAdmission(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) ([]topologyrollout.Member, string, error) {
	if err := r.revalidatePolicyIdentity(ctx, rollout); err != nil {
		return nil, "", err
	}
	if err := r.verifyFrozenSource(ctx, rollout); err != nil {
		return nil, "", err
	}
	if err := r.revalidateFrozenTarget(ctx, rollout, currentPolicy, target); err != nil {
		return nil, "", err
	}
	members, workers, err := r.currentFleetMembers(ctx, rollout, currentPolicy, effectivePolicy)
	if err != nil {
		return nil, "", err
	}
	var found bool
	for _, member := range members {
		if member.PhysicalID != target.PhysicalIdentity {
			continue
		}
		if member.DeviceUID != target.DeviceUID || member.NodeUID != target.NodeUID ||
			!domainsMatchTarget(member.Domains, target) {
			return nil, "", fmt.Errorf("target identity or topology changed after plan approval")
		}
		if !member.HealthKnown || !member.Healthy || member.Maintenance || member.HealthObserved.Before(effectivePolicy.RequiredHealthFreshBy) {
			return nil, "", topologyrollout.ErrTargetUnavailable
		}
		found = true
	}
	if !found {
		return nil, "", topologyrollout.ErrTargetUnavailable
	}
	workerUsername := workers[target.DeviceUID]
	if workerUsername == "" {
		return nil, "", fmt.Errorf("target worker identity is unavailable")
	}
	return members, workerUsername, nil
}

func (r *IOSXESoftwareRolloutReconciler) revalidateFrozenTarget(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) error {
	if target.WorkerProtocolVersion != managedprotocol.Version {
		return fmt.Errorf("frozen target worker protocol %q does not match manager protocol %q",
			target.WorkerProtocolVersion, managedprotocol.Version)
	}
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return fmt.Errorf("read frozen target device: %w", err)
	}
	if string(device.UID) != target.DeviceUID || device.Generation != target.DeviceGeneration ||
		device.Spec.Driver != ciskov1.DeviceDriverXE || string(device.Spec.Driver) != target.Driver ||
		!currentPolicy.Selector.Matches(labels.Set(device.Labels)) {
		return fmt.Errorf("frozen target device incarnation or fleet membership changed")
	}
	if device.Status.NodeIdentity == nil || device.Status.TopologyProjection == nil ||
		device.Status.NodeIdentity.NodeName != target.NodeName || device.Status.NodeIdentity.NodeUID != target.NodeUID ||
		device.Status.NodeIdentity.DeviceUID != target.DeviceUID {
		return fmt.Errorf("frozen target Node identity changed")
	}
	declaredPhysicalIdentity, err := topology.CanonicalPhysicalIdentity(device.Spec.PhysicalIdentity)
	if err != nil || device.Status.NodeIdentity.PhysicalIdentity != declaredPhysicalIdentity ||
		declaredPhysicalIdentity != target.PhysicalIdentity {
		return fmt.Errorf("frozen target physical identity changed")
	}
	if device.Status.TopologyProjection.EffectiveLabelHash != target.ProjectionHash {
		return fmt.Errorf("frozen target topology projection changed")
	}
	if device.Labels[managedprotocol.ImageFamilyLabel] != target.ImageFamily {
		return fmt.Errorf("frozen target image-family capability changed")
	}
	if target.QualificationCohort == "" ||
		device.Labels[managedprotocol.QualificationCohortLabel] != target.QualificationCohort {
		return fmt.Errorf("frozen target qualification cohort changed")
	}
	for _, value := range target.Topology {
		if device.Labels[value.Key] != value.Value {
			return fmt.Errorf("frozen target topology %q changed", value.Key)
		}
	}
	if gnoiCondition := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionGNOIConfigurationReady); gnoiCondition == nil || gnoiCondition.Status != metav1.ConditionTrue {
		return fmt.Errorf("frozen target gNOI configuration is not ready")
	}
	if _, err := r.currentReadyWorkerRevision(ctx, &device); err != nil {
		return fmt.Errorf("frozen target managed worker revision is not ready: %w", err)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) currentReadyWorkerRevision(
	ctx context.Context,
	device *ciskov1.CiscoDevice,
) (string, error) {
	if device == nil || device.Status.WorkerRevision == nil {
		return "", fmt.Errorf("CiscoDevice has no managed worker revision status")
	}
	status := device.Status.WorkerRevision
	if status.DesiredRevision == "" || status.DesiredRevision != status.ObservedRevision ||
		status.DeploymentUID == "" || status.DeploymentGeneration < 1 || status.PodUID == "" ||
		status.PodStartTime == nil || status.ReadyHeartbeatTime == nil ||
		status.ReadyHeartbeatTime.Before(status.PodStartTime) {
		return "", fmt.Errorf("CiscoDevice managed worker revision proof is incomplete")
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace,
		Name:      device.Name + deploymentSuffix,
	}}
	fresh, ready, err := observeManagedWorkerRevision(
		ctx, r.reader(), r.now(), device, deployment, status.DesiredRevision,
	)
	if err != nil {
		return "", err
	}
	if !ready || fresh == nil || fresh.DesiredRevision != status.DesiredRevision ||
		fresh.ObservedRevision != status.ObservedRevision ||
		fresh.DeploymentUID != status.DeploymentUID ||
		fresh.DeploymentGeneration != status.DeploymentGeneration ||
		fresh.PodUID != status.PodUID || fresh.PodStartTime == nil || fresh.ReadyHeartbeatTime == nil ||
		!fresh.PodStartTime.Equal(status.PodStartTime) ||
		!fresh.ReadyHeartbeatTime.Equal(status.ReadyHeartbeatTime) {
		return "", fmt.Errorf("live Deployment, Pod, or worker heartbeat no longer matches CiscoDevice status")
	}
	return status.DesiredRevision, nil
}

func (r *IOSXESoftwareRolloutReconciler) currentTargetWorkerRevision(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
) (string, error) {
	var device ciskov1.CiscoDevice
	if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		return "", fmt.Errorf("read target worker revision: %w", err)
	}
	if string(device.UID) != target.DeviceUID || device.Generation != target.DeviceGeneration {
		return "", fmt.Errorf("target CiscoDevice incarnation changed while checking worker revision")
	}
	return r.currentReadyWorkerRevision(ctx, &device)
}

func (r *IOSXESoftwareRolloutReconciler) requireWorkerRevisionAcknowledgement(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	control *opsv1alpha1.UpgradeWorkerControlStatus,
) (string, error) {
	if control == nil || control.ObservedWorkerConfigRevision == "" {
		return "", fmt.Errorf("leaf has no worker configuration revision acknowledgement")
	}
	current, err := r.currentTargetWorkerRevision(ctx, rollout, target)
	if err != nil {
		return "", err
	}
	if control.ObservedWorkerConfigRevision != current {
		return "", fmt.Errorf("leaf worker acknowledgement no longer matches the ready worker revision")
	}
	return current, nil
}

func domainsMatchTarget(domains map[string]string, target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) bool {
	frozen := targetDomains(target)
	for key, value := range domains {
		if frozen[key] != value {
			return false
		}
	}
	return true
}

func (r *IOSXESoftwareRolloutReconciler) currentFleetMembers(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
) ([]topologyrollout.Member, map[string]string, error) {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices); err != nil {
		return nil, nil, fmt.Errorf("list administrator-managed fleet: %w", err)
	}
	members := make([]topologyrollout.Member, 0, len(devices.Items))
	workers := map[string]string{}
	for i := range devices.Items {
		device := &devices.Items[i]
		if !currentPolicy.Selector.Matches(labels.Set(device.Labels)) {
			continue
		}
		if device.UID == "" || device.Status.NodeIdentity == nil || device.Status.TopologyProjection == nil {
			return nil, nil, fmt.Errorf("managed fleet inventory is incomplete for %s/%s", device.Namespace, device.Name)
		}
		identity := device.Status.NodeIdentity
		if identity.DeviceUID != string(device.UID) {
			return nil, nil, fmt.Errorf("managed fleet identity is stale for %s/%s", device.Namespace, device.Name)
		}
		var node corev1.Node
		if err := r.reader().Get(ctx, types.NamespacedName{Name: identity.NodeName}, &node); err != nil {
			return nil, nil, fmt.Errorf("read managed fleet Node %q: %w", identity.NodeName, err)
		}
		if string(node.UID) != identity.NodeUID || !managedNodeMatchesDevice(&node, device) ||
			node.Annotations[managedprotocol.AnnotationNodeUID] != identity.NodeUID ||
			node.Annotations[managedprotocol.AnnotationWorkerProtocol] != managedprotocol.Version {
			return nil, nil, fmt.Errorf("managed fleet Node binding is invalid for %s/%s", device.Namespace, device.Name)
		}
		for _, key := range currentPolicy.Config.ProjectedTopologyKeys {
			if node.Labels[key] != device.Labels[key] {
				return nil, nil, fmt.Errorf("managed fleet Node projection %q is stale for %s/%s", key, device.Namespace, device.Name)
			}
		}
		physicalID, err := stablePhysicalIdentity(device, &node)
		if err != nil {
			return nil, nil, fmt.Errorf("managed fleet identity for %s/%s: %w", device.Namespace, device.Name, err)
		}
		domains := make(map[string]string, len(currentPolicy.Config.RequiredTopologyKeys))
		for _, key := range currentPolicy.Config.RequiredTopologyKeys {
			domains[key] = device.Labels[key]
		}
		for key := range effectivePolicy.DomainBudgets {
			domains[key] = device.Labels[key]
		}
		for key := range effectivePolicy.DomainTransferBudgets {
			domains[key] = device.Labels[key]
		}
		for key, value := range domains {
			if strings.TrimSpace(value) == "" {
				return nil, nil, fmt.Errorf("managed fleet member %s/%s lacks risk domain %q", device.Namespace, device.Name, key)
			}
		}
		readyCondition := nodeReadyCondition(&node)
		healthy := device.Status.Phase == "Ready" && readyCondition != nil && readyCondition.Status == corev1.ConditionTrue &&
			deviceConditionCurrentTrue(device, ciskov1.CiscoDeviceConditionNodeIdentityReady) &&
			deviceConditionCurrentTrue(device, ciskov1.CiscoDeviceConditionTopologyReady) &&
			deviceConditionCurrentTrue(device, ciskov1.CiscoDeviceConditionGNOIConfigurationReady) &&
			managedWorkerReadyForProjection(&node, device.Status.TopologyProjection) &&
			!hasTopologyInitializationGuard(&node)
		// A Node heartbeat and CiscoDevice conditions change independently. The
		// manager-authenticated snapshot binds both sources; neither a fresh Node
		// nor an old True device condition can mask stale evidence from the other.
		healthConditionTypes := []string{
			ciskov1.CiscoDeviceConditionNodeIdentityReady,
			ciskov1.CiscoDeviceConditionTopologyReady,
			ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
		}
		observed, observationErr := managedDeviceHealthObservedAt(device, &node, r.now(), healthConditionTypes...)
		if observationErr != nil {
			healthy = false
			observed = time.Time{}
		}
		maintenance := activeMaintenanceSession(device.Status.MaintenanceSession)
		members = append(members, topologyrollout.Member{
			PhysicalID: physicalID, DeviceUID: string(device.UID), NodeUID: identity.NodeUID,
			Domains: domains, HealthKnown: observationErr == nil && !observed.IsZero(), Healthy: healthy,
			Maintenance: maintenance, HealthObserved: observed,
		})
		workers[string(device.UID)] = node.Annotations[managedprotocol.AnnotationWorkerUsername]
	}
	if len(members) == 0 {
		return nil, nil, fmt.Errorf("administrator-managed fleet is empty")
	}
	return members, workers, nil
}

func nodeReadyCondition(node *corev1.Node) *corev1.NodeCondition {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeReady {
			return &node.Status.Conditions[i]
		}
	}
	return nil
}

func deviceConditionCurrentTrue(device *ciskov1.CiscoDevice, conditionType string) bool {
	if device == nil {
		return false
	}
	condition := meta.FindStatusCondition(device.Status.Conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue &&
		condition.ObservedGeneration == device.Generation
}

func nodeReadyObservation(condition *corev1.NodeCondition) time.Time {
	if condition == nil || condition.LastHeartbeatTime.IsZero() {
		return time.Time{}
	}
	return condition.LastHeartbeatTime.Time
}

func managedDeviceHealthObservedAt(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	now time.Time,
	conditionTypes ...string,
) (time.Time, error) {
	if device == nil || device.Status.HealthObservation == nil {
		return time.Time{}, fmt.Errorf("manager health snapshot is absent")
	}
	ready := nodeReadyCondition(node)
	if ready == nil || ready.LastHeartbeatTime.IsZero() {
		return time.Time{}, fmt.Errorf("live Node Ready heartbeat is absent")
	}
	observation := device.Status.HealthObservation
	if observation.ObservedAt.IsZero() || observation.NodeReadyHeartbeatTime.IsZero() {
		return time.Time{}, fmt.Errorf("manager health snapshot timestamps are incomplete")
	}
	if !observation.NodeReadyHeartbeatTime.Time.Equal(ready.LastHeartbeatTime.Time) {
		return time.Time{}, fmt.Errorf("live Node Ready heartbeat is newer or different from the manager snapshot")
	}
	conditionsHash, err := deviceConditionsHash(device)
	if err != nil {
		return time.Time{}, err
	}
	if observation.DeviceConditionsHash != conditionsHash {
		return time.Time{}, fmt.Errorf("CiscoDevice phase or conditions changed after the manager snapshot")
	}
	// Use the older source time. This prevents a condition-only reconciliation
	// from refreshing an old Node heartbeat and prevents a future-dated worker
	// heartbeat from extending manager-authenticated freshness.
	observed := observation.ObservedAt.Time
	if ready.LastHeartbeatTime.Time.Before(observed) {
		observed = ready.LastHeartbeatTime.Time
	}
	for _, conditionType := range conditionTypes {
		condition := meta.FindStatusCondition(device.Status.Conditions, conditionType)
		if condition == nil {
			return time.Time{}, fmt.Errorf("required device condition %q is absent", conditionType)
		}
		conditionObserved := time.Time{}
		foundObservation := false
		for i := range observation.ConditionObservations {
			candidate := &observation.ConditionObservations[i]
			if candidate.Type != conditionType {
				continue
			}
			foundObservation = true
			if candidate.Status != condition.Status || candidate.ObservedGeneration != condition.ObservedGeneration {
				return time.Time{}, fmt.Errorf("required device condition %q changed after its producer observation", conditionType)
			}
			conditionObserved = candidate.ObservedAt.Time
			break
		}
		if !foundObservation || conditionObserved.IsZero() {
			return time.Time{}, fmt.Errorf("required device condition %q has no producer observation time", conditionType)
		}
		if conditionObserved.Before(observed) {
			observed = conditionObserved
		}
	}
	if observed.After(now) {
		observed = now
	}
	return observed, nil
}

func activeMaintenanceSession(session *ciskov1.DeviceMaintenanceSessionStatus) bool {
	if session == nil {
		return false
	}
	switch session.Phase {
	case ciskov1.DeviceMaintenanceSessionAcknowledged,
		ciskov1.DeviceMaintenanceSessionActive:
		return true
	default:
		return false
	}
}

func (r *IOSXESoftwareRolloutReconciler) ensureNoRunningWorkloads(ctx context.Context, nodeName string) error {
	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.MatchingFields{rolloutPodNodeNameIndex: nodeName}); err != nil {
		return fmt.Errorf("list workloads bound to Node %q: %w", nodeName, err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		if pod.DeletionTimestamp != nil ||
			(pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed) {
			return fmt.Errorf("%w: %s/%s is %s", errWorkloadsRunning, pod.Namespace, pod.Name, pod.Status.Phase)
		}
	}
	return nil
}

func terminalLeafPhase(phase opsv1alpha1.UpgradePhase) bool {
	switch phase {
	case opsv1alpha1.UpgradePhaseSucceeded,
		opsv1alpha1.UpgradePhaseStagedForNextBoot,
		opsv1alpha1.UpgradePhaseFailed,
		opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseValidationFailed,
		opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseRebootTimeout,
		opsv1alpha1.UpgradePhaseCancelled:
		return true
	default:
		return false
	}
}

func terminalLeafFailure(phase opsv1alpha1.UpgradePhase) bool {
	switch phase {
	case opsv1alpha1.UpgradePhaseFailed,
		opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseValidationFailed,
		opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseRebootTimeout,
		opsv1alpha1.UpgradePhaseCancelled:
		return true
	default:
		return false
	}
}

func leafMutationOutcomeResolved(leaf *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if leaf == nil || !terminalLeafPhase(leaf.Status.Phase) || leaf.Status.CompletionTime == nil {
		return false
	}
	if len(leaf.Status.ManagedMutationClaims) == 0 {
		return true
	}
	return meta.IsStatusConditionTrue(leaf.Status.Conditions, "DeviceMutationSettled")
}

func (r *IOSXESoftwareRolloutReconciler) trySettleLeaf(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	summary opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
	now time.Time,
) (bool, string, string, error) {
	if leaf.Status.ManagerAdmission != nil &&
		leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
		return true, "HealthGatePassed", "reservation and post-mutation health gate were already settled", nil
	}
	if !leafMutationOutcomeResolved(leaf) {
		return false, "MutationOutcomeUnresolved", "worker has not produced conclusive mutation-outcome evidence", nil
	}
	effectivePolicy, err := effectiveAdmissionPolicy(rollout, currentPolicy, now)
	if err != nil {
		return false, "PolicyUnavailable", "", err
	}
	healthy, detail, err := r.targetPostMutationHealthy(ctx, rollout, currentPolicy, effectivePolicy, target,
		leaf.Status.CompletionTime.Time)
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
	const healthySoakReason = "HealthyPostMutationSoak"
	soakDeadline, started := continuousHealthySoakDeadline(summary, leaf.Status.CompletionTime.Time, soak)
	if !started {
		return false, healthySoakReason, "target is healthy; starting the continuous post-mutation soak interval", nil
	}
	if now.Before(soakDeadline) {
		return false, healthySoakReason,
			fmt.Sprintf("healthy post-mutation soak remains until %s", soakDeadline.UTC().Format(time.RFC3339)), nil
	}
	store := r.ledgerStore(rollout)
	reservation := reservationID(string(rollout.UID), target.DeviceUID)
	policyEpoch := leaf.Status.ManagerAdmission.PolicyEpoch
	lockEpoch, lockID, err := r.topologyLockAcquisitionForRelease(ctx, rollout, target, policyEpoch,
		leaf.Status.ManagerAdmission.TopologyLockID)
	if err != nil {
		return false, "TopologyLockReleaseFailed", "", err
	}
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, lockEpoch, lockID); err != nil {
		return false, "TopologyLockReleaseFailed", "", err
	}
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.Settle(ledger, reservation, lockID, string(leaf.UID), lockEpoch, true, true)
	}); err != nil {
		return false, "LedgerSettlementFailed", "", fmt.Errorf("settle rollout reservation: %w", err)
	}
	if err := r.releaseDeviceTopologyLock(ctx, rollout, target, lockEpoch, lockID); err != nil {
		return false, "TopologyLockReleaseFailed", "", err
	}
	if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if err := validateManagerAdmission(rollout, target, current); err != nil {
			return err
		}
		current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
		current.Status.ManagerAdmission.RevocationReason = ""
		current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
		return nil
	}); err != nil {
		return false, "LeafSettlementFailed", "", err
	}
	return true, "HealthGatePassed", "post-mutation health and soak gates passed", nil
}

func continuousHealthySoakDeadline(
	summary opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
	completion time.Time,
	soak time.Duration,
) (time.Time, bool) {
	if summary.Phase != opsv1alpha1.IOSXESoftwareRolloutTargetSoaking ||
		summary.Reason != "HealthyPostMutationSoak" || summary.LastTransitionTime.IsZero() {
		return time.Time{}, false
	}
	started := summary.LastTransitionTime.Time
	if completion.After(started) {
		started = completion
	}
	return started.Add(soak), true
}

func (r *IOSXESoftwareRolloutReconciler) targetPostMutationHealthy(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	effectivePolicy topologyrollout.Policy,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	operationCompletedAt time.Time,
) (bool, string, error) {
	members, _, err := r.currentFleetMembers(ctx, rollout, currentPolicy, effectivePolicy)
	if err != nil {
		return false, "", err
	}
	var matched bool
	for _, member := range members {
		if member.PhysicalID != target.PhysicalIdentity {
			continue
		}
		if matched {
			return false, "target physical identity is enrolled more than once", nil
		}
		matched = true
		if member.DeviceUID != target.DeviceUID || member.NodeUID != target.NodeUID ||
			!domainsMatchTarget(member.Domains, target) {
			return false, "target identity or topology changed after mutation", nil
		}
		if !member.HealthKnown || !member.Healthy || member.HealthObserved.Before(effectivePolicy.RequiredHealthFreshBy) {
			return false, "target health is unknown, stale, or unhealthy", nil
		}
		if !isPostOperationObservation(member.HealthObserved, operationCompletedAt) {
			return false, "waiting for a post-operation managed Node and device health observation", nil
		}
		// Its own acknowledged maintenance session does not make the target
		// unhealthy for settlement; all other fleet budget calculations still
		// conservatively count that session as unavailable.
	}
	if matched {
		return true, "target is healthy", nil
	}
	return false, "target is absent from the current managed fleet", nil
}

func isPostOperationObservation(observed, completed time.Time) bool {
	return !observed.IsZero() && !completed.IsZero() && observed.After(completed)
}

func (r *IOSXESoftwareRolloutReconciler) propagateControl(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	pause, cancel bool,
	now time.Time,
) error {
	return r.propagateLeafControl(ctx, rollout, pause, cancel, rollout.Spec.Control.Revision, now)
}

func (r *IOSXESoftwareRolloutReconciler) propagateLeafControl(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	pause, cancel bool,
	revision int64,
	now time.Time,
) error {
	var controlErrors []error
	for _, target := range rollout.Status.FrozenPlan.Targets {
		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		if err := r.reader().Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}, &leaf); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			controlErrors = append(controlErrors, fmt.Errorf("read retained rollout leaf %s/%s while applying campaign control: %w",
				rollout.Namespace, target.ChildName, err))
			continue
		}
		if err := validateManagedLeafBinding(rollout, target, &leaf); err != nil {
			controlErrors = append(controlErrors, err)
			continue
		}
		if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(&leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
			if current.UID != leaf.UID {
				return fmt.Errorf("leaf incarnation changed while applying campaign control")
			}
			if current.Status.ManagerControl != nil && current.Status.ManagerControl.Revision > revision {
				return fmt.Errorf("leaf has newer manager control revision %d", current.Status.ManagerControl.Revision)
			}
			if current.Status.ManagerControl != nil && current.Status.ManagerControl.Revision == revision &&
				current.Status.ManagerControl.Pause == pause && current.Status.ManagerControl.Cancel == cancel {
				return nil
			}
			current.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
				Revision: revision, Pause: pause, Cancel: cancel,
				UpdatedAt: metav1.NewTime(now), Reason: "CampaignControl",
			}
			if current.Status.ManagerAdmission != nil {
				if current.Status.ManagerAdmission.ControlRevision != nil && *current.Status.ManagerAdmission.ControlRevision > revision {
					return fmt.Errorf("leaf admission has newer control revision %d", *current.Status.ManagerAdmission.ControlRevision)
				}
				current.Status.ManagerAdmission.ControlRevision = &revision
				if cancel && current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
					current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
					current.Status.ManagerAdmission.RevocationReason = "CampaignCancelled"
				}
				current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
			}
			return nil
		}); err != nil {
			controlErrors = append(controlErrors, err)
		}
	}
	return errors.Join(controlErrors...)
}

// ensurePolicyEpochFences establishes a durable tombstone for every target,
// revokes only leaves that have no accepted mutation claim, and releases only
// reservations from the still-effective epoch. Claimed leaves retain their old
// grant and reservation so the worker can complete and the manager can observe
// their conclusive terminal health before settlement.
func (r *IOSXESoftwareRolloutReconciler) ensurePolicyEpochFences(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	now time.Time,
) error {
	if rollout == nil || rollout.Status.FrozenPlan == nil || rollout.Status.EffectivePolicy == nil || rollout.Status.PolicyTransition == nil {
		return fmt.Errorf("policy epoch transition is incomplete")
	}
	var fenceErrors []error
	for _, target := range rollout.Status.FrozenPlan.Targets {
		if err := r.ensurePolicyEpochFenceForTarget(ctx, rollout, target, now); err != nil {
			fenceErrors = append(fenceErrors, fmt.Errorf("fence target %s: %w", target.DeviceName, err))
		}
	}
	return errors.Join(fenceErrors...)
}

func (r *IOSXESoftwareRolloutReconciler) ensurePolicyEpochFenceForTarget(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	now time.Time,
) error {
	effective := rollout.Status.EffectivePolicy
	workerUsername := fmt.Sprintf("system:serviceaccount:%s:%s", rollout.Namespace,
		managedWorkerServiceAccountName(&ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace, Name: target.DeviceName, UID: types.UID(target.DeviceUID),
		}}))
	if current, err := r.currentWorkerUsername(ctx, target); err == nil {
		workerUsername = current
	}
	key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{}
	err := r.reader().Get(ctx, key, leaf)
	if apierrors.IsNotFound(err) {
		leaf = &opsv1alpha1.IOSXESoftwareUpgrade{
			ObjectMeta: metav1.ObjectMeta{Namespace: rollout.Namespace, Name: target.ChildName,
				Labels:      map[string]string{managedprotocol.AnnotationCampaignUID: string(rollout.UID)},
				Annotations: rolloutLeafAnnotations(rollout, target, workerUsername, now)},
			Spec: expectedLeafSpec(rollout, target),
		}
		if err := r.Create(ctx, leaf); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create retained policy-transition tombstone %s: %w", key, err)
		}
		if err := r.reader().Get(ctx, key, leaf); err != nil {
			return fmt.Errorf("read retained policy-transition tombstone %s: %w", key, err)
		}
	} else if err != nil {
		return err
	}
	if err := validateManagedLeafBinding(rollout, target, leaf); err != nil {
		return err
	}
	if !reflect.DeepEqual(leaf.Spec, expectedLeafSpec(rollout, target)) {
		return fmt.Errorf("policy-transition tombstone %s spec differs from the frozen target", key)
	}
	if err := r.patchLeafManagerFields(ctx, key, func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID {
			return fmt.Errorf("policy-transition tombstone incarnation changed")
		}
		if current.Status.ManagerAdmission == nil {
			revision := rollout.Spec.Control.Revision
			current.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
				ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
				State:           opsv1alpha1.UpgradeManagerAdmissionRevoked,
				CampaignUID:     string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
				PolicyUID: effective.Policy.UID, PolicyResourceVersion: effective.Policy.ResourceVersion,
				PolicyEpoch: effective.Epoch, RevocationReason: "PolicyEpochTransition",
				LedgerUID: effective.Policy.LedgerUID, ReservationID: reservationID(string(rollout.UID), target.DeviceUID),
				LeafUID: string(current.UID), DeviceUID: target.DeviceUID, DeviceGeneration: target.DeviceGeneration,
				PhysicalIdentity: target.PhysicalIdentity, NodeUID: target.NodeUID,
				ControlRevision: &revision, UpdatedAt: metav1.NewTime(now),
			}
			if current.Status.ManagerControl == nil {
				current.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
					Revision: revision, UpdatedAt: metav1.NewTime(now), Reason: "PolicyEpochTransition",
				}
			}
			return nil
		}
		if err := validateManagerAdmission(rollout, target, current); err != nil {
			return err
		}
		if current.Status.ManagerAdmission.PolicyEpoch != effective.Epoch {
			return fmt.Errorf("leaf policy epoch changed while the preceding epoch was being fenced")
		}
		if len(current.Status.ManagedMutationClaims) != 0 || current.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			return nil
		}
		if current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked {
			current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
			current.Status.ManagerAdmission.RevocationReason = "PolicyEpochTransition"
			current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := r.reader().Get(ctx, key, leaf); err != nil {
		return err
	}
	if len(leaf.Status.ManagedMutationClaims) != 0 {
		return r.verifyClaimedReservation(ctx, rollout, target, leaf)
	}
	if leaf.Status.ManagerAdmission == nil || leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
		return nil
	}
	if leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
		leaf.Status.ManagerAdmission.PolicyEpoch != effective.Epoch {
		return fmt.Errorf("zero-claim leaf is not fenced at effective policy epoch %d", effective.Epoch)
	}
	store := r.ledgerStore(rollout)
	reservation := reservationID(string(rollout.UID), target.DeviceUID)
	if err := r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, string(leaf.UID),
		uint64(rollout.Spec.Control.Revision), effective.Epoch, leaf.Status.ManagerAdmission.TopologyLockID); err != nil {
		return fmt.Errorf("release preceding policy epoch reservation: %w", err)
	}
	_, ledger, err := store.Read(ctx)
	if err != nil {
		return err
	}
	if _, exists := ledger.Reservations[reservation]; exists {
		return fmt.Errorf("preceding policy epoch reservation remains after release")
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) ensureFailureFences(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	children map[string]opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) error {
	policySnapshot, policyEpoch, err := rolloutEffectivePolicyBinding(rollout)
	if err != nil {
		return err
	}
	var fenceErrors []error
	for _, target := range rollout.Status.FrozenPlan.Targets {
		leaf, exists := children[target.ChildName]
		if !exists {
			if _, err := r.ensureRetainedFenceForTarget(ctx, rollout, target,
				rollout.Spec.Control.Revision, false, "CampaignTargetFailed", now); err != nil {
				fenceErrors = append(fenceErrors,
					fmt.Errorf("create failure tombstone for target %s: %w", target.DeviceName, err))
			}
			continue
		}
		if err := validateManagedLeafBinding(rollout, target, &leaf); err != nil {
			fenceErrors = append(fenceErrors, err)
			continue
		}
		key := client.ObjectKeyFromObject(&leaf)
		if err := r.patchLeafManagerFields(ctx, key, func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
			if current.UID != leaf.UID {
				return fmt.Errorf("failure-fenced leaf incarnation changed")
			}
			if len(current.Status.ManagedMutationClaims) != 0 ||
				(current.Status.ManagerAdmission != nil && current.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled) {
				return nil
			}
			if current.Status.ManagerAdmission == nil {
				revision := rollout.Spec.Control.Revision
				current.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
					ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
					State:           opsv1alpha1.UpgradeManagerAdmissionRevoked,
					CampaignUID:     string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
					PolicyUID: policySnapshot.UID, PolicyResourceVersion: policySnapshot.ResourceVersion,
					PolicyEpoch: policyEpoch, RevocationReason: "CampaignTargetFailed",
					LedgerUID: policySnapshot.LedgerUID, ReservationID: reservationID(string(rollout.UID), target.DeviceUID),
					LeafUID: string(current.UID), DeviceUID: target.DeviceUID, DeviceGeneration: target.DeviceGeneration,
					PhysicalIdentity: target.PhysicalIdentity, NodeUID: target.NodeUID,
					ControlRevision: &revision, UpdatedAt: metav1.NewTime(now),
				}
				if current.Status.ManagerControl == nil {
					current.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
						Revision: revision, UpdatedAt: metav1.NewTime(now), Reason: "CampaignTargetFailed",
					}
				}
				return nil
			}
			if err := validateManagerAdmission(rollout, target, current); err != nil {
				return err
			}
			if current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked {
				current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
			}
			current.Status.ManagerAdmission.RevocationReason = "CampaignTargetFailed"
			current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
			return nil
		}); err != nil {
			fenceErrors = append(fenceErrors, fmt.Errorf("revoke zero-claim target %s: %w", target.DeviceName, err))
			continue
		}
		if err := r.reader().Get(ctx, key, &leaf); err != nil {
			fenceErrors = append(fenceErrors, err)
			continue
		}
		if len(leaf.Status.ManagedMutationClaims) != 0 {
			if err := r.verifyClaimedReservation(ctx, rollout, target, &leaf); err != nil {
				fenceErrors = append(fenceErrors, err)
			}
			continue
		}
		if leaf.Status.ManagerAdmission == nil || leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			continue
		}
		if err := r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, string(leaf.UID),
			uint64(rollout.Spec.Control.Revision), leaf.Status.ManagerAdmission.PolicyEpoch,
			leaf.Status.ManagerAdmission.TopologyLockID); err != nil {
			fenceErrors = append(fenceErrors, fmt.Errorf("release failed-campaign target %s: %w", target.DeviceName, err))
		}
	}
	return errors.Join(fenceErrors...)
}

// ensureRetainedFenceForTarget retains one exact leaf even when a stale
// manager reserved the target but had not completed its Create request. The
// leaf is the durable tombstone that makes a delayed Create collide, while its
// revoked/settled manager admission keeps every managed worker fail-closed.
// Cancellation additionally publishes terminal manager control; a policy
// drift fence relies on the terminal admission state without inventing a
// control revision that was never requested on the campaign.
func (r *IOSXESoftwareRolloutReconciler) ensureRetainedFenceForTarget(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	revision int64,
	cancel bool,
	fenceReason string,
	now time.Time,
) (*opsv1alpha1.IOSXESoftwareUpgrade, error) {
	policySnapshot, policyEpoch, err := rolloutEffectivePolicyBinding(rollout)
	if err != nil {
		return nil, err
	}
	workerUsername := fmt.Sprintf("system:serviceaccount:%s:%s", rollout.Namespace,
		managedWorkerServiceAccountName(&ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace, Name: target.DeviceName, UID: types.UID(target.DeviceUID),
		}}))
	if current, err := r.currentWorkerUsername(ctx, target); err == nil {
		workerUsername = current
	}
	key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{}
	err = r.reader().Get(ctx, key, leaf)
	if apierrors.IsNotFound(err) {
		leaf = &opsv1alpha1.IOSXESoftwareUpgrade{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: rollout.Namespace, Name: target.ChildName,
				Labels:      map[string]string{managedprotocol.AnnotationCampaignUID: string(rollout.UID)},
				Annotations: rolloutLeafAnnotations(rollout, target, workerUsername, now),
			},
			Spec: expectedLeafSpec(rollout, target),
		}
		if err := r.Create(ctx, leaf); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create retained cancellation tombstone %s: %w", key, err)
		}
		if err := r.reader().Get(ctx, key, leaf); err != nil {
			return nil, fmt.Errorf("read retained cancellation tombstone %s: %w", key, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read retained cancellation tombstone %s: %w", key, err)
	}
	if err := validateManagedLeafBinding(rollout, target, leaf); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(leaf.Spec, expectedLeafSpec(rollout, target)) {
		return nil, fmt.Errorf("cancellation tombstone %s spec does not equal the immutable frozen target", key)
	}

	if err := r.patchLeafManagerFields(ctx, key, func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID {
			return fmt.Errorf("cancellation tombstone incarnation changed")
		}
		if current.Status.ManagerAdmission == nil {
			controlRevision := revision
			current.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
				ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
				State:           opsv1alpha1.UpgradeManagerAdmissionRevoked,
				CampaignUID:     string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
				PolicyUID:             policySnapshot.UID,
				PolicyResourceVersion: policySnapshot.ResourceVersion,
				PolicyEpoch:           policyEpoch,
				RevocationReason:      fenceReason,
				LedgerUID:             rollout.Status.FrozenPlan.Policy.LedgerUID,
				ReservationID:         reservationID(string(rollout.UID), target.DeviceUID),
				LeafUID:               string(current.UID), DeviceUID: target.DeviceUID,
				DeviceGeneration: target.DeviceGeneration,
				PhysicalIdentity: target.PhysicalIdentity, NodeUID: target.NodeUID,
				ControlRevision: &controlRevision, UpdatedAt: metav1.NewTime(now),
			}
		} else {
			if err := validateManagerAdmission(rollout, target, current); err != nil {
				return err
			}
			admissionChanged := false
			if current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
				if current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked {
					current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
					current.Status.ManagerAdmission.RevocationReason = fenceReason
					admissionChanged = true
				} else if current.Status.ManagerAdmission.RevocationReason == "PolicyEpochTransition" && fenceReason != "PolicyEpochTransition" {
					current.Status.ManagerAdmission.RevocationReason = fenceReason
					admissionChanged = true
				}
			}
			if current.Status.ManagerAdmission.ControlRevision == nil || *current.Status.ManagerAdmission.ControlRevision < revision {
				controlRevision := revision
				current.Status.ManagerAdmission.ControlRevision = &controlRevision
				admissionChanged = true
			}
			if admissionChanged {
				current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
			}
		}
		if current.Status.ManagerControl == nil {
			current.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
				Revision: revision, Pause: rollout.Spec.Control.Pause, Cancel: cancel,
				UpdatedAt: metav1.NewTime(now), Reason: fenceReason,
			}
		} else if cancel {
			switch {
			case current.Status.ManagerControl.Revision < revision:
				current.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
					Revision: revision, Cancel: true, UpdatedAt: metav1.NewTime(now), Reason: fenceReason,
				}
			case current.Status.ManagerControl.Revision == revision &&
				(!current.Status.ManagerControl.Cancel || current.Status.ManagerControl.Pause):
				return fmt.Errorf("cancellation tombstone has conflicting control at revision %d", revision)
			case current.Status.ManagerControl.Revision > revision:
				return fmt.Errorf("cancellation tombstone has newer control revision %d", current.Status.ManagerControl.Revision)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := r.reader().Get(ctx, key, leaf); err != nil {
		return nil, err
	}
	if leaf.Status.ManagerAdmission == nil ||
		leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled ||
		len(leaf.Status.ManagedMutationClaims) != 0 ||
		leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionGranted {
		return leaf, nil
	}
	if err := r.releaseUnclaimedReservation(ctx, rollout, target, string(leaf.UID), uint64(revision),
		leaf.Status.ManagerAdmission.TopologyLockID); err != nil {
		return nil, err
	}
	if err := r.patchLeafManagerFields(ctx, key, func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID || len(current.Status.ManagedMutationClaims) != 0 || current.Status.ManagerAdmission == nil ||
			(current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked &&
				current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled) {
			return fmt.Errorf("cancellation tombstone claim fence changed before settlement")
		}
		if current.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			return nil
		}
		current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
		current.Status.ManagerAdmission.RevocationReason = ""
		current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := r.reader().Get(ctx, key, leaf); err != nil {
		return nil, err
	}
	return leaf, nil
}

func (r *IOSXESoftwareRolloutReconciler) ensureCancellationFenceForTarget(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	revision int64,
	now time.Time,
) (*opsv1alpha1.IOSXESoftwareUpgrade, error) {
	return r.ensureRetainedFenceForTarget(ctx, rollout, target, revision, true, "CampaignCancelled", now)
}

func (r *IOSXESoftwareRolloutReconciler) ensureCancellationFences(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	revision int64,
	now time.Time,
) error {
	var fenceErrors []error
	for _, target := range rollout.Status.FrozenPlan.Targets {
		if _, err := r.ensureCancellationFenceForTarget(ctx, rollout, target, revision, now); err != nil {
			fenceErrors = append(fenceErrors, fmt.Errorf("fence target %s: %w", target.DeviceName, err))
		}
	}
	return errors.Join(fenceErrors...)
}

func (r *IOSXESoftwareRolloutReconciler) ensurePolicyChangeFences(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	now time.Time,
) error {
	return r.ensureReplanFences(ctx, rollout, "AdministratorPolicyChanged", now)
}

func (r *IOSXESoftwareRolloutReconciler) ensureReplanFences(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	fenceReason string,
	now time.Time,
) error {
	var fenceErrors []error
	for _, target := range rollout.Status.FrozenPlan.Targets {
		leaf, err := r.ensureRetainedFenceForTarget(ctx, rollout, target,
			rollout.Spec.Control.Revision, false, fenceReason, now)
		if err != nil {
			fenceErrors = append(fenceErrors, fmt.Errorf("fence target %s: %w", target.DeviceName, err))
			continue
		}
		if leaf.Status.ManagerAdmission != nil &&
			leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			continue
		}
		if len(leaf.Status.ManagedMutationClaims) == 0 {
			continue
		}
		if err := r.verifyClaimedReservation(ctx, rollout, target, leaf); err != nil {
			fenceErrors = append(fenceErrors, fmt.Errorf("retain claimed target %s: %w", target.DeviceName, err))
		}
	}
	return errors.Join(fenceErrors...)
}

func (r *IOSXESoftwareRolloutReconciler) verifyClaimedReservation(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) error {
	if leaf == nil || leaf.Status.ManagerAdmission == nil {
		return fmt.Errorf("%w: claimed leaf has no manager admission", topologyrollout.ErrLedgerIdentity)
	}
	_, ledger, err := r.ledgerStore(rollout).Read(ctx)
	if err != nil {
		return err
	}
	reservation, ok := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)]
	if !ok {
		return fmt.Errorf("%w: claimed leaf has no durable reservation", topologyrollout.ErrLedgerIdentity)
	}
	admission := leaf.Status.ManagerAdmission
	if reservation.ChildUID != string(leaf.UID) || reservation.ChildNamespace != leaf.Namespace ||
		reservation.ChildName != leaf.Name || reservation.CampaignUID != string(rollout.UID) ||
		reservation.PlanHash != rollout.Status.FrozenPlan.Hash || reservation.DeviceUID != target.DeviceUID ||
		reservation.NodeUID != target.NodeUID || reservation.PhysicalID != target.PhysicalIdentity ||
		reservation.PolicyUID != admission.PolicyUID || reservation.PolicyEpoch != admission.PolicyEpoch ||
		reservation.TopologyLockID != admission.TopologyLockID ||
		reservation.PolicyVersion != admission.PolicyResourceVersion ||
		!reflect.DeepEqual(reservation.Domains, targetDomains(target)) {
		return fmt.Errorf("%w: claimed reservation identity no longer matches its retained leaf", topologyrollout.ErrLedgerIdentity)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) reconcilePolicyChanged(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	current *topologyrollout.ParsedAdminPolicy,
	now time.Time,
) (ctrl.Result, error) {
	frozen := rollout.Status.FrozenPlan.Policy
	message := fmt.Sprintf(
		"administrator policy changed from UID/resourceVersion %s/%s to %s/%s; all new mutation claims are fenced and a new rollout plan must be approved",
		frozen.UID, frozen.ResourceVersion, current.PolicyUID, current.ResourceVersion,
	)
	return r.reconcilePolicyFence(ctx, rollout, "PolicyChanged", message, now)
}

func (r *IOSXESoftwareRolloutReconciler) reconcilePolicyUnavailable(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	message string,
	now time.Time,
) (ctrl.Result, error) {
	message = truncateRolloutText(message+"; all new mutation claims are fenced and a new rollout plan must be approved", 512)
	return r.reconcilePolicyFence(ctx, rollout, "PolicyUnavailable", message, now)
}

func (r *IOSXESoftwareRolloutReconciler) reconcilePolicyFence(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	reason, message string,
	now time.Time,
) (ctrl.Result, error) {
	return r.reconcileReplanFence(ctx, rollout, "PolicyChanged", reason, "AdministratorPolicyChanged", message, now)
}

func (r *IOSXESoftwareRolloutReconciler) reconcileSourceChanged(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	detail string,
	now time.Time,
) (ctrl.Result, error) {
	message := truncateRolloutText(detail+"; all new mutation claims are fenced and a new rollout plan must be approved", 512)
	return r.reconcileReplanFence(ctx, rollout, "SourceChanged", "SourceIdentityChanged", "SourceIdentityChanged", message, now)
}

func (r *IOSXESoftwareRolloutReconciler) reconcileReplanFence(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	conditionType, reason, fenceReason, message string,
	now time.Time,
) (ctrl.Result, error) {
	fenceErr := r.ensureReplanFences(ctx, rollout, fenceReason, now)
	if fenceErr != nil {
		message += "; one or more safety reservations still require reconciliation"
	}

	before := rollout.DeepCopy()
	rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhasePaused
	rollout.Status.Message = truncateRolloutText(message, 512)
	setRolloutCondition(rollout, conditionType, metav1.ConditionTrue, "ReplanRequired", rollout.Status.Message, now)
	setRolloutCondition(rollout, "Ready", metav1.ConditionFalse, reason, rollout.Status.Message, now)
	if !reflect.DeepEqual(before.Status, rollout.Status) {
		if err := r.Status().Patch(ctx, rollout,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, fmt.Errorf("record rollout replan fence: %w", err)
		}
		r.emitRolloutEvent(rollout, corev1.EventTypeWarning, reason, rollout.Status.Message)
	}
	if fenceErr != nil {
		return ctrl.Result{RequeueAfter: rolloutPollInterval}, fmt.Errorf("reconcile rollout replan fence: %w", fenceErr)
	}
	return ctrl.Result{RequeueAfter: rolloutPollInterval}, nil
}

func (r *IOSXESoftwareRolloutReconciler) reconcileCancellation(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	currentPolicy *topologyrollout.ParsedAdminPolicy,
	now time.Time,
) (ctrl.Result, error) {
	if err := r.ensureCancellationFences(ctx, rollout, rollout.Spec.Control.Revision, now); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.propagateControl(ctx, rollout, false, true, now); err != nil {
		return ctrl.Result{}, err
	}
	children, err := r.rolloutChildren(ctx, rollout)
	if err != nil {
		return ctrl.Result{}, err
	}
	summaries := indexTargetSummaries(rollout.Status.Targets)
	allSettled := true
	for _, target := range rollout.Status.FrozenPlan.Targets {
		summary := summaries[target.DeviceUID]
		leaf, exists := children[target.ChildName]
		if !exists {
			if err := r.releaseUnclaimedReservation(ctx, rollout, target, "", uint64(rollout.Spec.Control.Revision)); err != nil {
				return ctrl.Result{}, err
			}
			transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetCancelled,
				"CancelledBeforeAdmission", "campaign cancelled before a leaf was created", now)
			summaries[target.DeviceUID] = summary
			continue
		}
		if err := validateManagedLeafBinding(rollout, target, &leaf); err != nil {
			return r.failRollout(ctx, rollout, "ChildIdentityConflict", err.Error(), false)
		}
		// Re-read after the control patch. This resourceVersion is the evidence
		// used to decide whether a worker won a concurrent mutation claim.
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(&leaf), &leaf); err != nil {
			return ctrl.Result{}, err
		}
		if leaf.Status.ManagerAdmission != nil && leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			reason := "CancelledAfterOutcome"
			message := "accepted work had already reached a settled outcome before cancellation"
			if len(leaf.Status.ManagedMutationClaims) == 0 {
				reason = "CancelledBeforeMutation"
				message = "retained cancellation tombstone prevents delayed mutation"
			}
			transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetCancelled, reason, message, now)
			summaries[target.DeviceUID] = summary
			continue
		}
		if len(leaf.Status.ManagedMutationClaims) == 0 && leaf.Status.ManagerAdmission != nil &&
			leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked {
			if err := r.releaseUnclaimedReservation(ctx, rollout, target, string(leaf.UID), uint64(rollout.Spec.Control.Revision),
				leaf.Status.ManagerAdmission.TopologyLockID); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(&leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
				if len(current.Status.ManagedMutationClaims) != 0 || current.Status.ManagerAdmission == nil ||
					current.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked {
					return fmt.Errorf("worker claim changed while settling unclaimed cancellation")
				}
				current.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
				current.Status.ManagerAdmission.RevocationReason = ""
				current.Status.ManagerAdmission.UpdatedAt = metav1.NewTime(now)
				return nil
			}); err != nil {
				return ctrl.Result{}, err
			}
			transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetCancelled,
				"CancelledBeforeMutation", "mutation authority was revoked before the worker claimed it", now)
			summaries[target.DeviceUID] = summary
			continue
		}

		if err := r.revokeClaimedReservation(ctx, rollout, target, uint64(rollout.Spec.Control.Revision)); err != nil {
			return ctrl.Result{}, err
		}
		if terminalLeafPhase(leaf.Status.Phase) {
			settled, gateReason, detail, settleErr := r.trySettleLeaf(ctx, rollout, currentPolicy, target, &leaf, summary, now)
			if settleErr != nil {
				return ctrl.Result{}, settleErr
			}
			if settled {
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetCancelled,
					"CancelledAfterOutcome", "accepted work reached a reconciled outcome before cancellation settled", now)
				summaries[target.DeviceUID] = summary
				continue
			}
			if gateReason == "HealthyPostMutationSoak" {
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetSoaking, gateReason, detail, now)
			} else {
				transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetCancelling,
					"CancellationHealthGate", detail, now)
			}
		} else {
			transitionTarget(&summary, opsv1alpha1.IOSXESoftwareRolloutTargetCancelling,
				"AcceptedMutationConverging", "cancellation blocks future claims; already accepted work is still being observed", now)
		}
		allSettled = false
		summaries[target.DeviceUID] = summary
	}
	if allSettled {
		return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled,
			"campaign cancellation is effective and every reservation is settled", now)
	}
	return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling,
		"cancellation is effective for new claims; accepted work is still converging", now)
}

func (r *IOSXESoftwareRolloutReconciler) releaseUnclaimedReservation(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	childUID string,
	revision uint64,
	expectedLockID ...string,
) error {
	policyEpoch, lockID, err := r.topologyLockAcquisitionForRelease(ctx, rollout, target, 0, expectedLockID...)
	if err != nil {
		return fmt.Errorf("resolve topology lock before reservation release: %w", err)
	}
	if lockID == "" {
		return nil
	}
	if err := r.persistLeafTopologyLockID(ctx, rollout, target, childUID, policyEpoch, lockID); err != nil {
		return fmt.Errorf("persist leaf topology-lock identity before reservation release: %w", err)
	}
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, policyEpoch, lockID); err != nil {
		return fmt.Errorf("fence topology lock before reservation release: %w", err)
	}
	store := r.ledgerStore(rollout)
	if err := r.revokeExactUnclaimedReservation(ctx, store, rollout, target, childUID, revision, policyEpoch, lockID); err != nil {
		return fmt.Errorf("revoke exact unclaimed reservation: %w", err)
	}
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.ReleaseUnclaimedAtEpoch(ledger,
			reservationID(string(rollout.UID), target.DeviceUID), lockID, childUID, revision, policyEpoch)
	}); err != nil {
		return fmt.Errorf("release unclaimed reservation: %w", err)
	}
	return r.releaseDeviceTopologyLock(ctx, rollout, target, policyEpoch, lockID)
}

func (r *IOSXESoftwareRolloutReconciler) releaseUnclaimedReservationAtEpoch(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	childUID string,
	revision uint64,
	policyEpoch int64,
	expectedLockID ...string,
) error {
	lockID := ""
	if len(expectedLockID) > 0 {
		lockID = expectedLockID[0]
	}
	if lockID == "" {
		_, resolved, err := r.topologyLockAcquisitionForRelease(ctx, rollout, target, policyEpoch)
		if err != nil {
			return fmt.Errorf("resolve topology lock before policy-epoch reservation release: %w", err)
		}
		lockID = resolved
	}
	if lockID == "" {
		return nil
	}
	if err := r.persistLeafTopologyLockID(ctx, rollout, target, childUID, policyEpoch, lockID); err != nil {
		return fmt.Errorf("persist leaf topology-lock identity before policy-epoch reservation release: %w", err)
	}
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, policyEpoch, lockID); err != nil {
		return fmt.Errorf("fence topology lock before policy-epoch reservation release: %w", err)
	}
	store := r.ledgerStore(rollout)
	if err := r.revokeExactUnclaimedReservation(ctx, store, rollout, target, childUID, revision, policyEpoch, lockID); err != nil {
		return fmt.Errorf("revoke exact unclaimed policy-epoch reservation: %w", err)
	}
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		return topologyrollout.ReleaseUnclaimedAtEpoch(ledger,
			reservationID(string(rollout.UID), target.DeviceUID), lockID, childUID, revision, policyEpoch)
	}); err != nil {
		return fmt.Errorf("release unclaimed policy-epoch reservation: %w", err)
	}
	return r.releaseDeviceTopologyLock(ctx, rollout, target, policyEpoch, lockID)
}

// revokeExactUnclaimedReservation closes the ledger half of a child grant
// before generic cleanup can remove it. Its API read runs inside every ledger
// CAS attempt, so a durable worker claim racing an earlier manager observation
// wins the safety decision and leaves both reservation and topology lock in
// place. Revoked is re-proven too because claimed cancellation uses the same
// ledger state and must proceed through outcome settlement instead.
func (r *IOSXESoftwareRolloutReconciler) revokeExactUnclaimedReservation(
	ctx context.Context,
	store topologyrollout.Store,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	childUID string,
	revision uint64,
	policyEpoch int64,
	topologyLockID string,
) error {
	if strings.TrimSpace(childUID) == "" {
		return nil
	}
	if r.APIReader == nil {
		return fmt.Errorf("unclaimed child revocation requires an uncached Kubernetes API reader")
	}
	reservationID := reservationID(string(rollout.UID), target.DeviceUID)
	return store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		reservation, exists := ledger.Reservations[reservationID]
		if !exists || reservation.State == topologyrollout.ReservationReserved {
			return nil
		}
		if reservation.State != topologyrollout.ReservationBound &&
			reservation.State != topologyrollout.ReservationGranted &&
			reservation.State != topologyrollout.ReservationRevoked {
			return fmt.Errorf("%w: cannot revoke unclaimed reservation in state %s",
				topologyrollout.ErrInvalidTransition, reservation.State)
		}

		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}
		if err := r.APIReader.Get(ctx, key, &leaf); err != nil {
			return fmt.Errorf("fresh-read bound leaf before ledger revocation: %w", err)
		}
		if string(leaf.UID) != childUID {
			return fmt.Errorf("bound leaf UID %q does not match release proof %q", leaf.UID, childUID)
		}
		if err := validateManagerAdmission(rollout, target, &leaf); err != nil {
			return err
		}
		admission := leaf.Status.ManagerAdmission
		if admission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
			len(leaf.Status.ManagedMutationClaims) != 0 {
			return fmt.Errorf("bound leaf is not manager-revoked without durable mutation claims")
		}
		if admission.PolicyEpoch != policyEpoch || admission.TopologyLockID != topologyLockID ||
			admission.ControlRevision == nil || *admission.ControlRevision < 0 {
			return fmt.Errorf("bound leaf revocation does not match the exact policy epoch, topology lock, and control revision")
		}
		leafRevision := uint64(*admission.ControlRevision)
		if leafRevision < revision {
			return fmt.Errorf("bound leaf revocation control revision %d is older than release revision %d",
				leafRevision, revision)
		}
		return topologyrollout.RevokeUnclaimedAtEpoch(
			ledger, reservationID, topologyLockID, childUID, leafRevision, policyEpoch,
		)
	})
}

// persistLeafTopologyLockID establishes the durable recovery identity before
// any reservation deletion. A later reconcile can therefore replay settlement
// after both the ledger entry and CiscoDevice lock have already disappeared.
func (r *IOSXESoftwareRolloutReconciler) persistLeafTopologyLockID(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	childUID string,
	policyEpoch int64,
	topologyLockID string,
) error {
	if childUID == "" || topologyLockID == "" {
		return nil
	}
	key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}
	return r.patchLeafManagerFields(ctx, key, func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		admission := current.Status.ManagerAdmission
		if string(current.UID) != childUID || admission == nil ||
			admission.CampaignUID != string(rollout.UID) ||
			admission.ReservationID != reservationID(string(rollout.UID), target.DeviceUID) ||
			admission.PolicyEpoch != policyEpoch {
			return fmt.Errorf("leaf admission changed before topology-lock identity binding")
		}
		if admission.TopologyLockID != "" && admission.TopologyLockID != topologyLockID {
			return fmt.Errorf("leaf admission is bound to a different topology-lock acquisition")
		}
		if admission.TopologyLockID == "" {
			admission.TopologyLockID = topologyLockID
			admission.UpdatedAt = metav1.NewTime(r.now())
		}
		return nil
	})
}

func (r *IOSXESoftwareRolloutReconciler) revokeClaimedReservation(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	revision uint64,
) error {
	store := r.ledgerStore(rollout)
	if err := store.Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
		reservation, ok := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)]
		if !ok {
			return fmt.Errorf("%w: claimed leaf has no durable reservation", topologyrollout.ErrLedgerIdentity)
		}
		return topologyrollout.Revoke(ledger, reservation.ID, revision)
	}); err != nil {
		return fmt.Errorf("revoke claimed reservation: %w", err)
	}
	return nil
}

func (r *IOSXESoftwareRolloutReconciler) reconcileDeletion(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	now time.Time,
) (ctrl.Result, error) {
	if !hasExactString(rollout.Finalizers, rolloutSafetyFinalizer) {
		return ctrl.Result{}, nil
	}
	if rollout.Status.FrozenPlan == nil {
		return r.removeRolloutFinalizer(ctx, rollout)
	}
	if rollout.Spec.Control.Revision == int64(1<<63-1) {
		return ctrl.Result{RequeueAfter: rolloutPollInterval}, fmt.Errorf("cannot fence deletion after maximum campaign control revision")
	}
	revision := rollout.Spec.Control.Revision + 1
	// Deletion is an implicit terminal cancellation. Fence every existing leaf
	// before reading policy or ledger state so an unavailable safety dependency
	// cannot leave a previously granted worker free to claim another mutation.
	if err := r.propagateLeafControl(ctx, rollout, false, true, revision, now); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureCancellationFences(ctx, rollout, revision, now); err != nil {
		return ctrl.Result{}, err
	}
	policy, err := topologyrollout.BootstrapAdminPolicy(ctx, r.Client, r.reader(),
		types.NamespacedName{Namespace: r.TopologyPolicyNamespace, Name: r.TopologyPolicyName})
	if err != nil {
		return ctrl.Result{RequeueAfter: rolloutPollInterval}, fmt.Errorf("deletion safety policy unavailable: %w", err)
	}
	if err := r.propagateLeafControl(ctx, rollout, false, true, revision, now); err != nil {
		return ctrl.Result{}, err
	}
	children, err := r.rolloutChildren(ctx, rollout)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, target := range rollout.Status.FrozenPlan.Targets {
		leaf, exists := children[target.ChildName]
		if !exists {
			if err := r.releaseUnclaimedReservation(ctx, rollout, target, "", uint64(revision)); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}
		if err := r.reader().Get(ctx, client.ObjectKeyFromObject(&leaf), &leaf); err != nil {
			return ctrl.Result{}, err
		}
		if leaf.Status.ManagerAdmission != nil &&
			leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled {
			continue
		}
		if len(leaf.Status.ManagedMutationClaims) == 0 && leaf.Status.ManagerAdmission != nil &&
			leaf.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked {
			if err := r.releaseUnclaimedReservation(ctx, rollout, target, string(leaf.UID), uint64(revision),
				leaf.Status.ManagerAdmission.TopologyLockID); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}
		if err := r.revokeClaimedReservation(ctx, rollout, target, uint64(revision)); err != nil {
			return ctrl.Result{}, err
		}
		if !terminalLeafPhase(leaf.Status.Phase) {
			return ctrl.Result{RequeueAfter: rolloutPollInterval}, nil
		}
		summaries := indexTargetSummaries(rollout.Status.Targets)
		summary := summaries[target.DeviceUID]
		settled, gateReason, detail, err := r.trySettleLeaf(ctx, rollout, policy, target, &leaf, summary, now)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !settled {
			phase := opsv1alpha1.IOSXESoftwareRolloutTargetCancelling
			if gateReason == "HealthyPostMutationSoak" {
				phase = opsv1alpha1.IOSXESoftwareRolloutTargetSoaking
			}
			transitionTarget(&summary, phase, gateReason, detail, now)
			summaries[target.DeviceUID] = summary
			return r.patchExecutionStatus(ctx, rollout, summaries, opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling,
				"deletion is fenced; accepted work is still converging", now)
		}
	}
	return r.removeRolloutFinalizer(ctx, rollout)
}

func (r *IOSXESoftwareRolloutReconciler) removeRolloutFinalizer(ctx context.Context, rollout *opsv1alpha1.IOSXESoftwareRollout) (ctrl.Result, error) {
	before := rollout.DeepCopy()
	finalizers := rollout.Finalizers[:0]
	for _, finalizer := range rollout.Finalizers {
		if finalizer != rolloutSafetyFinalizer {
			finalizers = append(finalizers, finalizer)
		}
	}
	rollout.Finalizers = finalizers
	if err := r.Patch(ctx, rollout, client.MergeFrom(before)); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("remove rollout safety finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func hasExactString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func indexTargetSummaries(values []opsv1alpha1.IOSXESoftwareRolloutTargetStatus) map[string]opsv1alpha1.IOSXESoftwareRolloutTargetStatus {
	out := make(map[string]opsv1alpha1.IOSXESoftwareRolloutTargetStatus, len(values))
	for _, value := range values {
		out[value.DeviceUID] = value
	}
	return out
}

func transitionTarget(
	target *opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
	phase opsv1alpha1.IOSXESoftwareRolloutTargetPhase,
	reason, message string,
	now time.Time,
) {
	if target.Phase != phase || target.Reason != reason {
		target.LastTransitionTime = metav1.NewTime(now)
	}
	target.Phase = phase
	target.Reason = truncateRolloutText(reason, 128)
	target.Message = truncateRolloutText(message, 512)
}

func orderedTargetSummaries(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	indexed map[string]opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
	now time.Time,
) []opsv1alpha1.IOSXESoftwareRolloutTargetStatus {
	ordered := make([]opsv1alpha1.IOSXESoftwareRolloutTargetStatus, 0, len(rollout.Status.FrozenPlan.Targets))
	for _, target := range rollout.Status.FrozenPlan.Targets {
		summary, ok := indexed[target.DeviceUID]
		if !ok {
			summary = opsv1alpha1.IOSXESoftwareRolloutTargetStatus{
				DeviceName: target.DeviceName, DeviceUID: target.DeviceUID, LeafName: target.ChildName,
				Phase: opsv1alpha1.IOSXESoftwareRolloutTargetPlanned, LastTransitionTime: metav1.NewTime(now),
			}
		}
		ordered = append(ordered, summary)
	}
	return ordered
}

func rolloutCounts(targets []opsv1alpha1.IOSXESoftwareRolloutTargetStatus) opsv1alpha1.IOSXESoftwareRolloutCounts {
	counts := opsv1alpha1.IOSXESoftwareRolloutCounts{Total: int32(len(targets))}
	for _, target := range targets {
		switch target.Phase {
		case opsv1alpha1.IOSXESoftwareRolloutTargetPlanned,
			opsv1alpha1.IOSXESoftwareRolloutTargetWaitingForAdmission:
			counts.Planned++
		case opsv1alpha1.IOSXESoftwareRolloutTargetAdmitted:
			counts.Admitted++
		case opsv1alpha1.IOSXESoftwareRolloutTargetRunning,
			opsv1alpha1.IOSXESoftwareRolloutTargetSoaking,
			opsv1alpha1.IOSXESoftwareRolloutTargetCancelling:
			counts.InProgress++
		case opsv1alpha1.IOSXESoftwareRolloutTargetSucceeded:
			counts.Succeeded++
		case opsv1alpha1.IOSXESoftwareRolloutTargetFailed:
			counts.Failed++
		case opsv1alpha1.IOSXESoftwareRolloutTargetBlocked:
			counts.Blocked++
		case opsv1alpha1.IOSXESoftwareRolloutTargetCancelled:
			counts.Cancelled++
		}
	}
	return counts
}

func recordPersistedTargetTransitions(
	before, after []opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
) {
	prior := indexTargetSummaries(before)
	for _, target := range after {
		old := prior[target.DeviceUID]
		if old.Phase == target.Phase && old.Reason == target.Reason {
			continue
		}
		topologyrollout.RecordTargetTransition(string(old.Phase), string(target.Phase), target.Reason)
	}
}

func (r *IOSXESoftwareRolloutReconciler) patchExecutionStatus(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	indexed map[string]opsv1alpha1.IOSXESoftwareRolloutTargetStatus,
	phase opsv1alpha1.IOSXESoftwareRolloutPhase,
	message string,
	now time.Time,
	extraConditions ...metav1.Condition,
) (ctrl.Result, error) {
	before := rollout.DeepCopy()
	rollout.Status.ObservedGeneration = rollout.Generation
	rollout.Status.Targets = orderedTargetSummaries(rollout, indexed, now)
	rollout.Status.Counts = rolloutCounts(rollout.Status.Targets)
	rollout.Status.Phase = phase
	rollout.Status.Message = truncateRolloutText(message, 512)
	rollout.Status.Control = r.rolloutControlStatus(ctx, rollout)
	setRolloutCondition(rollout, "Progressing", conditionForPhase(phase), string(phase), rollout.Status.Message, now)
	for _, condition := range extraConditions {
		condition.Type = truncateRolloutText(condition.Type, 63)
		condition.Reason = truncateRolloutText(condition.Reason, 128)
		condition.Message = truncateRolloutText(condition.Message, 512)
		meta.SetStatusCondition(&rollout.Status.Conditions, condition)
	}
	if reflect.DeepEqual(before.Status, rollout.Status) {
		return ctrl.Result{RequeueAfter: rolloutPollInterval}, nil
	}
	if err := r.Status().Patch(ctx, rollout,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("patch rollout execution status: %w", err)
	}
	recordPersistedTargetTransitions(before.Status.Targets, rollout.Status.Targets)
	if before.Status.Phase != phase {
		eventType := corev1.EventTypeNormal
		if phase == opsv1alpha1.IOSXESoftwareRolloutPhaseFailed {
			eventType = corev1.EventTypeWarning
		}
		r.emitRolloutEvent(rollout, eventType, rolloutPhaseEventReason(phase), rollout.Status.Message)
	}
	if phase == opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded ||
		phase == opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled ||
		(phase == opsv1alpha1.IOSXESoftwareRolloutPhaseFailed && rollout.Status.Counts.InProgress == 0) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: rolloutPollInterval}, nil
}

func (r *IOSXESoftwareRolloutReconciler) updateRolloutSummary(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	phase opsv1alpha1.IOSXESoftwareRolloutPhase,
	message string,
	now time.Time,
) (ctrl.Result, error) {
	return r.patchExecutionStatus(ctx, rollout, indexTargetSummaries(rollout.Status.Targets), phase, message, now)
}

func (r *IOSXESoftwareRolloutReconciler) failRollout(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	reason, message string,
	terminal bool,
) (ctrl.Result, error) {
	now := r.now()
	before := rollout.DeepCopy()
	phase := opsv1alpha1.IOSXESoftwareRolloutPhasePaused
	if terminal {
		phase = opsv1alpha1.IOSXESoftwareRolloutPhaseFailed
	}
	rollout.Status.Phase = phase
	rollout.Status.Message = truncateRolloutText(message, 512)
	setRolloutCondition(rollout, "Ready", metav1.ConditionFalse, reason, rollout.Status.Message, now)
	if reflect.DeepEqual(before.Status, rollout.Status) {
		if terminal {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{RequeueAfter: rolloutPollInterval}, nil
	}
	if err := r.Status().Patch(ctx, rollout,
		client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("record rollout failure: %w", err)
	}
	r.emitRolloutEvent(rollout, corev1.EventTypeWarning, reason, rollout.Status.Message)
	if terminal {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: rolloutPollInterval}, nil
}

func setRolloutCondition(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
	now time.Time,
) {
	meta.SetStatusCondition(&rollout.Status.Conditions, metav1.Condition{
		Type: truncateRolloutText(conditionType, 63), Status: status,
		ObservedGeneration: rollout.Generation,
		Reason:             truncateRolloutText(reason, 128), Message: truncateRolloutText(message, 512),
		LastTransitionTime: metav1.NewTime(now),
	})
}

func conditionForPhase(phase opsv1alpha1.IOSXESoftwareRolloutPhase) metav1.ConditionStatus {
	switch phase {
	case opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting,
		opsv1alpha1.IOSXESoftwareRolloutPhaseSoaking,
		opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling:
		return metav1.ConditionTrue
	default:
		return metav1.ConditionFalse
	}
}

func truncateRolloutText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func rolloutPhaseEventReason(phase opsv1alpha1.IOSXESoftwareRolloutPhase) string {
	switch phase {
	case opsv1alpha1.IOSXESoftwareRolloutPhaseAwaitingApproval:
		return "AwaitingApproval"
	case opsv1alpha1.IOSXESoftwareRolloutPhasePaused:
		return "RolloutPaused"
	case opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting:
		return "RolloutExecuting"
	case opsv1alpha1.IOSXESoftwareRolloutPhaseSoaking:
		return "RolloutSoaking"
	case opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling:
		return "RolloutCancelling"
	case opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded:
		return "RolloutSucceeded"
	case opsv1alpha1.IOSXESoftwareRolloutPhaseFailed:
		return "RolloutFailed"
	case opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled:
		return "RolloutCancelled"
	default:
		return "RolloutStatusChanged"
	}
}

func (r *IOSXESoftwareRolloutReconciler) emitRolloutEvent(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	eventType, reason, message string,
) {
	if r.Recorder == nil || rollout == nil {
		return
	}
	r.Recorder.Event(rollout, eventType, truncateRolloutText(reason, 128), truncateRolloutText(message, 512))
}

func (r *IOSXESoftwareRolloutReconciler) rolloutControlStatus(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
) *opsv1alpha1.IOSXESoftwareRolloutControlStatus {
	status := &opsv1alpha1.IOSXESoftwareRolloutControlStatus{
		RequestedRevision: rollout.Spec.Control.Revision,
		EffectiveRevision: rollout.Spec.Control.Revision,
		Cancelled:         rollout.Spec.Control.Cancel && rollout.Status.Phase == opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled,
	}
	children, err := r.rolloutChildren(ctx, rollout)
	if err != nil {
		status.EffectiveRevision = 0
		status.PausePending = rollout.Spec.Control.Pause
		status.CancellationPending = rollout.Spec.Control.Cancel
		return status
	}
	for _, child := range children {
		if rollout.Spec.Control.Cancel && child.Status.ManagerAdmission != nil &&
			(child.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionSettled ||
				(len(child.Status.ManagedMutationClaims) == 0 && child.Status.ManagerAdmission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked)) {
			// Settled admission already proves the outcome and reservation release.
			// For an unclaimed revoked leaf, the manager's resourceVersion CAS is
			// itself the durable claim fence; neither case needs a later worker ack.
			continue
		}
		worker := child.Status.WorkerControl
		if worker == nil || worker.ObservedControlRevision < rollout.Spec.Control.Revision {
			status.EffectiveRevision = 0
			status.PausePending = rollout.Spec.Control.Pause
			status.CancellationPending = rollout.Spec.Control.Cancel
			continue
		}
		if rollout.Spec.Control.Pause && worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlPaused &&
			worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlClaimed && worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlSettled {
			status.PausePending = true
		}
		if rollout.Spec.Control.Cancel && worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlCancelled &&
			worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlSettled {
			status.CancellationPending = true
		}
	}
	if status.PausePending || status.CancellationPending {
		status.EffectiveRevision = 0
	}
	status.Paused = rollout.Spec.Control.Pause && !status.PausePending
	return status
}
