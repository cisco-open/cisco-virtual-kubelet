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
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestRolloutDependencyIndexesScopeReconciliations(t *testing.T) {
	first := indexedRollout("lab-a", "rollout-a", "edge-a", "node-a", "source-a")
	second := indexedRollout("lab-b", "rollout-b", "edge-a", "node-a", "source-a")
	unplanned := &opsv1alpha1.IOSXESoftwareRollout{ObjectMeta: metav1.ObjectMeta{Namespace: "lab-a", Name: "unplanned"}}

	scheme := runtime.NewScheme()
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&opsv1alpha1.IOSXESoftwareRollout{}, rolloutTargetDeviceNameIndex, rolloutTargetDeviceNameIndexValues).
		WithIndex(&opsv1alpha1.IOSXESoftwareRollout{}, rolloutTargetNodeNameIndex, rolloutTargetNodeNameIndexValues).
		WithIndex(&opsv1alpha1.IOSXESoftwareRollout{}, rolloutSourceSecretNameIndex, rolloutSourceSecretNameIndexValues).
		WithObjects(first, second, unplanned).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient}

	assertRolloutRequests(t,
		reconciler.rolloutRequestsByField(context.Background(), "lab-a", rolloutTargetDeviceNameIndex, "edge-a"),
		"lab-a/rollout-a")
	assertRolloutRequests(t,
		reconciler.rolloutRequestsByField(context.Background(), "lab-a", rolloutSourceSecretNameIndex, "source-a"),
		"lab-a/rollout-a")
	assertRolloutRequests(t,
		reconciler.rolloutRequestsByField(context.Background(), "", rolloutTargetNodeNameIndex, "node-a"),
		"lab-a/rollout-a", "lab-b/rollout-b")
	if requests := reconciler.rolloutRequestsByField(context.Background(), "lab-a", rolloutTargetDeviceNameIndex, ""); len(requests) != 0 {
		t.Fatalf("empty index value returned requests: %#v", requests)
	}
}

func TestCachedClientWorkloadCheckUsesPodNodeIndex(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	running := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "running"},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	other := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "other"},
		Spec:       corev1.PodSpec{NodeName: "node-b"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&corev1.Pod{}).
		WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
		WithObjects(running, other).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient}
	if err := reconciler.ensureNoRunningWorkloads(context.Background(), "node-a"); !errors.Is(err, errWorkloadsRunning) {
		t.Fatalf("ensureNoRunningWorkloads(node-a) error = %v, want errWorkloadsRunning", err)
	}
	if err := reconciler.ensureNoRunningWorkloads(context.Background(), "node-c"); err != nil {
		t.Fatalf("ensureNoRunningWorkloads(node-c) error = %v", err)
	}

	running.Status.Phase = corev1.PodSucceeded
	if err := apiClient.Status().Update(context.Background(), running); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ensureNoRunningWorkloads(context.Background(), "node-a"); err != nil {
		t.Fatalf("ensureNoRunningWorkloads(completed node-a) error = %v", err)
	}
}

func indexedRollout(namespace, name, deviceName, nodeName, secretName string) *opsv1alpha1.IOSXESoftwareRollout {
	return &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{
			FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
				Source:  opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{SecretName: secretName},
				Targets: []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{{DeviceName: deviceName, NodeName: nodeName}},
			},
		},
	}
}

func assertRolloutRequests(t *testing.T, requests []reconcile.Request, expected ...string) {
	t.Helper()
	got := make([]string, 0, len(requests))
	for _, request := range requests {
		got = append(got, request.NamespacedName.String())
	}
	sort.Strings(got)
	sort.Strings(expected)
	if len(got) != len(expected) {
		t.Fatalf("requests = %v, want %v", got, expected)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Fatalf("requests = %v, want %v", got, expected)
		}
	}
}
