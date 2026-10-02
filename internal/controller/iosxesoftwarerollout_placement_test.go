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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDrainPlacementHashBindsHardIntent(t *testing.T) {
	t.Parallel()
	spec := &corev1.PodSpec{
		NodeSelector: map[string]string{"type": "virtual-kubelet"},
		Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: "topology.cisco.vk/site", Operator: corev1.NodeSelectorOpIn, Values: []string{"lab"},
				}},
			}}},
		}},
		TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
			MaxSkew: 1, TopologyKey: "topology.cisco.vk/rack", WhenUnsatisfiable: corev1.DoNotSchedule,
		}},
	}
	original, err := drainPlacementHash(spec)
	if err != nil {
		t.Fatal(err)
	}
	copy := spec.DeepCopy()
	copy.NodeSelector["type"] = "worker"
	changed, err := drainPlacementHash(copy)
	if err != nil {
		t.Fatal(err)
	}
	if original == changed {
		t.Fatal("node-selector drift did not change placement digest")
	}
	template := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true", "app": "edge"}},
		Spec:       *spec,
	}
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}}
	if err := validateRecoveredDrainTemplate("Deployment", selector, template.DeepCopy(), original); err != nil {
		t.Fatalf("unchanged frozen placement was rejected: %v", err)
	}
	template.Spec = *copy
	if err := validateRecoveredDrainTemplate("Deployment", selector, template.DeepCopy(), original); err == nil {
		t.Fatal("changed placement was accepted during recovery")
	}
}

func TestDrainHardPlacementRequiresAlternativeCapacity(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge-0", UID: types.UID("pod-uid"), Labels: map[string]string{"app": "edge"}},
		Spec: corev1.PodSpec{
			NodeName: "switch-a", NodeSelector: map[string]string{"type": "virtual-kubelet"},
			Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
			}}}},
		},
	}
	target := drainReadyNode("switch-a", "lab", "rack-a", "4", "8Gi")
	candidate := drainReadyNode("switch-b", "lab", "rack-b", "1", "1Gi")
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
		WithObjects(target, candidate, pod).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	hash, err := drainPlacementHash(&pod.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.validateDrainPlacementFeasibility(context.Background(), pod, hash); err != nil {
		t.Fatalf("eligible alternative was rejected: %v", err)
	}

	current := &corev1.Node{}
	if err := kubeClient.Get(context.Background(), types.NamespacedName{Name: candidate.Name}, current); err != nil {
		t.Fatal(err)
	}
	current.Status.Allocatable[corev1.ResourceCPU] = resource.MustParse("50m")
	if err := kubeClient.Status().Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if err := r.validateDrainPlacementFeasibility(context.Background(), pod, hash); err == nil ||
		!strings.Contains(err.Error(), "no currently feasible replacement") {
		t.Fatalf("capacity exhaustion error = %v", err)
	}
}

func TestDrainRequiredNodeAffinityAndTaints(t *testing.T) {
	t.Parallel()
	node := drainReadyNode("switch-b", "lab", "rack-b", "1", "1Gi")
	node.Labels["generation"] = "9300"
	node.Spec.Taints = []corev1.Taint{{Key: "dedicated", Value: "network", Effect: corev1.TaintEffectNoSchedule}}
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		NodeSelector: map[string]string{"type": "virtual-kubelet"},
		Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{Key: "topology.cisco.vk/site", Operator: corev1.NodeSelectorOpIn, Values: []string{"lab"}},
						{Key: "generation", Operator: corev1.NodeSelectorOpGt, Values: []string{"9200"}},
					},
				}},
			},
		}},
	}}
	if drainCandidateNode(pod, node) {
		t.Fatal("untolerated candidate taint was ignored")
	}
	pod.Spec.Tolerations = []corev1.Toleration{{
		Key: "dedicated", Operator: corev1.TolerationOpEqual, Value: "network", Effect: corev1.TaintEffectNoSchedule,
	}}
	if !drainCandidateNode(pod, node) {
		t.Fatal("matching selector, required affinity, and exact toleration were rejected")
	}
	pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[1].Values[0] = "9400"
	if drainCandidateNode(pod, node) {
		t.Fatal("failed numeric node-affinity requirement was accepted")
	}
}

func TestDrainHardTopologySpreadRejectsUnavailableDomain(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}}
	minDomains := int32(2)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge-a", UID: types.UID("pod-a"), Labels: map[string]string{"app": "edge"}},
		Spec: corev1.PodSpec{
			NodeName: "switch-a", NodeSelector: map[string]string{"type": "virtual-kubelet"},
			TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
				MaxSkew: 1, MinDomains: &minDomains, TopologyKey: "topology.cisco.vk/site", WhenUnsatisfiable: corev1.DoNotSchedule, LabelSelector: selector,
			}},
		},
	}
	target := drainReadyNode("switch-a", "site-a", "rack-a", "1", "1Gi")
	candidate := drainReadyNode("switch-b", "site-b", "rack-b", "1", "1Gi")
	existing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge-b", UID: types.UID("pod-b"), Labels: map[string]string{"app": "edge"}},
		Spec:       corev1.PodSpec{NodeName: candidate.Name}, Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
		WithObjects(target, candidate, pod, existing).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	hash, _ := drainPlacementHash(&pod.Spec)
	if err := r.validateDrainPlacementFeasibility(context.Background(), pod, hash); err == nil {
		t.Fatal("hard spread admitted a replacement that would exceed maxSkew")
	}
}

func TestDrainRawSchedulingGroupFailsClosed(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	raw := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"namespace": "apps", "name": "edge", "uid": "pod-uid", "resourceVersion": "10"},
		"spec":     map[string]any{"schedulingGroup": map[string]any{"podGroupName": "edge-group"}},
	}}
	raw.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(raw).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: &rawPodReader{Reader: kubeClient, raw: raw}}
	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge", UID: types.UID("pod-uid"), ResourceVersion: "10",
	}}
	if err := r.rejectUnqualifiedSchedulingGroup(context.Background(), observed); err == nil ||
		!strings.Contains(err.Error(), "scheduling-group") {
		t.Fatalf("group guard error = %v", err)
	}

	delete(raw.Object["spec"].(map[string]any), "schedulingGroup")
	r.APIReader = &rawPodReader{Reader: kubeClient, raw: raw}
	if err := r.rejectUnqualifiedSchedulingGroup(context.Background(), observed); err != nil {
		t.Fatalf("ordinary Pod was rejected: %v", err)
	}
}

func TestDrainRawSchedulingGroupFailsClosedOnCurrentObjectRaces(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	observed := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge", UID: types.UID("pod-uid"), ResourceVersion: "10",
	}}
	tests := []struct {
		name    string
		rawUID  types.UID
		rawRV   string
		readErr error
		want    string
	}{
		{name: "replacement incarnation", rawUID: types.UID("replacement-uid"), rawRV: "10", want: "incarnation changed"},
		{name: "membership update race", rawUID: observed.UID, rawRV: "11", want: "changed during scheduling-group guard"},
		{name: "served object unavailable", readErr: errors.New("Pod API unavailable"), want: "read current raw Pod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Pod",
				"metadata": map[string]any{
					"namespace": "apps", "name": "edge", "uid": string(tt.rawUID), "resourceVersion": tt.rawRV,
				},
				"spec": map[string]any{},
			}}
			raw.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(raw).Build()
			r := &IOSXESoftwareRolloutReconciler{
				Client:    kubeClient,
				APIReader: &rawPodReader{Reader: kubeClient, raw: raw, err: tt.readErr},
			}
			err := r.rejectUnqualifiedSchedulingGroup(context.Background(), observed)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("guard error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

type rawPodReader struct {
	client.Reader
	raw *unstructured.Unstructured
	err error
}

func (r *rawPodReader) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if r.err != nil {
		return r.err
	}
	if raw, ok := object.(*unstructured.Unstructured); ok && key.Namespace == r.raw.GetNamespace() && key.Name == r.raw.GetName() {
		raw.Object = r.raw.DeepCopy().Object
		return nil
	}
	return r.Reader.Get(ctx, key, object, options...)
}

func drainReadyNode(name, site, rack, cpu, memory string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
			"type": "virtual-kubelet", "topology.cisco.vk/site": site, "topology.cisco.vk/rack": rack,
		}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods: resource.MustParse("16"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}
