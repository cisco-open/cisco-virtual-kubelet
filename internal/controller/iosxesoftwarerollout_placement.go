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
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const drainPlacementDomain = "cisco.vk/workload-drain-placement/v1"

// drainPlacementEvidence intentionally contains only hard scheduler intent.
// Preferred terms remain scheduler hints and do not authorize an eviction.
type drainPlacementEvidence struct {
	Domain                    string                            `json:"domain"`
	SchedulerName             string                            `json:"schedulerName"`
	NodeSelector              map[string]string                 `json:"nodeSelector,omitempty"`
	RequiredNodeAffinity      *corev1.NodeSelector              `json:"requiredNodeAffinity,omitempty"`
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

func drainPlacementHash(spec *corev1.PodSpec) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("Pod spec is missing")
	}
	schedulerName := spec.SchedulerName
	if schedulerName == "" {
		schedulerName = corev1.DefaultSchedulerName
	}
	var required *corev1.NodeSelector
	if spec.Affinity != nil && spec.Affinity.NodeAffinity != nil {
		required = spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	}
	evidence := drainPlacementEvidence{
		Domain: drainPlacementDomain, SchedulerName: schedulerName,
		NodeSelector: spec.NodeSelector, RequiredNodeAffinity: required,
		TopologySpreadConstraints: spec.TopologySpreadConstraints,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return "", fmt.Errorf("encode workload placement: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// rejectUnqualifiedSchedulingGroup performs an uncached, unstructured read so
// a binary compiled with an older core/v1 Pod type cannot silently discard a
// newer native TAS schedulingGroup field. Group-aware drain is not qualified;
// any current group membership therefore blocks before Pod protection or
// Eviction. The exact UID/resourceVersion comparison also closes the list/get
// race instead of treating a replacement Pod as ungrouped.
func (r *IOSXESoftwareRolloutReconciler) rejectUnqualifiedSchedulingGroup(
	ctx context.Context,
	pod *corev1.Pod,
) error {
	if pod == nil || pod.UID == "" {
		return fmt.Errorf("cannot inspect scheduling group for an unidentified Pod")
	}
	raw := &unstructured.Unstructured{}
	raw.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(pod), raw); err != nil {
		return fmt.Errorf("read current raw Pod for scheduling-group guard: %w", err)
	}
	if raw.GetUID() != pod.UID {
		return fmt.Errorf("Pod incarnation changed during scheduling-group guard")
	}
	if pod.ResourceVersion != "" && raw.GetResourceVersion() != pod.ResourceVersion {
		return fmt.Errorf("Pod changed during scheduling-group guard")
	}
	group, found, err := unstructured.NestedFieldNoCopy(raw.Object, "spec", "schedulingGroup")
	if err != nil {
		return fmt.Errorf("decode Pod schedulingGroup: %w", err)
	}
	if found && group != nil {
		return fmt.Errorf("native scheduling-group workload is not qualified for managed drain")
	}
	return nil
}

func hasHardDrainPlacement(spec *corev1.PodSpec) bool {
	if spec == nil {
		return false
	}
	if len(spec.NodeSelector) != 0 ||
		(spec.Affinity != nil && spec.Affinity.NodeAffinity != nil &&
			spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil) {
		return true
	}
	for i := range spec.TopologySpreadConstraints {
		if spec.TopologySpreadConstraints[i].WhenUnsatisfiable == corev1.DoNotSchedule {
			return true
		}
	}
	return false
}

// validateDrainPlacementFeasibility is deliberately conservative. It does not
// reserve capacity or replace kube-scheduler; it proves only that the current
// API snapshot contains at least one other Ready, schedulable Node that keeps
// every hard selector/affinity/spread rule and has sufficient allocatable
// resources. The normal replacement-readiness gate remains authoritative.
func (r *IOSXESoftwareRolloutReconciler) validateDrainPlacementFeasibility(
	ctx context.Context,
	pod *corev1.Pod,
	placementHash string,
) error {
	if pod == nil || !hasHardDrainPlacement(&pod.Spec) {
		return nil
	}
	actualHash, err := drainPlacementHash(&pod.Spec)
	if err != nil || actualHash != placementHash {
		return fmt.Errorf("hard placement changed before feasibility evaluation")
	}
	var nodes corev1.NodeList
	if err := r.reader().List(ctx, &nodes); err != nil {
		return fmt.Errorf("list Nodes for hard-placement feasibility: %w", err)
	}
	var topologyPods corev1.PodList
	if hasHardTopologySpread(&pod.Spec) {
		if err := r.reader().List(ctx, &topologyPods, client.InNamespace(pod.Namespace)); err != nil {
			return fmt.Errorf("list namespace Pods for hard topology-spread feasibility: %w", err)
		}
	}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if node.Name == pod.Spec.NodeName || !drainCandidateNode(pod, node) {
			continue
		}
		var nodePods corev1.PodList
		if err := r.reader().List(ctx, &nodePods, client.MatchingFields{rolloutPodNodeNameIndex: node.Name}); err != nil {
			return fmt.Errorf("list Pods on replacement Node %q: %w", node.Name, err)
		}
		if !drainNodeHasCapacity(pod, node, nodePods.Items) {
			continue
		}
		ok, err := drainTopologySpreadAllows(pod, node, nodes.Items, topologyPods.Items)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("hard placement has no currently feasible replacement Node")
}

func hasHardTopologySpread(spec *corev1.PodSpec) bool {
	if spec == nil {
		return false
	}
	for i := range spec.TopologySpreadConstraints {
		if spec.TopologySpreadConstraints[i].WhenUnsatisfiable == corev1.DoNotSchedule {
			return true
		}
	}
	return false
}

func drainCandidateNode(pod *corev1.Pod, node *corev1.Node) bool {
	if pod == nil || node == nil || node.Spec.Unschedulable || !drainNodeReady(node) {
		return false
	}
	return drainNodeMatchesSelectorAffinity(pod, node) && drainNodeTaintsTolerated(pod, node)
}

func drainNodeMatchesSelectorAffinity(pod *corev1.Pod, node *corev1.Node) bool {
	for key, value := range pod.Spec.NodeSelector {
		if node.Labels[key] != value {
			return false
		}
	}
	if pod.Spec.Affinity != nil && pod.Spec.Affinity.NodeAffinity != nil {
		if !drainNodeSelectorMatches(node, pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution) {
			return false
		}
	}
	return true
}

func drainNodeTaintsTolerated(pod *corev1.Pod, node *corev1.Node) bool {
	for i := range node.Spec.Taints {
		taint := &node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if !drainPodToleratesTaint(pod, taint) {
			return false
		}
	}
	return true
}

func drainNodeReady(node *corev1.Node) bool {
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeReady {
			return node.Status.Conditions[i].Status == corev1.ConditionTrue
		}
	}
	return false
}

func drainPodToleratesTaint(pod *corev1.Pod, taint *corev1.Taint) bool {
	for i := range pod.Spec.Tolerations {
		toleration := &pod.Spec.Tolerations[i]
		if toleration.Effect != "" && toleration.Effect != taint.Effect {
			continue
		}
		if toleration.Key != taint.Key {
			continue
		}
		if toleration.Operator == corev1.TolerationOpExists ||
			(toleration.Operator == corev1.TolerationOpEqual && toleration.Value == taint.Value) {
			return true
		}
	}
	return false
}

func drainNodeSelectorMatches(node *corev1.Node, selector *corev1.NodeSelector) bool {
	if selector == nil {
		return true
	}
	for i := range selector.NodeSelectorTerms {
		term := &selector.NodeSelectorTerms[i]
		if drainNodeSelectorTermMatches(node, term) {
			return true
		}
	}
	return false
}

func drainNodeSelectorTermMatches(node *corev1.Node, term *corev1.NodeSelectorTerm) bool {
	if node == nil || term == nil || (len(term.MatchExpressions) == 0 && len(term.MatchFields) == 0) {
		return false
	}
	for i := range term.MatchExpressions {
		if !drainSelectorRequirementMatches(node.Labels, &term.MatchExpressions[i]) {
			return false
		}
	}
	for i := range term.MatchFields {
		requirement := &term.MatchFields[i]
		if requirement.Key != "metadata.name" ||
			!drainSelectorRequirementMatches(map[string]string{"metadata.name": node.Name}, requirement) {
			return false
		}
	}
	return true
}

func drainSelectorRequirementMatches(values map[string]string, requirement *corev1.NodeSelectorRequirement) bool {
	value, present := values[requirement.Key]
	switch requirement.Operator {
	case corev1.NodeSelectorOpIn:
		return present && stringIn(value, requirement.Values)
	case corev1.NodeSelectorOpNotIn:
		return !present || !stringIn(value, requirement.Values)
	case corev1.NodeSelectorOpExists:
		return present
	case corev1.NodeSelectorOpDoesNotExist:
		return !present
	case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
		if !present || len(requirement.Values) != 1 {
			return false
		}
		actual, actualErr := strconv.ParseInt(value, 10, 64)
		expected, expectedErr := strconv.ParseInt(requirement.Values[0], 10, 64)
		if actualErr != nil || expectedErr != nil {
			return false
		}
		if requirement.Operator == corev1.NodeSelectorOpGt {
			return actual > expected
		}
		return actual < expected
	default:
		return false
	}
}

func stringIn(value string, values []string) bool {
	for i := range values {
		if values[i] == value {
			return true
		}
	}
	return false
}

func drainPodRequests(pod *corev1.Pod) corev1.ResourceList {
	requests := corev1.ResourceList{}
	add := func(target corev1.ResourceList, source corev1.ResourceList) {
		for name, quantity := range source {
			current := target[name]
			current.Add(quantity)
			target[name] = current
		}
	}
	for i := range pod.Spec.Containers {
		add(requests, pod.Spec.Containers[i].Resources.Requests)
	}
	initMax := corev1.ResourceList{}
	for i := range pod.Spec.InitContainers {
		for name, quantity := range pod.Spec.InitContainers[i].Resources.Requests {
			current := initMax[name]
			if current.Cmp(quantity) < 0 {
				initMax[name] = quantity.DeepCopy()
			}
		}
	}
	for name, quantity := range initMax {
		if current := requests[name]; current.Cmp(quantity) < 0 {
			requests[name] = quantity.DeepCopy()
		}
	}
	add(requests, pod.Spec.Overhead)
	if pod.Spec.Resources != nil {
		for name, quantity := range pod.Spec.Resources.Requests {
			if current := requests[name]; current.Cmp(quantity) < 0 {
				requests[name] = quantity.DeepCopy()
			}
		}
	}
	return requests
}

func drainNodeHasCapacity(pod *corev1.Pod, node *corev1.Node, pods []corev1.Pod) bool {
	used := corev1.ResourceList{}
	activePods := int64(0)
	for i := range pods {
		current := &pods[i]
		if current.Spec.NodeName != node.Name || current.UID == pod.UID ||
			(current.DeletionTimestamp == nil && (current.Status.Phase == corev1.PodSucceeded || current.Status.Phase == corev1.PodFailed)) {
			continue
		}
		activePods++
		for name, quantity := range drainPodRequests(current) {
			total := used[name]
			total.Add(quantity)
			used[name] = total
		}
	}
	if capacity, ok := node.Status.Allocatable[corev1.ResourcePods]; ok && activePods+1 > capacity.Value() {
		return false
	}
	for name, requested := range drainPodRequests(pod) {
		capacity, ok := node.Status.Allocatable[name]
		if !ok {
			return false
		}
		available := capacity.DeepCopy()
		available.Sub(used[name])
		if available.Cmp(requested) < 0 {
			return false
		}
	}
	return true
}

func drainTopologySpreadAllows(
	pod *corev1.Pod,
	candidate *corev1.Node,
	nodes []corev1.Node,
	pods []corev1.Pod,
) (bool, error) {
	for i := range pod.Spec.TopologySpreadConstraints {
		constraint := &pod.Spec.TopologySpreadConstraints[i]
		if constraint.WhenUnsatisfiable != corev1.DoNotSchedule {
			continue
		}
		candidateDomain, ok := candidate.Labels[constraint.TopologyKey]
		if !ok || candidateDomain == "" {
			return false, nil
		}
		selectorSpec := constraint.LabelSelector.DeepCopy()
		if selectorSpec == nil && len(constraint.MatchLabelKeys) != 0 {
			return false, fmt.Errorf("hard topology spread uses matchLabelKeys without labelSelector")
		}
		if selectorSpec != nil {
			if selectorSpec.MatchLabels == nil {
				selectorSpec.MatchLabels = map[string]string{}
			}
			for _, key := range constraint.MatchLabelKeys {
				if _, duplicate := selectorSpec.MatchLabels[key]; duplicate {
					return false, fmt.Errorf("hard topology-spread key %q appears in labelSelector and matchLabelKeys", key)
				}
				if value, present := pod.Labels[key]; present {
					selectorSpec.MatchLabels[key] = value
				}
			}
		}
		selector, err := metav1.LabelSelectorAsSelector(selectorSpec)
		if err != nil {
			return false, fmt.Errorf("invalid hard topology-spread selector: %w", err)
		}
		eligibleDomains := map[string]struct{}{}
		nodesByName := make(map[string]*corev1.Node, len(nodes))
		for n := range nodes {
			node := &nodes[n]
			nodesByName[node.Name] = node
			if node.Name == pod.Spec.NodeName || !drainNodeReady(node) || node.Spec.Unschedulable {
				continue
			}
			if constraint.NodeAffinityPolicy == nil || *constraint.NodeAffinityPolicy == corev1.NodeInclusionPolicyHonor {
				if !drainNodeMatchesSelectorAffinity(pod, node) {
					continue
				}
			}
			if constraint.NodeTaintsPolicy != nil && *constraint.NodeTaintsPolicy == corev1.NodeInclusionPolicyHonor {
				if !drainNodeTaintsTolerated(pod, node) {
					continue
				}
			}
			domain, present := node.Labels[constraint.TopologyKey]
			if present && domain != "" {
				eligibleDomains[domain] = struct{}{}
			}
		}
		if _, eligible := eligibleDomains[candidateDomain]; !eligible {
			return false, nil
		}
		counts := make(map[string]int32, len(eligibleDomains))
		for domain := range eligibleDomains {
			counts[domain] = 0
		}
		for p := range pods {
			current := &pods[p]
			if current.UID == pod.UID || current.Namespace != pod.Namespace || current.Spec.NodeName == "" ||
				(current.DeletionTimestamp == nil && (current.Status.Phase == corev1.PodSucceeded || current.Status.Phase == corev1.PodFailed)) ||
				!selector.Matches(labels.Set(current.Labels)) {
				continue
			}
			if node := nodesByName[current.Spec.NodeName]; node != nil {
				domain := node.Labels[constraint.TopologyKey]
				if _, eligible := eligibleDomains[domain]; eligible {
					counts[domain]++
				}
			}
		}
		minimum := int32(0)
		if constraint.MinDomains == nil || int32(len(eligibleDomains)) >= *constraint.MinDomains {
			minimum = -1
			for _, count := range counts {
				if minimum < 0 || count < minimum {
					minimum = count
				}
			}
			if minimum < 0 {
				minimum = 0
			}
		}
		if counts[candidateDomain]+1-minimum > constraint.MaxSkew {
			return false, nil
		}
	}
	return true, nil
}
