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
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

func TestSnapshotDrainPodsFreezesEligibleNativeWorkloadAndPDB(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	controller := true
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "rs-uid", Generation: 3},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true", "app": "edge"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example.invalid/app"}}},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "pod-uid",
			Labels: map[string]string{drainSafeLabel: "true", "app": "edge"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Name: replicaSet.Name,
				UID: replicaSet.UID, Controller: &controller,
			}},
		},
		Spec: corev1.PodSpec{
			NodeName: "switch-a", Containers: []corev1.Container{{Name: "app", Image: "example.invalid/app"}},
			Volumes: []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "pdb-uid", Generation: 2},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}}},
		Status: policyv1.PodDisruptionBudgetStatus{
			ObservedGeneration: 2, DisruptionsAllowed: 1, CurrentHealthy: 1, DesiredHealthy: 1, ExpectedPods: 1,
		},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
		WithObjects(replicaSet, pod, pdb).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	rollout := drainTestRollout()

	got, err := r.snapshotDrainPods(context.Background(), rollout, opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{NodeName: "switch-a"})
	if err != nil {
		t.Fatalf("snapshotDrainPods() error = %v", err)
	}
	if len(got) != 1 || got[0].UID != "pod-uid" || got[0].Controller.UID != "rs-uid" ||
		len(got[0].PDBs) != 1 || got[0].PDBs[0].UID != "pdb-uid" {
		t.Fatalf("snapshotDrainPods() = %#v", got)
	}
	if err := workloaddrain.VerifyEligibilityHash(&got[0]); err != nil {
		t.Fatalf("snapshot eligibility hash is not authoritative: %v", err)
	}
}

func TestSnapshotDrainPodsRejectsNonportableVolume(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	controller := true
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "rs-uid", Generation: 1},
		Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "image"}}},
		}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge-0", UID: "pod-uid",
			Labels:          map[string]string{drainSafeLabel: "true", "app": "edge"},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Name: "edge", UID: "rs-uid", Controller: &controller}}},
		Spec: corev1.PodSpec{NodeName: "switch-a", Containers: []corev1.Container{{Name: "app", Image: "image"}},
			Volumes: []corev1.Volume{{Name: "state", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "state"}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "pdb-uid", Generation: 1},
		Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}}},
		Status:     policyv1.PodDisruptionBudgetStatus{ObservedGeneration: 1, DisruptionsAllowed: 1, CurrentHealthy: 1, DesiredHealthy: 1, ExpectedPods: 1},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
		WithObjects(replicaSet, pod, pdb).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	_, err := r.snapshotDrainPods(context.Background(), drainTestRollout(), opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{NodeName: "switch-a"})
	if !errors.Is(err, errDrainSafetyBlocked) {
		t.Fatalf("snapshotDrainPods() error = %v, want errDrainSafetyBlocked", err)
	}
}

func TestSnapshotDrainPodsRejectsWildcardMaintenanceTolerationOnPodOrTemplate(t *testing.T) {
	t.Parallel()
	controller := true
	for _, location := range []string{"Pod", "ReplicaSet template"} {
		t.Run(location, func(t *testing.T) {
			scheme := drainTestScheme(t)
			wildcard := corev1.Toleration{Operator: corev1.TolerationOpExists}
			replicaSet := &appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "rs-uid", Generation: 1},
				Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true", "app": "edge"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "image"}}},
				}},
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "apps", Name: "edge-0", UID: "pod-uid",
					Labels: map[string]string{drainSafeLabel: "true", "app": "edge"},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Name: replicaSet.Name,
						UID: replicaSet.UID, Controller: &controller,
					}},
				},
				Spec:   corev1.PodSpec{NodeName: "switch-a", Containers: []corev1.Container{{Name: "app", Image: "image"}}},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			}
			if location == "Pod" {
				pod.Spec.Tolerations = []corev1.Toleration{wildcard}
			} else {
				replicaSet.Spec.Template.Spec.Tolerations = []corev1.Toleration{wildcard}
			}
			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "pdb-uid", Generation: 1},
				Spec: policyv1.PodDisruptionBudgetSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}},
				},
				Status: policyv1.PodDisruptionBudgetStatus{
					ObservedGeneration: 1, DisruptionsAllowed: 1, CurrentHealthy: 1, DesiredHealthy: 1, ExpectedPods: 1,
				},
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).
				WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
				WithObjects(replicaSet, pod, pdb).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
			_, err := r.snapshotDrainPods(context.Background(), drainTestRollout(),
				opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{NodeName: "switch-a"})
			if !errors.Is(err, errDrainSafetyBlocked) {
				t.Fatalf("snapshotDrainPods() error = %v, want wildcard toleration safety block", err)
			}
		})
	}
}

func TestValidateDrainPodSpecRejectsHardReplacementPlacement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		spec corev1.PodSpec
	}{
		{name: "node selector", spec: corev1.PodSpec{NodeSelector: map[string]string{"kubernetes.io/hostname": "switch-a"}}},
		{name: "required node affinity", spec: corev1.PodSpec{Affinity: &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{}},
		}}},
		{name: "required pod affinity", spec: corev1.PodSpec{Affinity: &corev1.Affinity{
			PodAffinity: &corev1.PodAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{TopologyKey: "topology.kubernetes.io/zone"}}},
		}}},
		{name: "required pod anti-affinity", spec: corev1.PodSpec{Affinity: &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{TopologyKey: "topology.kubernetes.io/zone"}}},
		}}},
		{name: "hard topology spread", spec: corev1.PodSpec{TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
			TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: corev1.DoNotSchedule,
		}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDrainPodSpec(&test.spec); err == nil {
				t.Fatal("hard replacement placement was accepted")
			}
		})
	}
	soft := &corev1.PodSpec{
		Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{Weight: 1}},
		}},
		TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
			TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: corev1.ScheduleAnyway,
		}},
	}
	if err := validateDrainPodSpec(soft); err != nil {
		t.Fatalf("soft portable placement was rejected: %v", err)
	}
}

func TestSnapshotDrainPodsRejectsUnsupportedControllerAndMultiplePDBs(t *testing.T) {
	t.Parallel()
	controller := true
	tests := []struct {
		name      string
		ownerKind string
		pdbs      int
	}{
		{name: "StatefulSet is not yet qualified", ownerKind: "StatefulSet", pdbs: 1},
		{name: "upstream eviction rejects multiple PDBs", ownerKind: "ReplicaSet", pdbs: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := drainTestScheme(t)
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "apps", Name: "edge-0", UID: "pod-uid",
					Labels: map[string]string{drainSafeLabel: "true", "app": "edge"},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: appsv1.SchemeGroupVersion.String(), Kind: test.ownerKind,
						Name: "edge", UID: "controller-uid", Controller: &controller,
					}},
				},
				Spec:   corev1.PodSpec{NodeName: "switch-a", Containers: []corev1.Container{{Name: "app", Image: "image"}}},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			}
			objects := []client.Object{pod}
			if test.ownerKind == "ReplicaSet" {
				objects = append(objects, &appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "controller-uid", Generation: 1},
					Spec: appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "image"}}},
					}},
				})
			}
			for i := 0; i < test.pdbs; i++ {
				objects = append(objects, &policyv1.PodDisruptionBudget{
					ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge-" + string(rune('a'+i)), UID: types.UID("pdb-" + string(rune('a'+i))), Generation: 1},
					Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}}},
					Status: policyv1.PodDisruptionBudgetStatus{
						ObservedGeneration: 1, DisruptionsAllowed: 1, CurrentHealthy: 1, DesiredHealthy: 1, ExpectedPods: 1,
					},
				})
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).
				WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
				WithObjects(objects...).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
			_, err := r.snapshotDrainPods(context.Background(), drainTestRollout(),
				opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{NodeName: "switch-a"})
			if !errors.Is(err, errDrainSafetyBlocked) {
				t.Fatalf("snapshotDrainPods() error = %v, want errDrainSafetyBlocked", err)
			}
		})
	}
}

func TestRecoveringDrainCompletesSelectedAndNeverEvictedPods(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid",
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: "11111111-1111-4111-8111-111111111111"},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	selectedPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "selected", UID: "selected-uid",
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: "11111111-1111-4111-8111-111111111111"},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			SessionToken: "11111111-1111-4111-8111-111111111111", State: opsv1alpha1.UpgradeManagerDrainRecovering,
			Pods: []opsv1alpha1.UpgradeDrainPodStatus{
				{Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID), Phase: opsv1alpha1.UpgradeDrainPodProtected},
				{Namespace: selectedPod.Namespace, Name: selectedPod.Name, UID: string(selectedPod.UID), Phase: opsv1alpha1.UpgradeDrainPodSelected},
			},
		}},
	}
	setDrainEligibilityHash(t, &leaf.Status.ManagerDrain.Pods[0])
	setDrainEligibilityHash(t, &leaf.Status.ManagerDrain.Pods[1])
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(pod, selectedPod, leaf).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
		&leaf.Status.ManagerDrain.Pods[0], now, true); err != nil {
		t.Fatalf("reconcileOneDrainPod(recovering) error = %v", err)
	}
	var gotPod corev1.Pod
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &gotPod); err != nil {
		t.Fatal(err)
	}
	if len(gotPod.Finalizers) != 0 || gotPod.Annotations[managedprotocol.AnnotationDrainSession] != "" {
		t.Fatalf("Pod protection was not released atomically: annotations=%v finalizers=%v", gotPod.Annotations, gotPod.Finalizers)
	}
	if err := r.releaseDrainPodProtection(context.Background(), &leaf.Status.ManagerDrain.Pods[0],
		leaf.Status.ManagerDrain.SessionToken); err != nil {
		t.Fatalf("replay release after metadata-patch crash: %v", err)
	}
	var gotLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain == nil || gotLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodReleased {
		t.Fatalf("manager drain Pod phase = %#v, want Released", gotLeaf.Status.ManagerDrain)
	}
	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), &gotLeaf,
		&gotLeaf.Status.ManagerDrain.Pods[0], now.Add(time.Second), true); err != nil {
		t.Fatalf("complete released Pod: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodComplete {
		t.Fatalf("never-evicted Pod phase = %s, want Complete", gotLeaf.Status.ManagerDrain.Pods[0].Phase)
	}

	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), &gotLeaf,
		&gotLeaf.Status.ManagerDrain.Pods[1], now.Add(2*time.Second), true); err != nil {
		t.Fatalf("record selected Pod protection during recovery: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain.Pods[1].Phase != opsv1alpha1.UpgradeDrainPodProtected {
		t.Fatalf("selected recovery Pod phase = %s, want Protected crash boundary", gotLeaf.Status.ManagerDrain.Pods[1].Phase)
	}
	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), &gotLeaf,
		&gotLeaf.Status.ManagerDrain.Pods[1], now.Add(3*time.Second), true); err != nil {
		t.Fatalf("release selected Pod protection during recovery: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain.Pods[1].Phase != opsv1alpha1.UpgradeDrainPodReleased {
		t.Fatalf("selected recovery Pod phase = %s, want Released", gotLeaf.Status.ManagerDrain.Pods[1].Phase)
	}
	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), &gotLeaf,
		&gotLeaf.Status.ManagerDrain.Pods[1], now.Add(4*time.Second), true); err != nil {
		t.Fatalf("complete released selected Pod during recovery: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain.Pods[1].Phase != opsv1alpha1.UpgradeDrainPodComplete {
		t.Fatalf("selected recovery Pod phase = %s, want Complete", gotLeaf.Status.ManagerDrain.Pods[1].Phase)
	}
	var gotSelected corev1.Pod
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(selectedPod), &gotSelected); err != nil {
		t.Fatal(err)
	}
	if gotSelected.Annotations[managedprotocol.AnnotationDrainSession] != "" ||
		controllerutil.ContainsFinalizer(&gotSelected, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("Selected crash-boundary protection was not removed: %#v", gotSelected.ObjectMeta)
	}
}

func TestRecoveringSelectedPodDoesNotSkipAcceptedTeardownEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	deletedAt := metav1.NewTime(now.Add(-time.Second))
	const token = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name      string
		pod       *corev1.Pod
		wantPhase opsv1alpha1.UpgradeDrainPodPhase
		wantBlock bool
	}{
		{name: "missing", wantPhase: opsv1alpha1.UpgradeDrainPodComplete},
		{name: "replacement", wantPhase: opsv1alpha1.UpgradeDrainPodComplete, pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "replacement-uid",
		}}},
		{name: "terminating before protection", wantBlock: true, pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "pod-uid", DeletionTimestamp: &deletedAt,
			Finalizers: []string{"example.invalid/unrelated"},
		}}},
		{name: "protected then terminating", wantPhase: opsv1alpha1.UpgradeDrainPodProtected, pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "pod-uid", DeletionTimestamp: &deletedAt,
			Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
			Finalizers:  []string{managedprotocol.DrainPodFinalizer},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frozen := opsv1alpha1.UpgradeDrainPodStatus{
				Namespace: "apps", Name: "edge-0", UID: "pod-uid", Phase: opsv1alpha1.UpgradeDrainPodSelected,
			}
			setDrainEligibilityHash(t, &frozen)
			leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
				ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
				Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
					SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
					Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
				}},
			}
			objects := []client.Object{leaf}
			if tc.pod != nil {
				objects = append(objects, tc.pod)
			}
			kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
				WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(objects...).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
			err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
				&leaf.Status.ManagerDrain.Pods[0], now, true)
			if tc.wantBlock {
				if !errors.Is(err, errDrainSafetyBlocked) {
					t.Fatalf("recovery error = %v, want errDrainSafetyBlocked", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var current opsv1alpha1.IOSXESoftwareUpgrade
			if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &current); err != nil {
				t.Fatal(err)
			}
			want := opsv1alpha1.UpgradeDrainPodSelected
			if !tc.wantBlock {
				want = tc.wantPhase
			}
			if current.Status.ManagerDrain.Pods[0].Phase != want {
				t.Fatalf("recovery phase = %s, want %s", current.Status.ManagerDrain.Pods[0].Phase, want)
			}
		})
	}
}

func TestProtectedDeletingPodRecordsAcceptedEvictionBeforeTermination(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	deletedAt := metav1.NewTime(now.Add(-time.Second))
	token := "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid", DeletionTimestamp: &deletedAt,
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainEvicting,
			Pods: []opsv1alpha1.UpgradeDrainPodStatus{{Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
				Phase: opsv1alpha1.UpgradeDrainPodProtected, ProtectedAt: &deletedAt}},
		}},
	}
	setDrainEligibilityHash(t, &leaf.Status.ManagerDrain.Pods[0])
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(pod, leaf).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
		&leaf.Status.ManagerDrain.Pods[0], now, false); err != nil {
		t.Fatal(err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &got); err != nil {
		t.Fatal(err)
	}
	state := got.Status.ManagerDrain.Pods[0]
	if state.Phase != opsv1alpha1.UpgradeDrainPodEvictionRequested || state.EvictionRequestedAt == nil ||
		state.DeletionObservedAt != nil {
		t.Fatalf("Pod progress = %#v, want EvictionRequested before TerminationObserved", state)
	}
}

func TestDrainEvictionReauthorizesCurrentUncachedControl(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*opsv1alpha1.IOSXESoftwareRollout, *opsv1alpha1.IOSXESoftwareUpgrade, []client.Object)
	}{
		{
			name: "another leader committed recovery",
			mutate: func(_ *opsv1alpha1.IOSXESoftwareRollout, leaf *opsv1alpha1.IOSXESoftwareUpgrade, _ []client.Object) {
				leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainRecovering
				deadline := metav1.NewTime(now.Add(10 * time.Minute))
				leaf.Status.ManagerDrain.RecoveryDeadline = &deadline
				leaf.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now)
			},
		},
		{
			name: "pause revision won the race",
			mutate: func(rollout *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ []client.Object) {
				rollout.Spec.Control.Revision++
				rollout.Spec.Control.Pause = true
			},
		},
		{
			name: "cancel revision won the race",
			mutate: func(rollout *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ []client.Object) {
				rollout.Spec.Control.Revision++
				rollout.Spec.Control.Cancel = true
			},
		},
		{
			name: "persisted policy transition fences the old epoch",
			mutate: func(rollout *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ []client.Object) {
				rollout.Status.PolicyTransition = &opsv1alpha1.IOSXESoftwareRolloutPolicyTransitionStatus{
					Epoch:  rollout.Status.EffectivePolicy.Epoch + 1,
					Policy: rollout.Status.EffectivePolicy.Policy, StartedAt: metav1.NewTime(now),
				}
			},
		},
		{
			name: "non-active rollout phase",
			mutate: func(rollout *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ []client.Object) {
				rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhaseAwaitingApproval
			},
		},
		{
			name: "terminal rollout phase",
			mutate: func(rollout *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ []client.Object) {
				rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhaseFailed
			},
		},
		{
			name: "live administrator policy is malformed",
			mutate: func(_ *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, objects []client.Object) {
				for _, object := range objects {
					if policy, ok := object.(*corev1.ConfigMap); ok {
						policy.Data[topologyrollout.PolicyDataKey] = "not-json"
						return
					}
				}
				t.Fatal("fixture has no administrator policy")
			},
		},
		{
			name: "live managed worker revision changed",
			mutate: func(_ *opsv1alpha1.IOSXESoftwareRollout, _ *opsv1alpha1.IOSXESoftwareUpgrade, objects []client.Object) {
				for _, object := range objects {
					if deployment, ok := object.(*appsv1.Deployment); ok {
						deployment.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] =
							"sha256:" + strings.Repeat("f", 64)
						return
					}
				}
				t.Fatal("fixture has no managed worker Deployment")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rollout, target, leaf, objects := drainEvictionAuthorityFixture(t, now)
			currentRollout := rollout.DeepCopy()
			currentLeaf := leaf.DeepCopy()
			tc.mutate(currentRollout, currentLeaf, objects)
			objects = append(objects, currentRollout, currentLeaf)
			base := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
				WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
				WithObjects(objects...).Build()
			evictionCalls := 0
			apiClient := interceptor.NewClient(base, interceptor.Funcs{
				SubResourceCreate: func(context.Context, client.Client, string, client.Object,
					client.Object, ...client.SubResourceCreateOption) error {
					evictionCalls++
					return nil
				},
			})
			r := &IOSXESoftwareRolloutReconciler{
				Client: apiClient, APIReader: apiClient, Now: func() time.Time { return now },
			}
			err := r.reconcileOneDrainPod(context.Background(), rollout, target, leaf,
				&leaf.Status.ManagerDrain.Pods[0], now, false)
			if !errors.Is(err, errDrainSafetyBlocked) {
				t.Fatalf("stale Eviction reconcile error = %v, want errDrainSafetyBlocked", err)
			}
			if evictionCalls != 0 {
				t.Fatalf("policy/v1 Eviction was dispatched %d times after authority was fenced", evictionCalls)
			}
		})
	}
}

func TestDrainEvictionDispatchesOnlyAfterFreshExactAuthorization(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, phase := range []opsv1alpha1.IOSXESoftwareRolloutPhase{
		opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting,
		opsv1alpha1.IOSXESoftwareRolloutPhaseSoaking,
	} {
		t.Run(string(phase), func(t *testing.T) {
			rollout, target, leaf, objects := drainEvictionAuthorityFixture(t, now)
			rollout.Status.Phase = phase
			objects = append(objects, rollout, leaf)
			base := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
				WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
				WithObjects(objects...).Build()
			evictionCalls := 0
			apiClient := interceptor.NewClient(base, interceptor.Funcs{
				SubResourceCreate: func(_ context.Context, _ client.Client, subResource string, obj client.Object,
					subresource client.Object, _ ...client.SubResourceCreateOption) error {
					evictionCalls++
					if subResource != "eviction" || obj.GetUID() != types.UID(leaf.Status.ManagerDrain.Pods[0].UID) {
						t.Fatalf("unexpected subresource dispatch %q for UID %q", subResource, obj.GetUID())
					}
					eviction, ok := subresource.(*policyv1.Eviction)
					if !ok || eviction.DeleteOptions == nil || eviction.DeleteOptions.Preconditions == nil ||
						eviction.DeleteOptions.Preconditions.UID == nil ||
						*eviction.DeleteOptions.Preconditions.UID != obj.GetUID() ||
						eviction.DeleteOptions.Preconditions.ResourceVersion == nil ||
						*eviction.DeleteOptions.Preconditions.ResourceVersion != obj.GetResourceVersion() {
						t.Fatalf("Eviction lacks exact fresh Pod preconditions: %#v", subresource)
					}
					return nil
				},
			})
			r := &IOSXESoftwareRolloutReconciler{
				Client: apiClient, APIReader: apiClient, Now: func() time.Time { return now },
			}
			if err := r.reconcileOneDrainPod(context.Background(), rollout, target, leaf,
				&leaf.Status.ManagerDrain.Pods[0], now, false); err != nil {
				t.Fatal(err)
			}
			if evictionCalls != 1 {
				t.Fatalf("policy/v1 Eviction dispatch count = %d, want 1", evictionCalls)
			}
		})
	}
}

func TestStalePreparingLeaderCannotAddProtectionAfterRecovery(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	rollout, target, staleLeaf, objects := drainEvictionAuthorityFixture(t, now)
	staleLeaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainPreparing
	staleLeaf.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodSelected
	staleLeaf.Status.ManagerDrain.Pods[0].ProtectedAt = nil
	currentLeaf := staleLeaf.DeepCopy()
	currentLeaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainRecovering
	recoveryDeadline := metav1.NewTime(now.Add(10 * time.Minute))
	currentLeaf.Status.ManagerDrain.RecoveryDeadline = &recoveryDeadline
	currentLeaf.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now)
	var selectedPod *corev1.Pod
	for _, object := range objects {
		pod, ok := object.(*corev1.Pod)
		if !ok || string(pod.UID) != staleLeaf.Status.ManagerDrain.Pods[0].UID {
			continue
		}
		selectedPod = pod
		delete(selectedPod.Annotations, managedprotocol.AnnotationDrainSession)
		controllerutil.RemoveFinalizer(selectedPod, managedprotocol.DrainPodFinalizer)
		break
	}
	if selectedPod == nil {
		t.Fatal("fixture has no selected workload Pod")
	}
	objects = append(objects, rollout, currentLeaf)
	base := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(objects...).Build()
	podPatches := 0
	apiClient := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, inner client.WithWatch, object client.Object, patch client.Patch,
			opts ...client.PatchOption) error {
			if _, ok := object.(*corev1.Pod); ok {
				podPatches++
			}
			return inner.Patch(ctx, object, patch, opts...)
		},
	})
	r := &IOSXESoftwareRolloutReconciler{
		Client: apiClient, APIReader: apiClient, Now: func() time.Time { return now },
	}
	err := r.protectDrainPod(context.Background(), rollout, target, staleLeaf,
		&staleLeaf.Status.ManagerDrain.Pods[0], staleLeaf.Status.ManagerDrain.SessionToken,
		rollout.Spec.Plan.Workloads.Drain)
	if !errors.Is(err, errDrainSafetyBlocked) {
		t.Fatalf("stale protection error = %v, want errDrainSafetyBlocked", err)
	}
	if podPatches != 0 {
		t.Fatalf("stale Preparing leader patched the Pod %d times", podPatches)
	}
	var currentPod corev1.Pod
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(selectedPod), &currentPod); err != nil {
		t.Fatal(err)
	}
	if currentPod.Annotations[managedprotocol.AnnotationDrainSession] != "" ||
		controllerutil.ContainsFinalizer(&currentPod, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("stale Preparing leader added protection after recovery: %#v", currentPod.ObjectMeta)
	}
}

func TestTerminalRolloutRepairsLateStalePodProtection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	rollout, _, leaf, objects := drainEvictionAuthorityFixture(t, now)
	rollout.Finalizers = []string{rolloutSafetyFinalizer}
	rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
	leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainSettled
	recoveryDeadline := metav1.NewTime(now.Add(10 * time.Minute))
	leaf.Status.ManagerDrain.RecoveryDeadline = &recoveryDeadline
	leaf.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now)
	leaf.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodComplete
	var selectedPod *corev1.Pod
	for _, object := range objects {
		pod, ok := object.(*corev1.Pod)
		if !ok || string(pod.UID) != leaf.Status.ManagerDrain.Pods[0].UID {
			continue
		}
		selectedPod = pod
		delete(selectedPod.Annotations, managedprotocol.AnnotationDrainSession)
		controllerutil.RemoveFinalizer(selectedPod, managedprotocol.DrainPodFinalizer)
		break
	}
	if selectedPod == nil {
		t.Fatal("fixture has no selected workload Pod")
	}
	objects = append(objects, rollout, leaf)
	apiClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithIndex(&opsv1alpha1.IOSXESoftwareRollout{}, rolloutTargetNodeNameIndex, rolloutTargetNodeNameIndexValues).
		WithObjects(objects...).Build()
	r := &IOSXESoftwareRolloutReconciler{
		Client: apiClient, APIReader: apiClient, Now: func() time.Time { return now },
	}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rollout)}
	if _, err := r.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("initial terminal cleanup: %v", err)
	}

	var stalePod corev1.Pod
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(selectedPod), &stalePod); err != nil {
		t.Fatal(err)
	}
	if stalePod.Annotations == nil {
		stalePod.Annotations = map[string]string{}
	}
	stalePod.Annotations[managedprotocol.AnnotationDrainSession] = leaf.Status.ManagerDrain.SessionToken
	controllerutil.AddFinalizer(&stalePod, managedprotocol.DrainPodFinalizer)
	if err := apiClient.Update(context.Background(), &stalePod); err != nil {
		t.Fatal(err)
	}
	requests := r.rolloutRequestsForDrainPod(context.Background(), &stalePod)
	if len(requests) != 1 || requests[0].NamespacedName != request.NamespacedName {
		t.Fatalf("late protected Pod mapped to requests %#v, want %s", requests, request.NamespacedName)
	}
	if _, err := r.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("replayed terminal cleanup: %v", err)
	}
	var repaired corev1.Pod
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(selectedPod), &repaired); err != nil {
		t.Fatal(err)
	}
	if repaired.Annotations[managedprotocol.AnnotationDrainSession] != "" ||
		controllerutil.ContainsFinalizer(&repaired, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("terminal replay did not remove stale exact protection: %#v", repaired.ObjectMeta)
	}
}

func TestDrainProgressCASRejectsStaleLeaderRegression(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	current := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			SessionToken: "11111111-1111-4111-8111-111111111111",
			State:        opsv1alpha1.UpgradeManagerDrainPromoting,
			Pods: []opsv1alpha1.UpgradeDrainPodStatus{{
				Namespace: "apps", Name: "edge-0", UID: "pod-uid", Phase: opsv1alpha1.UpgradeDrainPodComplete,
			}},
		}},
	}
	stale := current.DeepCopy()
	stale.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainEvicting
	stale.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodProtected
	kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(current).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.patchDrainPodPhase(context.Background(), stale, "pod-uid",
		opsv1alpha1.UpgradeDrainPodProtected, opsv1alpha1.UpgradeDrainPodEvictionRequested, now, 0); err == nil {
		t.Fatal("stale post-Eviction leader regressed a completed Pod phase")
	}
	if err := r.patchManagerDrainState(context.Background(), stale,
		opsv1alpha1.UpgradeManagerDrainEvicting, opsv1alpha1.UpgradeManagerDrainDrained, now); err == nil {
		t.Fatal("stale leader regressed an advanced manager drain state")
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(current), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainPromoting ||
		got.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodComplete {
		t.Fatalf("stale leader changed durable drain progress: %#v", got.Status.ManagerDrain)
	}
}

func TestRecoveringSelectedPodCleansProtectionCommittedDuringStatusCAS(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	const token = "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge-0", UID: "pod-uid"}}
	frozen := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID), Phase: opsv1alpha1.UpgradeDrainPodSelected,
	}
	setDrainEligibilityHash(t, &frozen)
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
			Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
		}},
	}
	injected := false
	kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(pod, leaf).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, inner client.Client, subResource string,
				obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				upgrade, ok := obj.(*opsv1alpha1.IOSXESoftwareUpgrade)
				if subResource == "status" && ok && !injected && upgrade.Status.ManagerDrain != nil &&
					upgrade.Status.ManagerDrain.Pods[0].Phase == opsv1alpha1.UpgradeDrainPodComplete {
					// Recovery already read the unmarked Pod. Commit the stale
					// Preparing leader's Pod-side write immediately before the
					// Selected->Complete status CAS.
					injected = true
					var live corev1.Pod
					if err := inner.Get(ctx, client.ObjectKeyFromObject(pod), &live); err != nil {
						return err
					}
					before := live.DeepCopy()
					if live.Annotations == nil {
						live.Annotations = map[string]string{}
					}
					live.Annotations[managedprotocol.AnnotationDrainSession] = token
					controllerutil.AddFinalizer(&live, managedprotocol.DrainPodFinalizer)
					if err := inner.Patch(ctx, &live, client.MergeFrom(before)); err != nil {
						return err
					}
				}
				return inner.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
		&leaf.Status.ManagerDrain.Pods[0], now, true); err != nil {
		t.Fatal(err)
	}
	if !injected {
		t.Fatal("test did not inject the stale cross-object protection write")
	}
	var gotLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodComplete {
		t.Fatalf("recovery Pod phase = %s, want Complete", gotLeaf.Status.ManagerDrain.Pods[0].Phase)
	}
	var gotPod corev1.Pod
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &gotPod); err != nil {
		t.Fatal(err)
	}
	if gotPod.Annotations[managedprotocol.AnnotationDrainSession] != "" ||
		controllerutil.ContainsFinalizer(&gotPod, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("stale protection survived recovery completion: %#v", gotPod.ObjectMeta)
	}
}

func TestRecoveryCleanupRechecksCompletePodProtection(t *testing.T) {
	t.Parallel()
	const token = "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid",
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	frozen := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID), Phase: opsv1alpha1.UpgradeDrainPodComplete,
	}
	setDrainEligibilityHash(t, &frozen)
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
			Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
		}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(pod, leaf).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.reconcileDrainRecoveryCleanup(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var got corev1.Pod
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[managedprotocol.AnnotationDrainSession] != "" ||
		controllerutil.ContainsFinalizer(&got, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("Complete recovery Pod retained stale exact protection: %#v", got.ObjectMeta)
	}
}

func TestProtectedRecoveryPreservesFinalizerWhenConcurrentEvictionWins(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	const token = "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid",
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	frozen := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID), Phase: opsv1alpha1.UpgradeDrainPodProtected,
	}
	setDrainEligibilityHash(t, &frozen)
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
			Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
		}},
	}
	injected := false
	kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(pod, leaf).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, inner client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				candidate, ok := obj.(*corev1.Pod)
				if ok && !injected && candidate.Annotations[managedprotocol.AnnotationDrainSession] == "" &&
					!controllerutil.ContainsFinalizer(candidate, managedprotocol.DrainPodFinalizer) {
					injected = true
					var live corev1.Pod
					if err := inner.Get(ctx, client.ObjectKeyFromObject(pod), &live); err != nil {
						return err
					}
					if err := inner.Delete(ctx, &live); err != nil {
						return err
					}
					return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, candidate.Name,
						errors.New("injected concurrent policy/v1 Eviction"))
				}
				return inner.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
		&leaf.Status.ManagerDrain.Pods[0], now, true); err != nil {
		t.Fatal(err)
	}
	if !injected {
		t.Fatal("test did not inject the concurrent Eviction")
	}
	var gotLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodEvictionRequested {
		t.Fatalf("concurrently evicted Pod phase = %s, want EvictionRequested", gotLeaf.Status.ManagerDrain.Pods[0].Phase)
	}
	var gotPod corev1.Pod
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &gotPod); err != nil {
		t.Fatal(err)
	}
	if gotPod.DeletionTimestamp == nil || gotPod.Annotations[managedprotocol.AnnotationDrainSession] != token ||
		!controllerutil.ContainsFinalizer(&gotPod, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("concurrent Eviction lost exact cleanup protection: %#v", gotPod.ObjectMeta)
	}
}

func TestRecoveringProtectedPodClosesReleasedCrashBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	const token = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name      string
		pod       *corev1.Pod
		wantBlock bool
	}{
		{name: "exact Pod absent after release"},
		{name: "unprotected replacement after release", pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "replacement-uid",
		}}},
		{name: "exact Pod unprotected after release", pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "pod-uid",
		}}},
		{name: "partial protection on replacement is quarantined", wantBlock: true, pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "replacement-uid",
			Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		}}},
		{name: "foreign protection on replacement is quarantined", wantBlock: true, pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "replacement-uid",
			Annotations: map[string]string{managedprotocol.AnnotationDrainSession: "22222222-2222-4222-8222-222222222222"},
			Finalizers:  []string{managedprotocol.DrainPodFinalizer},
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frozen := opsv1alpha1.UpgradeDrainPodStatus{
				Namespace: "apps", Name: "edge-0", UID: "pod-uid", Phase: opsv1alpha1.UpgradeDrainPodProtected,
			}
			setDrainEligibilityHash(t, &frozen)
			leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
				ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
				Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
					SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
					Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
				}},
			}
			objects := []client.Object{leaf}
			if tc.pod != nil {
				objects = append(objects, tc.pod)
			}
			kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
				WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(objects...).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
			err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
				&leaf.Status.ManagerDrain.Pods[0], now, true)
			if tc.wantBlock {
				if !errors.Is(err, errDrainSafetyBlocked) {
					t.Fatalf("recovery error = %v, want errDrainSafetyBlocked", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var current opsv1alpha1.IOSXESoftwareUpgrade
			if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &current); err != nil {
				t.Fatal(err)
			}
			want := opsv1alpha1.UpgradeDrainPodReleased
			if tc.wantBlock {
				want = opsv1alpha1.UpgradeDrainPodProtected
			}
			if current.Status.ManagerDrain.Pods[0].Phase != want {
				t.Fatalf("recovery phase = %s, want %s", current.Status.ManagerDrain.Pods[0].Phase, want)
			}
		})
	}
}

func TestRecoveringProtectedPodConvergesAcrossReleaseRereadRace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	const token = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name        string
		replacement *corev1.Pod
		terminate   bool
		wantPhase   opsv1alpha1.UpgradeDrainPodPhase
		wantBlock   bool
	}{
		{
			name: "already released Pod disappeared", wantPhase: opsv1alpha1.UpgradeDrainPodReleased,
		},
		{
			name: "already released Pod was replaced", wantPhase: opsv1alpha1.UpgradeDrainPodReleased,
			replacement: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Namespace: "apps", Name: "edge-0", UID: "replacement-uid",
			}},
		},
		{
			name: "replacement gained partial protection", wantBlock: true,
			replacement: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Namespace: "apps", Name: "edge-0", UID: "replacement-uid",
				Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
			}},
		},
		{
			name: "accepted termination retains strict cleanup", terminate: true,
			wantPhase: opsv1alpha1.UpgradeDrainPodEvictionRequested,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Namespace: "apps", Name: "edge-0", UID: "pod-uid",
				Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
				Finalizers:  []string{managedprotocol.DrainPodFinalizer},
			}}
			frozen := opsv1alpha1.UpgradeDrainPodStatus{
				Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
				Phase: opsv1alpha1.UpgradeDrainPodProtected,
			}
			setDrainEligibilityHash(t, &frozen)
			leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
				ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
				Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
					SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
					Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
				}},
			}
			podGets := 0
			injected := false
			apiClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
				WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(pod, leaf).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, inner client.WithWatch, key client.ObjectKey, obj client.Object,
						opts ...client.GetOption) error {
						if _, ok := obj.(*corev1.Pod); ok && key == client.ObjectKeyFromObject(pod) {
							podGets++
							if podGets == 2 {
								injected = true
								var live corev1.Pod
								if err := inner.Get(ctx, key, &live); err != nil {
									return err
								}
								if tc.terminate {
									if err := inner.Delete(ctx, &live); err != nil {
										return err
									}
								} else {
									delete(live.Annotations, managedprotocol.AnnotationDrainSession)
									controllerutil.RemoveFinalizer(&live, managedprotocol.DrainPodFinalizer)
									if err := inner.Update(ctx, &live); err != nil {
										return err
									}
									if err := inner.Delete(ctx, &live); err != nil {
										return err
									}
									if tc.replacement != nil {
										if err := inner.Create(ctx, tc.replacement.DeepCopy()); err != nil {
											return err
										}
									}
								}
							}
						}
						return inner.Get(ctx, key, obj, opts...)
					},
				}).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
			err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), leaf,
				&leaf.Status.ManagerDrain.Pods[0], now, true)
			if !injected {
				t.Fatal("test did not inject the second-read recovery race")
			}
			if tc.wantBlock {
				if !errors.Is(err, errDrainSafetyBlocked) {
					t.Fatalf("recovery error = %v, want errDrainSafetyBlocked", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var current opsv1alpha1.IOSXESoftwareUpgrade
			if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &current); err != nil {
				t.Fatal(err)
			}
			want := tc.wantPhase
			if tc.wantBlock {
				want = opsv1alpha1.UpgradeDrainPodProtected
			}
			if current.Status.ManagerDrain.Pods[0].Phase != want {
				t.Fatalf("recovery phase = %s, want %s", current.Status.ManagerDrain.Pods[0].Phase, want)
			}
		})
	}
}

func TestCompleteRecoveryDoesNotRemoveProtectionFromConcurrentlyTerminatingPod(t *testing.T) {
	t.Parallel()
	const token = "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid",
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	frozen := &opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID), Phase: opsv1alpha1.UpgradeDrainPodComplete,
	}
	setDrainEligibilityHash(t, frozen)
	injected := false
	base := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).WithObjects(pod).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, inner client.WithWatch, obj client.Object, patch client.Patch,
				opts ...client.PatchOption) error {
				candidate, ok := obj.(*corev1.Pod)
				if ok && !injected && candidate.Annotations[managedprotocol.AnnotationDrainSession] == "" &&
					!controllerutil.ContainsFinalizer(candidate, managedprotocol.DrainPodFinalizer) {
					injected = true
					var live corev1.Pod
					if err := inner.Get(ctx, client.ObjectKeyFromObject(pod), &live); err != nil {
						return err
					}
					if err := inner.Delete(ctx, &live); err != nil {
						return err
					}
					return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, candidate.Name,
						errors.New("injected concurrent policy/v1 Eviction"))
				}
				return inner.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: base, APIReader: base}
	if err := r.ensureCompletedDrainPodUnprotected(context.Background(), frozen, token); err == nil {
		t.Fatal("concurrently terminating Complete Pod was not quarantined")
	}
	if !injected {
		t.Fatal("test did not inject concurrent termination")
	}
	var current corev1.Pod
	if err := base.Get(context.Background(), client.ObjectKeyFromObject(pod), &current); err != nil {
		t.Fatal(err)
	}
	if current.DeletionTimestamp == nil || current.Annotations[managedprotocol.AnnotationDrainSession] != token ||
		!controllerutil.ContainsFinalizer(&current, managedprotocol.DrainPodFinalizer) {
		t.Fatalf("concurrent termination lost exact cleanup protection: %#v", current.ObjectMeta)
	}
}

func TestApplyManagerDrainControlPauseAndResumeAtPromotion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	promoted := &opsv1alpha1.UpgradeManagerDrainStatus{
		State: opsv1alpha1.UpgradeManagerDrainPromoted, ControlRevision: 7, UpdatedAt: metav1.NewTime(now),
	}
	applyManagerDrainControl(promoted, true, false, 8, now.Add(time.Second))
	if promoted.State != opsv1alpha1.UpgradeManagerDrainPromoted || promoted.ControlRevision != 8 ||
		!promoted.UpdatedAt.Time.Equal(now.Add(time.Second)) {
		t.Fatalf("paused promoted drain = %#v, want retained Promoted at revision 8", promoted)
	}
	// Replaying after a manager crash is idempotent, then resume advances the
	// same exact promoted session rather than manufacturing a second drain.
	applyManagerDrainControl(promoted, true, false, 8, now.Add(2*time.Second))
	if !promoted.UpdatedAt.Time.Equal(now.Add(time.Second)) {
		t.Fatalf("same control revision rewrote manager drain updatedAt: %s", promoted.UpdatedAt.Time)
	}
	applyManagerDrainControl(promoted, false, false, 9, now.Add(3*time.Second))
	if promoted.State != opsv1alpha1.UpgradeManagerDrainPromoted || promoted.ControlRevision != 9 ||
		!promoted.UpdatedAt.Time.Equal(now.Add(3*time.Second)) {
		t.Fatalf("resumed promoted drain = %#v, want retained Promoted at revision 9", promoted)
	}

	evicting := &opsv1alpha1.UpgradeManagerDrainStatus{
		State: opsv1alpha1.UpgradeManagerDrainEvicting, ControlRevision: 7, UpdatedAt: metav1.NewTime(now),
		StartedAt: metav1.NewTime(now), DrainDeadline: metav1.NewTime(now.Add(time.Minute)),
	}
	applyManagerDrainControl(evicting, true, false, 8, now.Add(time.Second))
	if evicting.State != opsv1alpha1.UpgradeManagerDrainRecovering || evicting.ControlRevision != 8 {
		t.Fatalf("paused pre-promotion drain = %#v, want Recovering at revision 8", evicting)
	}
}

func TestApplyManagerDrainControlRenewsExpiredRecoveryOnlyAtNewerRevision(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	now := started.Add(20 * time.Minute)
	oldDeadline := metav1.NewTime(now.Add(-time.Second))
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion:  opsv1alpha1.ManagedDrainProtocolPDBV1,
		State:            opsv1alpha1.UpgradeManagerDrainRecovering,
		SessionToken:     "11111111-1111-4111-8111-111111111111",
		ControlRevision:  7,
		StartedAt:        metav1.NewTime(started),
		DrainDeadline:    metav1.NewTime(started.Add(10 * time.Minute)),
		RecoveryDeadline: &oldDeadline,
		UpdatedAt:        metav1.NewTime(started.Add(10 * time.Minute)),
		Pods:             []opsv1alpha1.UpgradeDrainPodStatus{{Namespace: "apps", Name: "edge", UID: "pod-uid"}},
	}

	applyManagerDrainControl(drain, false, true, 7, now)
	if !drain.RecoveryDeadline.Equal(&oldDeadline) || drain.ControlRevision != 7 {
		t.Fatalf("same-revision recovery changed authority: %#v", drain)
	}

	applyManagerDrainControl(drain, false, true, 8, now)
	wantDeadline := now.Add(12 * time.Minute)
	if drain.State != opsv1alpha1.UpgradeManagerDrainRecovering || drain.ControlRevision != 8 ||
		drain.RecoveryDeadline == nil || !drain.RecoveryDeadline.Time.Equal(wantDeadline) ||
		!drain.UpdatedAt.Time.Equal(now) {
		t.Fatalf("renewed recovery = %#v, want revision 8 deadline %s", drain, wantDeadline)
	}
	if drain.SessionToken != "11111111-1111-4111-8111-111111111111" ||
		!drain.StartedAt.Time.Equal(started) || len(drain.Pods) != 1 || drain.Pods[0].UID != "pod-uid" {
		t.Fatalf("recovery renewal changed immutable session or selection: %#v", drain)
	}

	renewed := drain.RecoveryDeadline.DeepCopy()
	applyManagerDrainControl(drain, false, true, 8, now.Add(time.Minute))
	if !drain.RecoveryDeadline.Equal(renewed) {
		t.Fatalf("same revision extended recovery from %s to %s", renewed, drain.RecoveryDeadline)
	}
}

func TestDeletionFenceRevisionRenewsOverdueRecovery(t *testing.T) {
	t.Parallel()
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Plan.Workloads = drainTestRollout().Spec.Plan.Workloads
	leaf := policyFenceLeaf(rollout, target, "leaf-uid")
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	leaf.Status.ManagerControl.Revision = rollout.Spec.Control.Revision + 1
	leaf.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
		State: opsv1alpha1.UpgradeManagerDrainRecovering, ControlRevision: leaf.Status.ManagerControl.Revision,
		RecoveryDeadline: ptr.To(metav1.NewTime(now.Add(-time.Second))),
	}
	scheme := drainTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(leaf).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	revision, err := r.deletionFenceRevision(context.Background(), rollout, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := rollout.Spec.Control.Revision + 2; revision != want {
		t.Fatalf("deletionFenceRevision() = %d, want overdue synthetic revision %d", revision, want)
	}
	leaf.Status.ManagerDrain.RecoveryDeadline = ptr.To(metav1.NewTime(now.Add(time.Minute)))
	if err := kubeClient.Update(context.Background(), leaf); err != nil {
		t.Fatal(err)
	}
	revision, err = r.deletionFenceRevision(context.Background(), rollout, now)
	if err != nil || revision != rollout.Spec.Control.Revision+1 {
		t.Fatalf("unexpired deletionFenceRevision() = (%d, %v), want existing synthetic revision", revision, err)
	}
}

func TestCancellationWaitsForSettledDrainAcknowledgementBeforeBecomingTerminal(t *testing.T) {
	t.Parallel()
	r, rollout, leaf, device := settledDrainAcknowledgementFixture(t)
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rollout), rollout); err != nil {
		t.Fatal(err)
	}
	rollout.Spec.Control.Cancel = true
	if err := r.Update(context.Background(), rollout); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	result, err := r.reconcileCancellation(context.Background(), rollout, nil, now)
	if err != nil {
		t.Fatalf("reconcileCancellation() before device acknowledgement: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("cancellation became terminal before the CiscoDevice acknowledged drain settlement")
	}
	var currentRollout opsv1alpha1.IOSXESoftwareRollout
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rollout), &currentRollout); err != nil {
		t.Fatal(err)
	}
	if currentRollout.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling {
		t.Fatalf("campaign phase = %s, want Cancelling while drain acknowledgement is pending", currentRollout.Status.Phase)
	}
	assertSettledDrainBindingPreserved(t, r, leaf)
	assertDrainTopologyLock(t, r, device, true)

	acknowledgeSettledDrain(t, r, device)
	result, err = r.reconcileCancellation(context.Background(), &currentRollout, nil, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reconcileCancellation() after device acknowledgement: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("settled cancellation result = %#v, want terminal result", result)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rollout), &currentRollout); err != nil {
		t.Fatal(err)
	}
	if currentRollout.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled {
		t.Fatalf("campaign phase = %s, want Cancelled after drain acknowledgement", currentRollout.Status.Phase)
	}
	assertDrainTopologyLock(t, r, device, false)
}

func TestPromotedZeroPodCancellationConvergesAfterWorkerLeaseBecomesIdle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cancelledAt := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	fixture := promotedZeroPodCancellationFixture(t, cancelledAt)
	fixture.rolloutReconciler.Now = func() time.Time { return cancelledAt }
	fixture.clock.now = cancelledAt

	var rollout opsv1alpha1.IOSXESoftwareRollout
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &rollout); err != nil {
		t.Fatal(err)
	}
	rollout.Spec.Control.Cancel = true
	rollout.Spec.Control.Revision++
	if err := fixture.rolloutReconciler.Update(ctx, &rollout); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.rolloutReconciler.reconcileCancellation(
		ctx, &rollout, fixture.policy, cancelledAt,
	)
	if err != nil {
		t.Fatalf("initial reconcileCancellation(): %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("promoted cancellation became terminal before recovery acknowledgement")
	}

	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.leaf), &leaf); err != nil {
		t.Fatal(err)
	}
	drain := leaf.Status.ManagerDrain
	if drain == nil || drain.State != opsv1alpha1.UpgradeManagerDrainRecovering ||
		drain.ControlRevision != rollout.Spec.Control.Revision || drain.SessionToken != fixture.sessionToken ||
		!drain.StartedAt.Equal(&fixture.startedAt) || len(drain.Pods) != 0 ||
		len(leaf.Status.ManagedMutationClaims) != 0 {
		t.Fatalf("cancellation recovery fence = %#v", leaf.Status)
	}
	var device ciskov1.CiscoDevice
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.device), &device); err != nil {
		t.Fatal(err)
	}
	if device.Status.MaintenanceSession == nil ||
		device.Status.MaintenanceSession.ControlRevision != rollout.Spec.Control.Revision-1 {
		t.Fatalf("device unexpectedly acknowledged recovery before Lease retirement: %#v", device.Status.MaintenanceSession)
	}
	assertDrainTopologyLock(t, fixture.rolloutReconciler, fixture.device, true)

	// Model the worker's ordered cancellation barrier: first publish the exact
	// Cancelled acknowledgement, then retire its unsubmitted software-mutation
	// request and leave the canonical Lease wholly idle and request-free.
	leaf.Status.WorkerControl = &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState:  opsv1alpha1.UpgradeManagerAdmissionRevoked,
		ObservedPolicyEpoch:     leaf.Status.ManagerAdmission.PolicyEpoch,
		ObservedControlRevision: rollout.Spec.Control.Revision,
		EffectiveState:          opsv1alpha1.UpgradeWorkerControlCancelled,
		UpdatedAt:               metav1.NewTime(cancelledAt.Add(time.Second)),
	}
	if err := fixture.rolloutReconciler.Status().Update(ctx, &leaf); err != nil {
		t.Fatal(err)
	}
	var lease coordv1.Lease
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.lease), &lease); err != nil {
		t.Fatal(err)
	}
	lease.Annotations, lease.Labels = managedMutationLeaseMetadata(
		&device, fixture.target.NodeName, fixture.target.NodeUID,
		leaf.Annotations[managedprotocol.AnnotationWorkerUsername],
	)
	lease.Spec = coordv1.LeaseSpec{LeaseTransitions: lease.Spec.LeaseTransitions}
	if err := fixture.rolloutReconciler.Update(ctx, &lease); err != nil {
		t.Fatal(err)
	}

	recoveryAt := cancelledAt.Add(2 * time.Second)
	fixture.clock.now = recoveryAt
	var node corev1.Node
	if err := fixture.deviceReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.device), &device); err != nil {
		t.Fatal(err)
	}
	if err := fixture.deviceReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.node), &node); err != nil {
		t.Fatal(err)
	}
	decision := fixture.deviceReconciler.resolveManagedMaintenance(ctx, &device, &node)
	if decision.err != nil || decision.session == nil || decision.guard ||
		decision.session.Phase != ciskov1.DeviceMaintenanceSessionRecovering ||
		decision.session.Purpose != ciskov1.DeviceMaintenancePurposeSoftwareMutation ||
		decision.session.ControlRevision != rollout.Spec.Control.Revision ||
		decision.session.SessionToken != fixture.sessionToken ||
		!decision.session.RequestedAt.Equal(&fixture.startedAt) {
		t.Fatalf("idle-Lease recovery acknowledgement = %#v", decision)
	}
	if err := fixture.deviceReconciler.reconcileManagedNodeMetadata(
		ctx, &device, &node, map[string]string{"topology.cisco.vk/site": "site-a"},
		fixture.policy, fixture.target.ProjectionHash, decision,
	); err != nil {
		t.Fatalf("restore drain-owned Node guard: %v", err)
	}
	if err := fixture.deviceReconciler.patchManagedTopologyStatus(
		ctx, &device, &node, fixture.target.ProjectionHash, decision,
	); err != nil {
		t.Fatalf("persist exact recovery acknowledgement: %v", err)
	}

	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.node), &node); err != nil {
		t.Fatal(err)
	}
	if node.Spec.Unschedulable || hasDrainMaintenanceTaint(node.Spec.Taints) ||
		node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" ||
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		t.Fatalf("recovery acknowledgement retained a drain-owned scheduling guard: %#v", node)
	}
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.device), &device); err != nil {
		t.Fatal(err)
	}
	if err := validateDrainSessionIdentity(device.Status.MaintenanceSession, fixture.target, &leaf, drain); err != nil {
		t.Fatalf("persisted recovery acknowledgement lost exact session identity: %v", err)
	}

	healthyAt := recoveryAt.Add(time.Second)
	ready := nodeReadyCondition(&node)
	if ready == nil {
		t.Fatal("fixture lost Node Ready condition")
	}
	ready.Status = corev1.ConditionTrue
	ready.LastHeartbeatTime = metav1.NewTime(healthyAt)
	ready.LastTransitionTime = metav1.NewTime(fixture.startedAt.Add(-time.Minute))
	if err := fixture.rolloutReconciler.Status().Update(ctx, &node); err != nil {
		t.Fatal(err)
	}
	if err := refreshManagedHealthObservation(&device, &node, healthyAt,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.rolloutReconciler.Status().Update(ctx, &device); err != nil {
		t.Fatal(err)
	}

	fixture.rolloutReconciler.Now = func() time.Time { return healthyAt }
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &rollout); err != nil {
		t.Fatal(err)
	}
	result, err = fixture.rolloutReconciler.reconcileCancellation(ctx, &rollout, fixture.policy, healthyAt)
	if err != nil {
		t.Fatalf("start recovered health soak: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("cancellation skipped the recovered health soak")
	}

	settledAt := healthyAt.Add(2 * time.Second)
	fixture.rolloutReconciler.Now = func() time.Time { return settledAt }
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &rollout); err != nil {
		t.Fatal(err)
	}
	result, err = fixture.rolloutReconciler.reconcileCancellation(ctx, &rollout, fixture.policy, settledAt)
	if err != nil {
		t.Fatalf("settle recovered drain: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("cancellation became terminal before the Settled session acknowledgement")
	}
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.leaf), &leaf); err != nil {
		t.Fatal(err)
	}
	if leaf.Status.ManagerDrain == nil || leaf.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainSettled ||
		leaf.Status.ManagerAdmission == nil || leaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		var currentRollout opsv1alpha1.IOSXESoftwareRollout
		_ = fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &currentRollout)
		t.Fatalf("recovered leaf did not settle: admission=%#v drain=%#v targets=%#v",
			leaf.Status.ManagerAdmission, leaf.Status.ManagerDrain, currentRollout.Status.Targets)
	}
	_, ledger, err := fixture.rolloutReconciler.ledgerStore(&rollout).Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, retained := ledger.Reservations[leaf.Status.ManagerAdmission.ReservationID]; retained ||
		ledger.LastReleaseFence == nil || ledger.LastReleaseFence.DrainSessionToken != fixture.sessionToken ||
		ledger.LastReleaseFence.DrainChildUID != string(leaf.UID) {
		t.Fatalf("drain ledger did not settle with exact provenance: %#v", ledger)
	}
	assertDrainTopologyLock(t, fixture.rolloutReconciler, fixture.device, true)

	fixture.clock.now = settledAt.Add(time.Second)
	if err := fixture.deviceReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.device), &device); err != nil {
		t.Fatal(err)
	}
	if err := fixture.deviceReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.node), &node); err != nil {
		t.Fatal(err)
	}
	decision = fixture.deviceReconciler.resolveManagedMaintenance(ctx, &device, &node)
	if decision.err != nil || decision.session == nil ||
		decision.session.Phase != ciskov1.DeviceMaintenanceSessionSettled ||
		decision.session.ControlRevision != rollout.Spec.Control.Revision ||
		decision.session.SessionToken != fixture.sessionToken {
		t.Fatalf("settled maintenance acknowledgement = %#v", decision)
	}
	if err := fixture.deviceReconciler.patchManagedTopologyStatus(
		ctx, &device, &node, fixture.target.ProjectionHash, decision,
	); err != nil {
		t.Fatal(err)
	}

	finishedAt := settledAt.Add(2 * time.Second)
	fixture.rolloutReconciler.Now = func() time.Time { return finishedAt }
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &rollout); err != nil {
		t.Fatal(err)
	}
	result, err = fixture.rolloutReconciler.reconcileCancellation(ctx, &rollout, fixture.policy, finishedAt)
	if err != nil {
		t.Fatalf("finish acknowledged cancellation: %v", err)
	}
	if result.RequeueAfter != 0 {
		var pending opsv1alpha1.IOSXESoftwareRollout
		_ = fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &pending)
		t.Fatalf("finished cancellation result = %#v, want terminal result; targets=%#v", result, pending.Status.Targets)
	}
	if err := fixture.rolloutReconciler.Get(ctx, client.ObjectKeyFromObject(fixture.rollout), &rollout); err != nil {
		t.Fatal(err)
	}
	if rollout.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled {
		t.Fatalf("campaign phase = %s, want Cancelled", rollout.Status.Phase)
	}
	assertDrainTopologyLock(t, fixture.rolloutReconciler, fixture.device, false)
}

func TestDeletionRetainsFinalizerUntilSettledDrainAcknowledgement(t *testing.T) {
	t.Parallel()
	r, rollout, leaf, device := settledDrainAcknowledgementFixture(t)
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rollout), rollout); err != nil {
		t.Fatal(err)
	}
	rollout.Finalizers = []string{rolloutSafetyFinalizer}
	if err := r.Update(context.Background(), rollout); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	result, err := r.reconcileDeletion(context.Background(), rollout, now)
	if err != nil {
		t.Fatalf("reconcileDeletion() before device acknowledgement: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("deletion became terminal before the CiscoDevice acknowledged drain settlement")
	}
	var currentRollout opsv1alpha1.IOSXESoftwareRollout
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rollout), &currentRollout); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&currentRollout, rolloutSafetyFinalizer) {
		t.Fatal("rollout safety finalizer was removed before drain acknowledgement")
	}
	assertSettledDrainBindingPreserved(t, r, leaf)
	assertDrainTopologyLock(t, r, device, true)

	acknowledgeSettledDrain(t, r, device)
	result, err = r.reconcileDeletion(context.Background(), &currentRollout, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reconcileDeletion() after device acknowledgement: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("settled deletion result = %#v, want terminal result", result)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(rollout), &currentRollout); err != nil {
		t.Fatal(err)
	}
	if controllerutil.ContainsFinalizer(&currentRollout, rolloutSafetyFinalizer) {
		t.Fatal("rollout safety finalizer remains after exact drain acknowledgement and lock release")
	}
	assertDrainTopologyLock(t, r, device, false)
}

func TestTerminalDrainDeletionDoesNotReplayReplaceableSettlement(t *testing.T) {
	t.Parallel()
	for _, phase := range []opsv1alpha1.IOSXESoftwareRolloutPhase{
		opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled,
		opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded,
	} {
		t.Run(string(phase), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, rollout, _, _, leaseName := settledDrainSuccessorFixture(t)
			target := rollout.Status.FrozenPlan.Targets[0]

			// A later active session and held Lease must not make a terminal old
			// campaign replay successor-owned settlement state.
			var device ciskov1.CiscoDevice
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
				t.Fatal(err)
			}
			device.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionActive
			if err := r.Status().Update(ctx, &device); err != nil {
				t.Fatal(err)
			}
			var lease coordv1.Lease
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: leaseName}, &lease); err != nil {
				t.Fatal(err)
			}
			setTestLeaseHeld(&lease)
			if err := r.Update(ctx, &lease); err != nil {
				t.Fatal(err)
			}

			var current opsv1alpha1.IOSXESoftwareRollout
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			current.Finalizers = []string{rolloutSafetyFinalizer}
			if err := r.Update(ctx, &current); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			current.Status.Phase = phase
			if err := r.Status().Update(ctx, &current); err != nil {
				t.Fatal(err)
			}

			// Terminal deletion must not depend on policy/ledger bootstrap.
			var policy corev1.ConfigMap
			policyKey := types.NamespacedName{
				Namespace: rollout.Status.FrozenPlan.Policy.Namespace,
				Name:      rollout.Status.FrozenPlan.Policy.Name,
			}
			if err := r.Get(ctx, policyKey, &policy); err != nil {
				t.Fatal(err)
			}
			if err := r.Delete(ctx, &policy); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			if _, err := r.reconcileDeletion(ctx, &current, time.Date(2026, 9, 12, 12, 10, 0, 0, time.UTC)); err != nil {
				t.Fatalf("terminal deletion: %v", err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			if controllerutil.ContainsFinalizer(&current, rolloutSafetyFinalizer) {
				t.Fatal("terminal rollout replayed obsolete settlement instead of releasing its finalizer")
			}
		})
	}
}

func TestDeletingSettledDrainAcceptsBoundSettledSuccessorAcrossClockSkew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		protocol               ciskov1.DeviceMaintenanceProtocolVersion
		requestedSkew          time.Duration
		admissionRevisionDelta int64
	}{
		{name: "legacy successor with earlier clock and newer admission revision", requestedSkew: -time.Minute, admissionRevisionDelta: 2},
		{name: "rollout-v1 successor with equal clock and newer admission revision", protocol: ciskov1.DeviceMaintenanceProtocolRolloutV1, admissionRevisionDelta: 1},
		{name: "PDB successor with equal clock", protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, rollout, _, successorName, _ := settledDrainSuccessorFixtureForProtocol(t, test.protocol, test.requestedSkew)
			if test.admissionRevisionDelta > 0 {
				var successor opsv1alpha1.IOSXESoftwareUpgrade
				if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: successorName}, &successor); err != nil {
					t.Fatal(err)
				}
				*successor.Status.ManagerAdmission.ControlRevision += test.admissionRevisionDelta
				if err := r.Status().Update(ctx, &successor); err != nil {
					t.Fatal(err)
				}
			}

			var current opsv1alpha1.IOSXESoftwareRollout
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			current.Finalizers = []string{rolloutSafetyFinalizer}
			if err := r.Update(ctx, &current); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			if _, err := r.reconcileDeletion(ctx, &current, time.Date(2026, 9, 12, 12, 10, 0, 0, time.UTC)); err != nil {
				t.Fatalf("superseded deletion: %v", err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			if controllerutil.ContainsFinalizer(&current, rolloutSafetyFinalizer) {
				t.Fatalf("settled maintenance proof did not release the stale rollout finalizer: phase=%s targets=%#v", current.Status.Phase, current.Status.Targets)
			}
		})
	}
}

func TestDeletingSettledDrainRejectsUnprovenSuccessor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		protocol ciskov1.DeviceMaintenanceProtocolVersion
		mutate   func(*ciskov1.CiscoDevice, *opsv1alpha1.IOSXESoftwareUpgrade, *coordv1.Lease)
	}{
		{
			name: "active successor",
			mutate: func(device *ciskov1.CiscoDevice, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				device.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionActive
			},
		},
		{
			name: "wrong Node incarnation",
			mutate: func(device *ciskov1.CiscoDevice, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				device.Status.MaintenanceSession.NodeUID = "replacement-node"
			},
		},
		{
			name: "missing acknowledgement",
			mutate: func(device *ciskov1.CiscoDevice, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				device.Status.MaintenanceSession.AcknowledgedAt = nil
			},
		},
		{
			name: "acknowledgement precedes request",
			mutate: func(device *ciskov1.CiscoDevice, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				acknowledgedAt := metav1.NewTime(device.Status.MaintenanceSession.RequestedAt.Add(-time.Second))
				device.Status.MaintenanceSession.AcknowledgedAt = &acknowledgedAt
			},
		},
		{
			name: "successor outcome is not settled",
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionGranted
			},
		},
		{
			name: "successor leaf is nonterminal",
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			},
		},
		{
			name: "admission leaf UID mismatch",
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerAdmission.LeafUID = "replacement-leaf"
			},
		},
		{
			name: "admission device UID mismatch",
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerAdmission.DeviceUID = "replacement-device"
			},
		},
		{
			name: "admission Node UID mismatch",
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerAdmission.NodeUID = "replacement-node"
			},
		},
		{
			name: "admission revision precedes session",
			mutate: func(device *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				*successor.Status.ManagerAdmission.ControlRevision = device.Status.MaintenanceSession.ControlRevision - 1
			},
		},
		{
			name: "partial old session identity reuse",
			mutate: func(device *ciskov1.CiscoDevice, _ *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				device.Status.MaintenanceSession.SessionToken = "11111111-1111-4111-8111-111111111111"
			},
		},
		{
			name:     "PDB token mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.SessionToken = "33333333-3333-4333-8333-333333333333"
			},
		},
		{
			name:     "PDB drain is not settled",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainRecovering
			},
		},
		{
			name:     "PDB revision mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.ControlRevision++
			},
		},
		{
			name:     "PDB admission revision mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				(*successor.Status.ManagerAdmission.ControlRevision)++
			},
		},
		{
			name:     "PDB request time mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.StartedAt = metav1.NewTime(successor.Status.ManagerDrain.StartedAt.Add(time.Second))
			},
		},
		{
			name:     "PDB reservation mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.ReservationID = "replacement-reservation"
			},
		},
		{
			name:     "PDB policy epoch mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.PolicyEpoch++
			},
		},
		{
			name:     "PDB drain Node mismatch",
			protocol: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			mutate: func(_ *ciskov1.CiscoDevice, successor *opsv1alpha1.IOSXESoftwareUpgrade, _ *coordv1.Lease) {
				successor.Status.ManagerDrain.NodeUID = "replacement-node"
			},
		},
		{
			name: "canonical Lease is held",
			mutate: func(_ *ciskov1.CiscoDevice, _ *opsv1alpha1.IOSXESoftwareUpgrade, lease *coordv1.Lease) {
				setTestLeaseHeld(lease)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, rollout, oldLeaf, successorName, leaseName := settledDrainSuccessorFixtureForProtocol(t, test.protocol, 0)
			target := rollout.Status.FrozenPlan.Targets[0]

			var device ciskov1.CiscoDevice
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
				t.Fatal(err)
			}
			var successor opsv1alpha1.IOSXESoftwareUpgrade
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: successorName}, &successor); err != nil {
				t.Fatal(err)
			}
			var lease coordv1.Lease
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: leaseName}, &lease); err != nil {
				t.Fatal(err)
			}
			test.mutate(&device, &successor, &lease)
			if err := r.Status().Update(ctx, &device); err != nil {
				t.Fatal(err)
			}
			if err := r.Status().Update(ctx, &successor); err != nil {
				t.Fatal(err)
			}
			if err := r.Update(ctx, &lease); err != nil {
				t.Fatal(err)
			}

			superseded, observed, detail, err := r.deletionDrainSettlementSuperseded(
				ctx, rollout, target, oldLeaf,
			)
			if err != nil {
				t.Fatalf("deletionDrainSettlementSuperseded(): %v", err)
			}
			if superseded || !observed || detail == "" {
				t.Fatalf("unproven successor = superseded %v, observed %v, detail %q", superseded, observed, detail)
			}
		})
	}
}

func TestDeletingSettledDrainDoesNotReplaySettlementWhileSuccessorIsUnproven(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice, *coordv1.Lease)
	}{
		{
			name: "active successor",
			mutate: func(device *ciskov1.CiscoDevice, _ *coordv1.Lease) {
				device.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionActive
			},
		},
		{
			name: "held successor Lease",
			mutate: func(_ *ciskov1.CiscoDevice, lease *coordv1.Lease) {
				setTestLeaseHeld(lease)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, rollout, _, _, leaseName := settledDrainSuccessorFixture(t)
			target := rollout.Status.FrozenPlan.Targets[0]

			if err := r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
				return topologyrollout.FenceUnboundAcquisition(
					ledger, "successor-release-fence", 2, strings.Repeat("e", 32),
				)
			}); err != nil {
				t.Fatalf("seed successor release fence: %v", err)
			}
			ledgerKey := types.NamespacedName{
				Namespace: rollout.Status.FrozenPlan.Policy.LedgerNamespace,
				Name:      rollout.Status.FrozenPlan.Policy.LedgerName,
			}
			var ledgerBefore corev1.ConfigMap
			if err := r.Get(ctx, ledgerKey, &ledgerBefore); err != nil {
				t.Fatal(err)
			}

			var device ciskov1.CiscoDevice
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
				t.Fatal(err)
			}
			var lease coordv1.Lease
			if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: leaseName}, &lease); err != nil {
				t.Fatal(err)
			}
			test.mutate(&device, &lease)
			if err := r.Status().Update(ctx, &device); err != nil {
				t.Fatal(err)
			}
			if err := r.Update(ctx, &lease); err != nil {
				t.Fatal(err)
			}

			var current opsv1alpha1.IOSXESoftwareRollout
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			current.Finalizers = []string{rolloutSafetyFinalizer}
			if err := r.Update(ctx, &current); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			result, err := r.reconcileDeletion(ctx, &current, time.Date(2026, 9, 12, 12, 10, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("reconcileDeletion(): %v", err)
			}
			if result.RequeueAfter == 0 {
				t.Fatal("unproven successor did not keep deletion converging")
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(rollout), &current); err != nil {
				t.Fatal(err)
			}
			if !controllerutil.ContainsFinalizer(&current, rolloutSafetyFinalizer) {
				t.Fatal("unproven successor allowed deletion to release its finalizer")
			}
			var ledgerAfter corev1.ConfigMap
			if err := r.Get(ctx, ledgerKey, &ledgerAfter); err != nil {
				t.Fatal(err)
			}
			if got, want := ledgerAfter.Data[topologyrollout.LedgerDataKey], ledgerBefore.Data[topologyrollout.LedgerDataKey]; got != want {
				t.Fatalf("unproven successor replayed old settlement:\n got %s\nwant %s", got, want)
			}
		})
	}
}

func TestDeletingSettledDrainRejectsUnreleasedOldCampaignState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prepare func(*testing.T, context.Context, *IOSXESoftwareRolloutReconciler, *opsv1alpha1.IOSXESoftwareRollout, opsv1alpha1.IOSXESoftwareRolloutPlannedTarget, *opsv1alpha1.IOSXESoftwareUpgrade)
	}{
		{
			name: "old reservation remains",
			prepare: func(t *testing.T, ctx context.Context, r *IOSXESoftwareRolloutReconciler,
				rollout *opsv1alpha1.IOSXESoftwareRollout, target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
				leaf *opsv1alpha1.IOSXESoftwareUpgrade,
			) {
				seed := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
					map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationBound)
				seedLedger, err := topologyrollout.Decode(
					[]byte(seed.Data[topologyrollout.LedgerDataKey]), string(seed.UID),
				)
				if err != nil {
					t.Fatal(err)
				}
				reservation := seedLedger.Reservations[leaf.Status.ManagerDrain.ReservationID]
				if err := r.ledgerStore(rollout).Mutate(ctx, func(ledger *topologyrollout.Ledger) error {
					ledger.Reservations[reservation.ID] = reservation
					return nil
				}); err != nil {
					t.Fatalf("restore old reservation: %v", err)
				}
			},
		},
		{
			name: "old topology lock remains",
			prepare: func(t *testing.T, ctx context.Context, r *IOSXESoftwareRolloutReconciler,
				rollout *opsv1alpha1.IOSXESoftwareRollout, target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
				leaf *opsv1alpha1.IOSXESoftwareUpgrade,
			) {
				var device ciskov1.CiscoDevice
				if err := r.Get(ctx, types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
					t.Fatal(err)
				}
				device.Status.TopologyLock = expectedDeviceTopologyLock(
					rollout, target, leaf.Status.ManagerAdmission.PolicyEpoch,
					leaf.Status.ManagerAdmission.TopologyLockID, time.Date(2026, 9, 12, 12, 5, 0, 0, time.UTC),
				)
				if err := r.Status().Update(ctx, &device); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale Pod protection remains",
			prepare: func(t *testing.T, ctx context.Context, r *IOSXESoftwareRolloutReconciler,
				_ *opsv1alpha1.IOSXESoftwareRollout, _ opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
				leaf *opsv1alpha1.IOSXESoftwareUpgrade,
			) {
				frozen := opsv1alpha1.UpgradeDrainPodStatus{
					Namespace: "apps", Name: "stale-protected", UID: "stale-protected-uid",
					Phase: opsv1alpha1.UpgradeDrainPodComplete,
					PDBs: []opsv1alpha1.UpgradeDrainPDBStatus{{
						UpgradeDrainObjectReference: opsv1alpha1.UpgradeDrainObjectReference{
							APIVersion: "policy/v1", Kind: "PodDisruptionBudget", Namespace: "apps",
							Name: "stale-protected", UID: "stale-pdb-uid", Generation: 1,
						},
						ObservedGeneration: 1, DisruptionsAllowed: 1, CurrentHealthy: 1,
						DesiredHealthy: 1, ExpectedPods: 1,
					}},
				}
				setDrainEligibilityHash(t, &frozen)
				leaf.Status.ManagerDrain.Pods = []opsv1alpha1.UpgradeDrainPodStatus{frozen}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
					Namespace: frozen.Namespace, Name: frozen.Name, UID: types.UID(frozen.UID),
					// A marker without its paired finalizer is partial protection and
					// cannot be interpreted or repaired as an exact old-session write.
					Annotations: map[string]string{
						managedprotocol.AnnotationDrainSession: leaf.Status.ManagerDrain.SessionToken,
					},
				}}
				if err := r.Create(ctx, pod); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, rollout, oldLeaf, _, _ := settledDrainSuccessorFixture(t)
			target := rollout.Status.FrozenPlan.Targets[0]
			test.prepare(t, ctx, r, rollout, target, oldLeaf)

			superseded, observed, detail, err := r.deletionDrainSettlementSuperseded(
				ctx, rollout, target, oldLeaf,
			)
			if err != nil {
				t.Fatalf("deletionDrainSettlementSuperseded(): %v", err)
			}
			if superseded || !observed || detail == "" {
				t.Fatalf("unreleased old state = superseded %v, observed %v, detail %q", superseded, observed, detail)
			}
		})
	}
}

func TestDeletingSettledDrainRejectsFreshIncarnationOrSuccessorSessionSwap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		wantSuccessorReads int
		mutateRead         func(client.Object)
	}{
		{
			name: "device delete and recreate",
			mutateRead: func(object client.Object) {
				device, ok := object.(*ciskov1.CiscoDevice)
				if !ok {
					return
				}
				device.UID = "replacement-device-uid"
				device.Status.MaintenanceSession = nil
			},
		},
		{
			name: "Node delete and recreate",
			mutateRead: func(object client.Object) {
				node, ok := object.(*corev1.Node)
				if ok {
					node.UID = "replacement-node-uid"
				}
			},
		},
		{
			name: "successor session swaps between proofs",
			mutateRead: func(object client.Object) {
				device, ok := object.(*ciskov1.CiscoDevice)
				if ok && device.Status.MaintenanceSession != nil {
					device.Status.MaintenanceSession.SessionToken = "replacement-successor-session"
				}
			},
		},
		{
			name:               "successor leaf delete and recreate",
			wantSuccessorReads: 1,
			mutateRead: func(object client.Object) {
				successor, ok := object.(*opsv1alpha1.IOSXESoftwareUpgrade)
				if ok {
					successor.UID = "replacement-successor-uid"
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			r, rollout, oldLeaf, _, _ := settledDrainSuccessorFixture(t)
			target := rollout.Status.FrozenPlan.Targets[0]
			base, ok := r.Client.(client.WithWatch)
			if !ok {
				t.Fatal("fixture client does not implement client.WithWatch")
			}
			deviceReads := 0
			nodeReads := 0
			successorReads := 0
			r.APIReader = interceptor.NewClient(base, interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
					if err := underlying.Get(ctx, key, object, opts...); err != nil {
						return err
					}
					switch object.(type) {
					case *ciskov1.CiscoDevice:
						deviceReads++
						if deviceReads == 2 {
							test.mutateRead(object)
						}
					case *corev1.Node:
						nodeReads++
						if nodeReads == 1 {
							test.mutateRead(object)
						}
					case *opsv1alpha1.IOSXESoftwareUpgrade:
						successorReads++
						if successorReads == 1 {
							test.mutateRead(object)
						}
					}
					return nil
				},
			})

			superseded, observed, detail, err := r.deletionDrainSettlementSuperseded(
				ctx, rollout, target, oldLeaf,
			)
			if err != nil {
				t.Fatalf("deletionDrainSettlementSuperseded(): %v", err)
			}
			if superseded || !observed || detail == "" {
				t.Fatalf("racing successor proof = superseded %v, observed %v, detail %q", superseded, observed, detail)
			}
			if deviceReads != 2 || nodeReads != 1 || successorReads != test.wantSuccessorReads {
				t.Fatalf("fresh authority reads = device %d, Node %d, successor %d, want 2, 1, and %d",
					deviceReads, nodeReads, successorReads, test.wantSuccessorReads)
			}
		})
	}
}

func settledDrainSuccessorFixture(
	t *testing.T,
) (*IOSXESoftwareRolloutReconciler, *opsv1alpha1.IOSXESoftwareRollout, *opsv1alpha1.IOSXESoftwareUpgrade, string, string) {
	t.Helper()
	return settledDrainSuccessorFixtureForProtocol(t, ciskov1.DeviceMaintenanceProtocolRolloutV1, 0)
}

func settledDrainSuccessorFixtureForProtocol(
	t *testing.T,
	protocol ciskov1.DeviceMaintenanceProtocolVersion,
	requestedSkew time.Duration,
) (*IOSXESoftwareRolloutReconciler, *opsv1alpha1.IOSXESoftwareRollout, *opsv1alpha1.IOSXESoftwareUpgrade, string, string) {
	t.Helper()
	ctx := context.Background()
	r, rollout, leaf, device := settledDrainAcknowledgementFixture(t)
	target := rollout.Status.FrozenPlan.Targets[0]
	acknowledgeSettledDrain(t, r, device)
	if err := r.finishSettledDrain(ctx, rollout, target, leaf); err != nil {
		t.Fatalf("finish old settled drain: %v", err)
	}

	const successorName = "successor-upgrade"
	const successorUID = "successor-upgrade-uid"
	const successorReservationID = "successor-reservation"
	const successorPolicyEpoch = int64(2)
	const successorControlRevision = int64(11)
	successorAdmission := &opsv1alpha1.UpgradeManagerAdmissionStatus{
		ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
		State:           opsv1alpha1.UpgradeManagerAdmissionSettled,
		PolicyEpoch:     successorPolicyEpoch,
		ReservationID:   successorReservationID,
		LeafUID:         successorUID,
		DeviceUID:       target.DeviceUID,
		NodeUID:         target.NodeUID,
		ControlRevision: ptr.To(successorControlRevision),
	}
	successor := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace, Name: successorName, UID: successorUID,
		},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{
			DeviceRef: leaf.Spec.DeviceRef,
		},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
			Phase:            opsv1alpha1.UpgradePhaseFailed,
			ManagerAdmission: successorAdmission,
		},
	}
	var currentDevice ciskov1.CiscoDevice
	if err := r.Get(ctx, client.ObjectKeyFromObject(device), &currentDevice); err != nil {
		t.Fatal(err)
	}
	leaseRef := currentDevice.Status.MaintenanceSession.Lease
	requestedAt := metav1.NewTime(leaf.Status.ManagerDrain.UpdatedAt.Add(requestedSkew))
	acknowledgedAt := metav1.NewTime(requestedAt.Add(time.Second))
	sessionToken := "successor-session-0001"
	purpose := ciskov1.DeviceMaintenancePurpose("")
	holder := "software-upgrade/" + successorUID
	if protocol == ciskov1.DeviceMaintenanceProtocolPDBDrainV1 {
		sessionToken = "22222222-2222-4222-8222-222222222222"
		purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
		recoveryDeadline := metav1.NewTime(requestedAt.Add(20 * time.Minute))
		successor.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
			ProtocolVersion:  opsv1alpha1.ManagedDrainProtocolPDBV1,
			State:            opsv1alpha1.UpgradeManagerDrainSettled,
			SessionToken:     sessionToken,
			ReservationID:    successorReservationID,
			PolicyEpoch:      successorPolicyEpoch,
			ControlRevision:  successorControlRevision,
			NodeUID:          target.NodeUID,
			StartedAt:        requestedAt,
			DrainDeadline:    metav1.NewTime(requestedAt.Add(10 * time.Minute)),
			RecoveryDeadline: &recoveryDeadline,
			UpdatedAt:        acknowledgedAt,
		}
	} else if protocol == ciskov1.DeviceMaintenanceProtocolRolloutV1 {
		purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
	}
	if err := r.Create(ctx, successor); err != nil {
		t.Fatal(err)
	}
	currentDevice.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase:           ciskov1.DeviceMaintenanceSessionSettled,
		ProtocolVersion: protocol,
		Purpose:         purpose,
		SessionToken:    sessionToken,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: leaseRef.DeviceMaintenanceObjectReference,
			Holder:                           holder,
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: rollout.Namespace, Name: successorName, UID: successorUID,
		},
		DeviceUID: target.DeviceUID, NodeName: target.NodeName, NodeUID: target.NodeUID,
		RequestedAt: requestedAt, AcknowledgedAt: &acknowledgedAt, ControlRevision: successorControlRevision,
	}
	if err := r.Status().Update(ctx, &currentDevice); err != nil {
		t.Fatal(err)
	}
	return r, rollout, leaf, successorName, leaseRef.Name
}

func setTestLeaseHeld(lease *coordv1.Lease) {
	holder := "software-upgrade/active-successor"
	heldAt := metav1.NewMicroTime(time.Date(2026, 9, 12, 12, 5, 0, 0, time.UTC))
	lease.Spec.HolderIdentity = &holder
	lease.Spec.LeaseDurationSeconds = ptr.To[int32](3600)
	lease.Spec.AcquireTime = &heldAt
	lease.Spec.RenewTime = &heldAt
	lease.Spec.LeaseTransitions = ptr.To[int32](1)
}

func TestPolicyAndControlTransitionPreservesSettledDrainAcknowledgementBinding(t *testing.T) {
	t.Parallel()
	r, rollout, leaf, _ := settledDrainAcknowledgementFixture(t)
	target := rollout.Status.FrozenPlan.Targets[0]
	rollout.Spec.Control.Revision++
	rollout.Status.PolicyTransition = &opsv1alpha1.IOSXESoftwareRolloutPolicyTransitionStatus{
		Epoch: rollout.Status.EffectivePolicy.Epoch + 1,
	}

	if err := r.ensurePolicyEpochFenceForTarget(
		context.Background(), rollout, nil, target, time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("ensurePolicyEpochFenceForTarget() with a settled drain: %v", err)
	}
	assertSettledDrainBindingPreserved(t, r, leaf)
}

type promotedZeroPodCancellationTestFixture struct {
	rolloutReconciler *IOSXESoftwareRolloutReconciler
	deviceReconciler  *CiscoDeviceReconciler
	rollout           *opsv1alpha1.IOSXESoftwareRollout
	target            opsv1alpha1.IOSXESoftwareRolloutPlannedTarget
	leaf              *opsv1alpha1.IOSXESoftwareUpgrade
	device            *ciskov1.CiscoDevice
	node              *corev1.Node
	lease             *coordv1.Lease
	policy            *topologyrollout.ParsedAdminPolicy
	clock             *fakeClock
	sessionToken      string
	startedAt         metav1.Time
}

func promotedZeroPodCancellationFixture(
	t *testing.T,
	now time.Time,
) *promotedZeroPodCancellationTestFixture {
	t.Helper()
	rollout, target, leaf, baseObjects := drainEvictionAuthorityFixture(t, now)
	rollout.Spec.Plan.Health.WaveSoakSeconds = 1

	var device *ciskov1.CiscoDevice
	var node *corev1.Node
	var policyConfigMap *corev1.ConfigMap
	workerObjects := make([]client.Object, 0, 3)
	for _, object := range baseObjects {
		switch current := object.(type) {
		case *ciskov1.CiscoDevice:
			device = current
		case *corev1.Node:
			node = current
		case *corev1.ConfigMap:
			policyConfigMap = current
		case *appsv1.Deployment:
			if current.Namespace == rollout.Namespace {
				workerObjects = append(workerObjects, current)
			}
		case *appsv1.ReplicaSet:
			if current.Namespace == rollout.Namespace {
				workerObjects = append(workerObjects, current)
			}
		case *corev1.Pod:
			if current.Namespace == rollout.Namespace {
				workerObjects = append(workerObjects, current)
			}
		}
	}
	if device == nil || node == nil || policyConfigMap == nil || len(workerObjects) != 3 {
		t.Fatal("drain fixture is missing managed device, Node, policy, or worker proof")
	}
	policyConfigMap.ResourceVersion = rollout.Status.FrozenPlan.Policy.ResourceVersion
	policy, err := topologyrollout.ParseAdminPolicy(policyConfigMap)
	if err != nil {
		t.Fatal(err)
	}
	policySnapshot, err := freezePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	rollout.Status.FrozenPlan.Policy = policySnapshot
	rollout.Status.EffectivePolicy.Policy = policySnapshot
	rollout.Status.Targets = []opsv1alpha1.IOSXESoftwareRolloutTargetStatus{{
		DeviceName: target.DeviceName, DeviceUID: target.DeviceUID,
		LeafName: target.ChildName, LeafUID: string(leaf.UID),
	}}

	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionGranted
	leaf.Status.ManagerAdmission.PolicyUID = policySnapshot.UID
	leaf.Status.ManagerAdmission.PolicyResourceVersion = policySnapshot.ResourceVersion
	leaf.Status.ManagerAdmission.LedgerUID = policySnapshot.LedgerUID
	leaf.Status.WorkerControl.ObservedAdmissionState = opsv1alpha1.UpgradeManagerAdmissionGranted
	leaf.Status.WorkerControl.EffectiveState = opsv1alpha1.UpgradeWorkerControlReady
	leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainPromoted
	leaf.Status.ManagerDrain.Pods = nil
	leaf.Status.ManagerDrain.UpdatedAt = metav1.NewTime(now.Add(-30 * time.Second))
	sessionToken := leaf.Status.ManagerDrain.SessionToken
	startedAt := leaf.Status.ManagerDrain.StartedAt

	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:        "true",
		"topology.cisco.vk/site":                 "site-a",
		managedprotocol.ImageFamilyLabel:         target.ImageFamily,
		managedprotocol.QualificationCohortLabel: target.QualificationCohort,
	}
	device.Spec.Driver = ciskov1.DeviceDriverXE
	device.Spec.NodeName = target.NodeName
	device.Spec.PhysicalIdentity = target.PhysicalIdentity
	device.Status.Phase = "Ready"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		NodeName: target.NodeName, NodeUID: target.NodeUID, DeviceUID: target.DeviceUID,
		PhysicalIdentity: target.PhysicalIdentity,
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{
		EffectiveLabelHash: target.ProjectionHash, LastSuccessfulTime: metav1.NewTime(now.Add(-time.Minute)),
	}
	device.Status.TopologyLock = expectedDeviceTopologyLock(
		rollout, target, leaf.Status.ManagerAdmission.PolicyEpoch,
		leaf.Status.ManagerAdmission.TopologyLockID, now.Add(-time.Minute),
	)
	conditionTime := metav1.NewTime(now.Add(-time.Minute))
	device.Status.Conditions = []metav1.Condition{
		{Type: ciskov1.CiscoDeviceConditionNodeIdentityReady, Status: metav1.ConditionTrue,
			ObservedGeneration: device.Generation, LastTransitionTime: conditionTime},
		{Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionTrue,
			ObservedGeneration: device.Generation, LastTransitionTime: conditionTime},
		{Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue,
			ObservedGeneration: device.Generation, LastTransitionTime: conditionTime},
	}

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	worker := "system:serviceaccount:" + device.Namespace + ":" + managedprotocol.NetworkManagementServiceAccount
	leaf.Annotations[managedprotocol.AnnotationWorkerUsername] = worker
	leaf.Annotations[managedprotocol.AnnotationNetworkWorkerUsername] = worker
	for key, value := range map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		managedprotocol.AnnotationDeviceNamespace:  device.Namespace,
		managedprotocol.AnnotationDeviceName:       device.Name,
		managedprotocol.AnnotationDeviceUID:        string(device.UID),
		managedprotocol.AnnotationNodeUID:          target.NodeUID,
		managedprotocol.AnnotationWorkerUsername:   worker,
		managedprotocol.AnnotationWorkerProtocol:   managedprotocol.Version,
		managedprotocol.AnnotationProjectionHash:   target.ProjectionHash,
		managedprotocol.AnnotationDrainCordonOwner: sessionToken,
		managedprotocol.AnnotationDrainTaintOwner:  sessionToken,
		managedprotocol.AnnotationManagedTaints:    encodeManagedTaints([]corev1.Taint{maintenanceGuardTaint()}),
	} {
		node.Annotations[key] = value
	}
	node.Labels = map[string]string{"topology.cisco.vk/site": "site-a"}
	node.Spec.Unschedulable = true
	node.Spec.Taints = []corev1.Taint{maintenanceGuardTaint()}
	node.Status.NodeInfo = corev1.NodeSystemInfo{
		MachineID: target.PhysicalIdentity, SystemUUID: target.PhysicalIdentity,
	}
	node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		LastHeartbeatTime: conditionTime, LastTransitionTime: metav1.NewTime(now.Add(-2 * time.Minute)),
	})

	leaseAnnotations, leaseLabels := managedMutationLeaseMetadata(device, target.NodeName, target.NodeUID, worker)
	for key, value := range map[string]string{
		managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.DrainProtocolVersion,
		managedprotocol.AnnotationMaintenanceSessionToken:    sessionToken,
		managedprotocol.AnnotationMaintenanceRequestedAt:     startedAt.Time.UTC().Format(time.RFC3339Nano),
		managedprotocol.AnnotationMaintenanceOperationNS:     leaf.Namespace,
		managedprotocol.AnnotationMaintenanceOperationName:   leaf.Name,
		managedprotocol.AnnotationMaintenanceOperationUID:    string(leaf.UID),
		managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(rollout.Spec.Control.Revision, 10),
		managedprotocol.AnnotationMaintenancePurpose:         managedprotocol.MaintenancePurposeSoftwareMutation,
	} {
		leaseAnnotations[key] = value
	}
	holder := mutationguard.UpgradeHolderIdentity(leaf)
	heldAt := metav1.NewMicroTime(now.Add(-30 * time.Second))
	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace,
			Name: engine.LeaseName(
				devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily,
			),
			UID: "lease-uid", Annotations: leaseAnnotations, Labels: leaseLabels,
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity: &holder, LeaseDurationSeconds: ptr.To[int32](3600),
			AcquireTime: &heldAt, RenewTime: &heldAt, LeaseTransitions: ptr.To[int32](1),
		},
	}
	acknowledgedAt := metav1.NewTime(startedAt.Add(time.Second))
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: ciskov1.DeviceMaintenanceSessionActive, ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
		Purpose: ciskov1.DeviceMaintenancePurposeSoftwareMutation, SessionToken: sessionToken,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID),
			},
			Holder: holder,
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
		},
		DeviceUID: target.DeviceUID, NodeName: target.NodeName, NodeUID: target.NodeUID,
		RequestedAt: startedAt, AcknowledgedAt: &acknowledgedAt,
		ControlRevision: rollout.Spec.Control.Revision,
	}

	ledgerConfigMap := policyFenceLedger(
		t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
		map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationBound,
	)
	ledger, err := topologyrollout.Decode(
		[]byte(ledgerConfigMap.Data[topologyrollout.LedgerDataKey]), string(ledgerConfigMap.UID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := topologyrollout.BeginDrain(
		ledger, leaf.Status.ManagerAdmission.ReservationID, policySnapshot.LedgerUID,
		string(leaf.UID), sessionToken, uint64(rollout.Spec.Control.Revision), startedAt.Time,
	); err != nil {
		t.Fatal(err)
	}
	if err := topologyrollout.PromoteDrain(
		ledger, leaf.Status.ManagerAdmission.ReservationID, policySnapshot.LedgerUID,
		string(leaf.UID), sessionToken, uint64(rollout.Spec.Control.Revision), startedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	encodedLedger, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerConfigMap.Data[topologyrollout.LedgerDataKey] = string(encodedLedger)

	objects := []client.Object{rollout, leaf, device, node, lease, policyConfigMap, ledgerConfigMap}
	objects = append(objects, workerObjects...)
	scheme := drainTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(
			&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{},
			&ciskov1.CiscoDevice{}, &corev1.Node{},
		).
		WithIndex(&corev1.Pod{}, rolloutPodNodeNameIndex, rolloutPodNodeNameIndexValues).
		WithObjects(objects...).Build()
	rolloutReconciler := &IOSXESoftwareRolloutReconciler{
		Client: apiClient, APIReader: apiClient,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
	}
	testClock := &fakeClock{now: now}
	deviceReconciler := &CiscoDeviceReconciler{
		Client: apiClient, APIReader: apiClient, Scheme: scheme,
		LeaseNamespace:          lease.Namespace,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		clock: testClock,
	}
	return &promotedZeroPodCancellationTestFixture{
		rolloutReconciler: rolloutReconciler, deviceReconciler: deviceReconciler,
		rollout: rollout, target: target, leaf: leaf, device: device, node: node, lease: lease,
		policy: policy, clock: testClock, sessionToken: sessionToken, startedAt: startedAt,
	}
}

func settledDrainAcknowledgementFixture(
	t *testing.T,
) (*IOSXESoftwareRolloutReconciler, *opsv1alpha1.IOSXESoftwareRollout, *opsv1alpha1.IOSXESoftwareUpgrade, *ciskov1.CiscoDevice) {
	t.Helper()
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Plan.Workloads = drainTestRollout().Spec.Plan.Workloads
	rollout.Status.FrozenPlan.Policy.ResourceVersion = "1"
	rollout.Status.FrozenPlan.Policy.LedgerName = "cvk-rollout-ledger"
	rollout.Status.EffectivePolicy.Policy = rollout.Status.FrozenPlan.Policy
	leaf := policyFenceLeaf(rollout, target, "leaf-uid")
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled

	now := time.Date(2026, 9, 12, 11, 55, 0, 0, time.UTC)
	startedAt := metav1.NewTime(now.Add(-time.Minute))
	acknowledgedAt := metav1.NewTime(startedAt.Add(time.Second))
	leaf.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
		State:           opsv1alpha1.UpgradeManagerDrainSettled,
		SessionToken:    "11111111-1111-4111-8111-111111111111",
		ReservationID:   leaf.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:     leaf.Status.ManagerAdmission.PolicyEpoch,
		ControlRevision: rollout.Spec.Control.Revision,
		NodeUID:         target.NodeUID,
		StartedAt:       startedAt,
		DrainDeadline:   metav1.NewTime(startedAt.Add(10 * time.Minute)),
		RecoveryDeadline: ptr.To(
			metav1.NewTime(now.Add(5 * time.Minute)),
		),
		UpdatedAt: metav1.NewTime(now),
	}

	device := topologyLockDevice(target)
	device.Status.TopologyLock = expectedDeviceTopologyLock(
		rollout, target, leaf.Status.ManagerAdmission.PolicyEpoch,
		leaf.Status.ManagerAdmission.TopologyLockID, now,
	)
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase:           ciskov1.DeviceMaintenanceSessionRecovering,
		ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
		Purpose:         ciskov1.DeviceMaintenancePurposeSoftwareMutation,
		SessionToken:    leaf.Status.ManagerDrain.SessionToken,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: rollout.Namespace, Name: "mutation", UID: "lease-uid",
			},
			Holder: "software-upgrade/" + string(leaf.UID),
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
		},
		DeviceUID:       target.DeviceUID,
		NodeName:        target.NodeName,
		NodeUID:         target.NodeUID,
		RequestedAt:     startedAt,
		AcknowledgedAt:  &acknowledgedAt,
		ControlRevision: rollout.Spec.Control.Revision,
	}
	worker := leaf.Annotations[managedprotocol.AnnotationWorkerUsername]
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: target.NodeName, UID: types.UID(target.NodeUID),
		Annotations: map[string]string{managedprotocol.AnnotationWorkerUsername: worker},
	}}
	leaseAnnotations, leaseLabels := managedMutationLeaseMetadata(device, target.NodeName, target.NodeUID, worker)
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Namespace, Name: "mutation", UID: "lease-uid",
		Annotations: leaseAnnotations, Labels: leaseLabels,
	}}

	ledgerCM := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
		map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationBound)
	ledger, err := topologyrollout.Decode(
		[]byte(ledgerCM.Data[topologyrollout.LedgerDataKey]), string(ledgerCM.UID),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := topologyrollout.SettleDrainedReservation(
		ledger, leaf.Status.ManagerAdmission.ReservationID, leaf.Status.ManagerAdmission.LedgerUID,
		leaf.Status.ManagerAdmission.TopologyLockID, string(leaf.UID), leaf.Status.ManagerDrain.SessionToken,
		leaf.Status.ManagerAdmission.PolicyEpoch, true, true, true,
	); err != nil {
		t.Fatal(err)
	}
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM.Data[topologyrollout.LedgerDataKey] = string(encoded)
	policyCM, _ := managedPolicyAndLedger(t, nil)

	scheme := drainTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(
			&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}, &ciskov1.CiscoDevice{},
		).
		WithObjects(rollout, leaf, device, node, lease, ledgerCM, policyCM).Build()
	return &IOSXESoftwareRolloutReconciler{
		Client: apiClient, APIReader: apiClient,
		TopologyPolicyNamespace: policyCM.Namespace, TopologyPolicyName: policyCM.Name,
	}, rollout, leaf, device
}

func assertSettledDrainBindingPreserved(
	t *testing.T,
	r *IOSXESoftwareRolloutReconciler,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
) {
	t.Helper()
	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(leaf), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.ManagerAdmission == nil || current.Status.ManagerAdmission.ControlRevision == nil ||
		current.Status.ManagerControl == nil || current.Status.ManagerDrain == nil ||
		*current.Status.ManagerAdmission.ControlRevision != current.Status.ManagerDrain.ControlRevision ||
		current.Status.ManagerControl.Revision != current.Status.ManagerDrain.ControlRevision ||
		current.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainSettled {
		t.Fatalf("settled drain terminal binding changed while acknowledgement was pending: %#v", current.Status)
	}
}

func assertDrainTopologyLock(
	t *testing.T,
	r *IOSXESoftwareRolloutReconciler,
	device *ciskov1.CiscoDevice,
	want bool,
) {
	t.Helper()
	var current ciskov1.CiscoDevice
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if (current.Status.TopologyLock != nil) != want {
		t.Fatalf("topology lock present = %v, want %v", current.Status.TopologyLock != nil, want)
	}
}

func acknowledgeSettledDrain(
	t *testing.T,
	r *IOSXESoftwareRolloutReconciler,
	device *ciskov1.CiscoDevice,
) {
	t.Helper()
	var current ciskov1.CiscoDevice
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	current.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionSettled
	if err := r.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
}

func TestFailureFenceAtomicallyRenewsRecoveringDrainAtNewRevision(t *testing.T) {
	t.Parallel()
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Plan.Workloads = drainTestRollout().Spec.Plan.Workloads
	leaf := policyFenceLeaf(rollout, target, "leaf-uid")
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	started := now.Add(-20 * time.Minute)
	leaf.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion:  opsv1alpha1.ManagedDrainProtocolPDBV1,
		State:            opsv1alpha1.UpgradeManagerDrainRecovering,
		SessionToken:     "11111111-1111-4111-8111-111111111111",
		ReservationID:    leaf.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:      leaf.Status.ManagerAdmission.PolicyEpoch,
		ControlRevision:  rollout.Spec.Control.Revision,
		NodeUID:          target.NodeUID,
		StartedAt:        metav1.NewTime(started),
		DrainDeadline:    metav1.NewTime(started.Add(10 * time.Minute)),
		RecoveryDeadline: ptr.To(metav1.NewTime(now.Add(-time.Second))),
		UpdatedAt:        metav1.NewTime(started.Add(10 * time.Minute)),
	}
	ledgerCM := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
		map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationGranted)
	rollout.Spec.Control.Revision++
	scheme := drainTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(leaf, ledgerCM).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	children := map[string]opsv1alpha1.IOSXESoftwareUpgrade{leaf.Name: *leaf.DeepCopy()}
	if err := r.ensureFailureFences(context.Background(), rollout,
		&topologyrollout.ParsedAdminPolicy{}, children, now); err == nil {
		t.Fatal("failure fence unexpectedly settled incomplete drain fixture")
	}
	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.ManagerAdmission == nil || current.Status.ManagerAdmission.ControlRevision == nil ||
		*current.Status.ManagerAdmission.ControlRevision != rollout.Spec.Control.Revision ||
		current.Status.ManagerControl == nil || current.Status.ManagerControl.Revision != rollout.Spec.Control.Revision ||
		current.Status.ManagerDrain == nil || current.Status.ManagerDrain.ControlRevision != rollout.Spec.Control.Revision {
		t.Fatalf("failure fence did not atomically synchronize revision %d: %#v",
			rollout.Spec.Control.Revision, current.Status)
	}
	wantDeadline := now.Add(12 * time.Minute)
	if current.Status.ManagerDrain.RecoveryDeadline == nil ||
		!current.Status.ManagerDrain.RecoveryDeadline.Time.Equal(wantDeadline) {
		t.Fatalf("failure-fence recovery deadline = %v, want %s",
			current.Status.ManagerDrain.RecoveryDeadline, wantDeadline)
	}
}

func TestDrainWorkerRecoveryAllowsReplacementButActiveDrainDoesNot(t *testing.T) {
	t.Parallel()
	now := metav1.NewTime(time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	target := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: "apps", Name: "edge-a", UID: "pod-a", Phase: opsv1alpha1.UpgradeDrainPodTerminationObserved,
		DeletionObservedAt: &now,
	}
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{
		State: opsv1alpha1.UpgradeManagerDrainRecovering, SessionToken: "11111111-1111-4111-8111-111111111111",
		PolicyEpoch: 3, ControlRevision: 8,
		Pods: []opsv1alpha1.UpgradeDrainPodStatus{target, {
			Namespace: "apps", Name: "edge-b", UID: "pod-b", Phase: opsv1alpha1.UpgradeDrainPodComplete,
		}},
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerDrain:  drain,
		WorkerControl: &opsv1alpha1.UpgradeWorkerControlStatus{ObservedWorkerConfigRevision: "sha256:worker"},
		WorkerDrain: &opsv1alpha1.UpgradeWorkerDrainStatus{
			ProtocolVersion:      opsv1alpha1.ManagedDrainProtocolPDBV1,
			ObservedSessionToken: drain.SessionToken, ObservedPolicyEpoch: 3, ObservedControlRevision: 8,
			ObservedWorkerConfigRevision: "sha256:worker", InventoryRevision: 9,
			InventoryObservedAt: now, InventoryComplete: true, ForeignDeviceWorkloadCount: 1, UpdatedAt: now,
		},
	}}
	if revision, ok := drainWorkerProvesPodClean(leaf, &target); !ok || revision != 9 {
		t.Fatalf("recovery inventory with one replacement = (%d, %v), want exact UID absence proof", revision, ok)
	}
	drain.State = opsv1alpha1.UpgradeManagerDrainEvicting
	if _, ok := drainWorkerProvesPodClean(leaf, &target); ok {
		t.Fatal("active drain accepted foreign/replacement workload inventory")
	}
}

func TestDrainWorkerProofRejectsStaleWorkerConfigUntilFreshRepublish(t *testing.T) {
	t.Parallel()
	now := metav1.NewTime(time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	pod := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: "apps", Name: "edge-a", UID: "pod-a",
		Phase: opsv1alpha1.UpgradeDrainPodTerminationObserved, DeletionObservedAt: &now,
	}
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{
		State:        opsv1alpha1.UpgradeManagerDrainRecovering,
		SessionToken: "11111111-1111-4111-8111-111111111111", PolicyEpoch: 3, ControlRevision: 8,
		Pods: []opsv1alpha1.UpgradeDrainPodStatus{pod},
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerDrain: drain,
		WorkerControl: &opsv1alpha1.UpgradeWorkerControlStatus{
			ObservedWorkerConfigRevision: "sha256:new-worker",
		},
		WorkerDrain: &opsv1alpha1.UpgradeWorkerDrainStatus{
			ProtocolVersion:      opsv1alpha1.ManagedDrainProtocolPDBV1,
			ObservedSessionToken: drain.SessionToken, ObservedPolicyEpoch: 3, ObservedControlRevision: 8,
			ObservedWorkerConfigRevision: "sha256:old-worker", InventoryRevision: 9,
			InventoryObservedAt: now, InventoryComplete: true, UpdatedAt: now,
		},
	}}
	if _, ok := drainWorkerProvesPodClean(leaf, &pod); ok {
		t.Fatal("manager accepted stale WorkerDrain evidence after worker configuration rotation")
	}
	leaf.Status.WorkerDrain.ObservedWorkerConfigRevision = "sha256:new-worker"
	leaf.Status.WorkerDrain.InventoryRevision = 0
	if _, ok := drainWorkerProvesPodClean(leaf, &pod); ok {
		t.Fatal("manager accepted matching worker configuration without a fresh positive inventory revision")
	}
	leaf.Status.WorkerDrain.InventoryRevision = 10
	leaf.Status.WorkerDrain.InventoryObservedAt = metav1.NewTime(now.Add(time.Second))
	leaf.Status.WorkerDrain.UpdatedAt = leaf.Status.WorkerDrain.InventoryObservedAt
	if revision, ok := drainWorkerProvesPodClean(leaf, &pod); !ok || revision != 10 {
		t.Fatalf("fresh republished WorkerDrain evidence = (%d, %v), want revision 10", revision, ok)
	}
	leaf.Annotations = map[string]string{
		managedprotocol.AnnotationNetworkWorkerPodUID:     "network-pod",
		managedprotocol.AnnotationAppWorkerPodUID:         "app-pod",
		managedprotocol.AnnotationAppWorkerConfigRevision: "sha256:new-worker",
	}
	leaf.Status.WorkerControl.ObservedWorkerConfigRevision = "sha256:distinct-network"
	leaf.Status.WorkerDrain.ObservedWorkerPodUID = "app-pod"
	if _, ok := drainWorkerProvesPodClean(leaf, &pod); !ok {
		t.Fatal("distinct app and network revisions prevented valid clean proof")
	}
	leaf.Annotations[managedprotocol.AnnotationAppWorkerPodUID] = "replacement-app-pod"
	if _, ok := drainWorkerProvesPodClean(leaf, &pod); ok {
		t.Fatal("same-config app restart reused the previous Pod's inventory")
	}
}

func TestDrainWorkerProofRequiresPerPodPostTerminationInventory(t *testing.T) {
	t.Parallel()
	now := metav1.NewTime(time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	podA := opsv1alpha1.UpgradeDrainPodStatus{
		UID: "pod-a", Phase: opsv1alpha1.UpgradeDrainPodTerminationObserved,
		DeletionObservedAt: &now, DeletionObservedInventoryRevision: 8,
	}
	podB := opsv1alpha1.UpgradeDrainPodStatus{
		UID: "pod-b", Phase: opsv1alpha1.UpgradeDrainPodTerminationObserved,
		DeletionObservedAt: &now, DeletionObservedInventoryRevision: 9,
	}
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{
		State: opsv1alpha1.UpgradeManagerDrainRecovering, SessionToken: "11111111-1111-4111-8111-111111111111",
		PolicyEpoch: 3, ControlRevision: 8, Pods: []opsv1alpha1.UpgradeDrainPodStatus{podA, podB},
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerDrain: drain,
		WorkerControl: &opsv1alpha1.UpgradeWorkerControlStatus{
			ObservedWorkerConfigRevision: "sha256:worker",
		},
		WorkerDrain: &opsv1alpha1.UpgradeWorkerDrainStatus{
			ProtocolVersion:      opsv1alpha1.ManagedDrainProtocolPDBV1,
			ObservedSessionToken: drain.SessionToken, ObservedPolicyEpoch: 3, ObservedControlRevision: 8,
			ObservedWorkerConfigRevision: "sha256:worker", InventoryRevision: 9,
			InventoryObservedAt: now, InventoryComplete: true, UpdatedAt: now,
		},
	}}
	if revision, ok := drainWorkerProvesPodClean(leaf, &podA); !ok || revision != 9 {
		t.Fatalf("Pod A post-termination proof = (%d, %v), want revision 9", revision, ok)
	}
	if _, ok := drainWorkerProvesPodClean(leaf, &podB); ok {
		t.Fatal("Pod B reused the same inventory revision captured at its termination boundary")
	}
	leaf.Status.WorkerDrain.InventoryRevision = 10
	leaf.Status.WorkerDrain.InventoryObservedAt = metav1.NewTime(now.Add(time.Second))
	leaf.Status.WorkerDrain.UpdatedAt = leaf.Status.WorkerDrain.InventoryObservedAt
	if revision, ok := drainWorkerProvesPodClean(leaf, &podB); !ok || revision != 10 {
		t.Fatalf("Pod B fresh post-termination proof = (%d, %v), want revision 10", revision, ok)
	}
}

func TestPatchDrainPodDeviceCleanUsesLatestWorkerEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	observedAt := metav1.NewTime(now)
	const token = "11111111-1111-4111-8111-111111111111"
	pod := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: "apps", Name: "edge-a", UID: "pod-a",
		Phase: opsv1alpha1.UpgradeDrainPodTerminationObserved, DeletionObservedAt: &observedAt,
		DeletionObservedInventoryRevision: 8,
	}
	current := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
			ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
				State: opsv1alpha1.UpgradeManagerDrainRecovering, SessionToken: token,
				PolicyEpoch: 3, ControlRevision: 8, Pods: []opsv1alpha1.UpgradeDrainPodStatus{pod},
			},
			WorkerControl: &opsv1alpha1.UpgradeWorkerControlStatus{ObservedWorkerConfigRevision: "sha256:worker"},
			WorkerDrain: &opsv1alpha1.UpgradeWorkerDrainStatus{
				ProtocolVersion:      opsv1alpha1.ManagedDrainProtocolPDBV1,
				ObservedSessionToken: token, ObservedPolicyEpoch: 3, ObservedControlRevision: 8,
				ObservedWorkerConfigRevision: "sha256:worker", InventoryRevision: 10,
				InventoryObservedAt: observedAt, InventoryComplete: false, UnknownDeviceWorkloadCount: 1,
				UpdatedAt: observedAt,
			},
		},
	}
	stale := current.DeepCopy()
	stale.Status.WorkerDrain.InventoryRevision = 9
	stale.Status.WorkerDrain.InventoryComplete = true
	stale.Status.WorkerDrain.UnknownDeviceWorkloadCount = 0
	kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(current).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.patchDrainPodDeviceClean(context.Background(), stale, pod.UID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(current), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved {
		t.Fatalf("stale clean proof advanced latest unclean status to %s", got.Status.ManagerDrain.Pods[0].Phase)
	}

	got.Status.WorkerDrain.InventoryRevision = 11
	got.Status.WorkerDrain.InventoryObservedAt = metav1.NewTime(now.Add(2 * time.Second))
	got.Status.WorkerDrain.UpdatedAt = got.Status.WorkerDrain.InventoryObservedAt
	got.Status.WorkerDrain.InventoryComplete = true
	got.Status.WorkerDrain.UnknownDeviceWorkloadCount = 0
	if err := kubeClient.Status().Update(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if err := r.patchDrainPodDeviceClean(context.Background(), stale, pod.UID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(current), &got); err != nil {
		t.Fatal(err)
	}
	progress := got.Status.ManagerDrain.Pods[0]
	if progress.Phase != opsv1alpha1.UpgradeDrainPodDeviceClean || progress.DeviceCleanInventoryRevision != 11 {
		t.Fatalf("DeviceClean progress = %#v, want latest clean revision 11", progress)
	}
}

func TestTerminationObservationCapturesFreshWorkerInventoryBaseline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	deletedAt := metav1.NewTime(now.Add(-time.Second))
	const token = "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid", DeletionTimestamp: &deletedAt,
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	frozen := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID), Phase: opsv1alpha1.UpgradeDrainPodEvictionRequested,
	}
	setDrainEligibilityHash(t, &frozen)
	current := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
			ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
				SessionToken: token, State: opsv1alpha1.UpgradeManagerDrainRecovering,
				Pods: []opsv1alpha1.UpgradeDrainPodStatus{frozen},
			},
			WorkerDrain: &opsv1alpha1.UpgradeWorkerDrainStatus{InventoryRevision: 8},
		},
	}
	stale := current.DeepCopy()
	stale.Status.WorkerDrain.InventoryRevision = 7
	kubeClient := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).WithObjects(pod, current).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	if err := r.reconcileOneDrainPod(context.Background(), drainTestRollout(), drainTestTarget(), stale,
		&stale.Status.ManagerDrain.Pods[0], now, true); err != nil {
		t.Fatal(err)
	}
	var got opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(current), &got); err != nil {
		t.Fatal(err)
	}
	progress := got.Status.ManagerDrain.Pods[0]
	if progress.Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved ||
		progress.DeletionObservedInventoryRevision != 8 {
		t.Fatalf("termination progress = %#v, want fresh inventory baseline 8", progress)
	}
}

func TestDrainWorkerCleanEvidenceWaitsForCanonicalLeaseRelease(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	observedAt := metav1.NewTime(now)
	startedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	deletedAt := metav1.NewTime(now.Add(-time.Second))
	token := "11111111-1111-4111-8111-111111111111"
	rollout := drainTestRollout()
	rollout.Namespace = "fleet"
	target := drainTestTarget()
	workerUsername := "system:serviceaccount:fleet:cvk-worker"

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "apps", Name: "edge-0", UID: "pod-uid", DeletionTimestamp: &deletedAt,
		Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
		Finalizers:  []string{managedprotocol.DrainPodFinalizer},
	}}
	frozen := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
		Phase: opsv1alpha1.UpgradeDrainPodTerminationObserved, DeletionObservedAt: &deletedAt,
	}
	setDrainEligibilityHash(t, &frozen)
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: rollout.Namespace, Name: target.ChildName, UID: "leaf-uid"},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
			ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
				ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
				State:           opsv1alpha1.UpgradeManagerDrainRecovering, SessionToken: token,
				PolicyEpoch: 3, ControlRevision: 8, NodeUID: target.NodeUID,
				StartedAt: startedAt,
				Pods:      []opsv1alpha1.UpgradeDrainPodStatus{frozen},
			},
			WorkerControl: &opsv1alpha1.UpgradeWorkerControlStatus{ObservedWorkerConfigRevision: "sha256:worker"},
			WorkerDrain: &opsv1alpha1.UpgradeWorkerDrainStatus{
				ProtocolVersion:      opsv1alpha1.ManagedDrainProtocolPDBV1,
				ObservedSessionToken: token, ObservedPolicyEpoch: 3, ObservedControlRevision: 8,
				ObservedWorkerConfigRevision: "sha256:worker", InventoryRevision: 9,
				InventoryObservedAt: observedAt, InventoryComplete: true, UpdatedAt: observedAt,
			},
		},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: target.NodeName, UID: types.UID(target.NodeUID),
		Annotations: map[string]string{managedprotocol.AnnotationWorkerUsername: workerUsername},
	}}
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: rollout.Namespace, Name: target.DeviceName, UID: types.UID(target.DeviceUID)},
		Status: ciskov1.DeviceStatus{MaintenanceSession: &ciskov1.DeviceMaintenanceSessionStatus{
			ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			Phase:           ciskov1.DeviceMaintenanceSessionRecovering,
			Purpose:         ciskov1.DeviceMaintenancePurposeWorkloadDrain, SessionToken: token,
			Lease: ciskov1.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: rollout.Namespace, Name: "mutation", UID: "lease-uid",
			}, Holder: "software-drain/leaf-uid"},
			Operation: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
			},
			DeviceUID: target.DeviceUID, NodeName: target.NodeName, NodeUID: target.NodeUID,
			RequestedAt: startedAt, AcknowledgedAt: &observedAt, ControlRevision: 8,
		}},
	}
	annotations, labels := managedMutationLeaseMetadata(device, target.NodeName, target.NodeUID, workerUsername)
	annotations[managedprotocol.AnnotationMaintenanceRequestVersion] = managedprotocol.Version
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Namespace, Name: "mutation", UID: "lease-uid", Annotations: annotations, Labels: labels,
	}, Spec: coordv1.LeaseSpec{
		HolderIdentity: ptr.To("pod-delete/leaf-uid"), LeaseDurationSeconds: ptr.To[int32](60),
		AcquireTime: ptr.To(metav1.NewMicroTime(now.Add(-time.Second))),
		RenewTime:   ptr.To(metav1.NewMicroTime(now)), LeaseTransitions: ptr.To[int32](1),
	}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(pod, leaf, node, device, lease).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}

	err := r.reconcileOneDrainPod(context.Background(), rollout, target, leaf,
		&leaf.Status.ManagerDrain.Pods[0], now, true)
	if !errors.Is(err, errDrainSafetyBlocked) {
		t.Fatalf("held canonical Lease error = %v, want errDrainSafetyBlocked", err)
	}
	var currentLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	if currentLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved {
		t.Fatalf("held Lease advanced Pod to %s", currentLeaf.Status.ManagerDrain.Pods[0].Phase)
	}
	var currentPod corev1.Pod
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &currentPod); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&currentPod, managedprotocol.DrainPodFinalizer) {
		t.Fatal("held Lease allowed manager finalizer to be removed")
	}

	var currentLease coordv1.Lease
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(lease), &currentLease); err != nil {
		t.Fatal(err)
	}
	currentLease.Annotations, currentLease.Labels = managedMutationLeaseMetadata(
		device, target.NodeName, target.NodeUID, workerUsername,
	)
	currentLease.Spec = coordv1.LeaseSpec{LeaseTransitions: ptr.To[int32](1)}
	if err := kubeClient.Update(context.Background(), &currentLease); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileOneDrainPod(context.Background(), rollout, target, &currentLeaf,
		&currentLeaf.Status.ManagerDrain.Pods[0], now.Add(time.Second), true); err != nil {
		t.Fatalf("idle canonical Lease did not accept WorkerDrain evidence: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	if currentLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodDeviceClean ||
		currentLeaf.Status.ManagerDrain.Pods[0].DeviceCleanInventoryRevision != 9 {
		t.Fatalf("idle Lease Pod progress = %#v, want DeviceClean at inventory revision 9",
			currentLeaf.Status.ManagerDrain.Pods[0])
	}

	// DeviceClean is not permission to drop protection while an in-flight or
	// retained exact mutation Lease still exists.
	holder := device.Status.MaintenanceSession.Lease.Holder
	currentLease.Spec = coordv1.LeaseSpec{
		HolderIdentity:       &holder,
		LeaseDurationSeconds: ptr.To[int32](60),
		AcquireTime:          ptr.To(metav1.NewMicroTime(now)),
		RenewTime:            ptr.To(metav1.NewMicroTime(now)),
		LeaseTransitions:     ptr.To[int32](2),
	}
	if err := kubeClient.Update(context.Background(), &currentLease); err != nil {
		t.Fatal(err)
	}
	err = r.reconcileOneDrainPod(context.Background(), rollout, target, &currentLeaf,
		&currentLeaf.Status.ManagerDrain.Pods[0], now.Add(time.Second), true)
	if !errors.Is(err, errDrainSafetyBlocked) {
		t.Fatalf("DeviceClean held Lease error = %v, want errDrainSafetyBlocked", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &currentPod); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&currentPod, managedprotocol.DrainPodFinalizer) {
		t.Fatal("DeviceClean held Lease allowed manager finalizer removal")
	}
	currentLease.Spec = coordv1.LeaseSpec{LeaseTransitions: ptr.To[int32](2)}
	if err := kubeClient.Update(context.Background(), &currentLease); err != nil {
		t.Fatal(err)
	}

	// A newer worker publication can supersede the revision that originally
	// established DeviceClean. Reconciliation from a stale clean snapshot must
	// re-read it and retain the finalizer while the latest evidence is unproven.
	staleDeviceClean := currentLeaf.DeepCopy()
	currentLeaf.Status.WorkerDrain.InventoryRevision = 10
	currentLeaf.Status.WorkerDrain.InventoryObservedAt = metav1.NewTime(now.Add(2 * time.Second))
	currentLeaf.Status.WorkerDrain.UpdatedAt = currentLeaf.Status.WorkerDrain.InventoryObservedAt
	currentLeaf.Status.WorkerDrain.InventoryComplete = false
	currentLeaf.Status.WorkerDrain.UnknownDeviceWorkloadCount = 1
	if err := kubeClient.Status().Update(context.Background(), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileOneDrainPod(context.Background(), rollout, target, staleDeviceClean,
		&staleDeviceClean.Status.ManagerDrain.Pods[0], now.Add(2*time.Second), true); err != nil {
		t.Fatalf("superseding unclean evidence returned unexpected error: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &currentPod); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&currentPod, managedprotocol.DrainPodFinalizer) {
		t.Fatal("stale DeviceClean snapshot removed the manager finalizer over newer unclean evidence")
	}

	// Crash/retry resumes from durable DeviceClean. Once a newer complete proof
	// is present and the exact Lease remains idle, finalizer release and the
	// Released acknowledgement are safe and idempotent.
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	currentLeaf.Status.WorkerDrain.InventoryRevision = 11
	currentLeaf.Status.WorkerDrain.InventoryObservedAt = metav1.NewTime(now.Add(3 * time.Second))
	currentLeaf.Status.WorkerDrain.UpdatedAt = currentLeaf.Status.WorkerDrain.InventoryObservedAt
	currentLeaf.Status.WorkerDrain.InventoryComplete = true
	currentLeaf.Status.WorkerDrain.UnknownDeviceWorkloadCount = 0
	if err := kubeClient.Status().Update(context.Background(), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileOneDrainPod(context.Background(), rollout, target, &currentLeaf,
		&currentLeaf.Status.ManagerDrain.Pods[0], now.Add(3*time.Second), true); err != nil {
		t.Fatalf("fresh final proof did not resume DeviceClean: %v", err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &currentLeaf); err != nil {
		t.Fatal(err)
	}
	if currentLeaf.Status.ManagerDrain.Pods[0].Phase != opsv1alpha1.UpgradeDrainPodReleased {
		t.Fatalf("fresh final proof left Pod in phase %s", currentLeaf.Status.ManagerDrain.Pods[0].Phase)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(pod), &currentPod); err == nil &&
		controllerutil.ContainsFinalizer(&currentPod, managedprotocol.DrainPodFinalizer) {
		t.Fatal("fresh final proof retained the manager finalizer")
	} else if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestApplyManagerDrainFenceRetainsGuardForUnresolvedClaim(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name             string
		cancel           bool
		recoveryRequired bool
	}{
		{name: "cancellation", cancel: true},
		{name: "source fence", recoveryRequired: true},
		{name: "policy fence", recoveryRequired: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
				Phase: opsv1alpha1.UpgradePhaseActivating,
				ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
					State: opsv1alpha1.UpgradeManagerDrainPromoted, ControlRevision: 7, UpdatedAt: metav1.NewTime(now),
				},
				ManagedMutationClaims: []opsv1alpha1.UpgradeManagedMutationClaimStatus{{}},
			}}
			applyManagerDrainFence(leaf, false, test.cancel, test.recoveryRequired, 8, now.Add(time.Second))
			if leaf.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainPromoted ||
				leaf.Status.ManagerDrain.ControlRevision != 8 {
				t.Fatalf("unresolved claimed drain = %#v, want Promoted guard retained at revision 8", leaf.Status.ManagerDrain)
			}
		})
	}

	completed := metav1.NewTime(now.Add(time.Minute))
	resolved := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		Phase: opsv1alpha1.UpgradePhaseSucceeded, CompletionTime: &completed,
		Conditions: []metav1.Condition{{Type: "DeviceMutationSettled", Status: metav1.ConditionTrue}},
		ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			State: opsv1alpha1.UpgradeManagerDrainPromoted, ControlRevision: 7, UpdatedAt: metav1.NewTime(now),
			StartedAt: metav1.NewTime(now), DrainDeadline: metav1.NewTime(now.Add(time.Minute)),
		},
		ManagedMutationClaims: []opsv1alpha1.UpgradeManagedMutationClaimStatus{{}},
	}}
	applyManagerDrainFence(resolved, false, true, false, 8, now.Add(2*time.Minute))
	if resolved.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainRecovering {
		t.Fatalf("resolved claimed drain state = %s, want Recovering", resolved.Status.ManagerDrain.State)
	}

	zeroClaim := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			State: opsv1alpha1.UpgradeManagerDrainPromoted, ControlRevision: 7, UpdatedAt: metav1.NewTime(now),
			StartedAt: metav1.NewTime(now), DrainDeadline: metav1.NewTime(now.Add(time.Minute)),
		},
	}}
	applyManagerDrainFence(zeroClaim, false, false, true, 7, now.Add(time.Second))
	if zeroClaim.Status.ManagerDrain.State != opsv1alpha1.UpgradeManagerDrainRecovering {
		t.Fatalf("zero-claim fenced drain state = %s, want Recovering", zeroClaim.Status.ManagerDrain.State)
	}
}

func TestExactDrainPodProtectionRejectsPartialOrForeignState(t *testing.T) {
	t.Parallel()
	const token = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name       string
		annotation string
		finalizer  bool
		wantOwned  bool
		wantError  bool
	}{
		{name: "absent"},
		{name: "exact", annotation: token, finalizer: true, wantOwned: true},
		{name: "marker only", annotation: token, wantError: true},
		{name: "finalizer only", finalizer: true, wantError: true},
		{name: "foreign", annotation: "22222222-2222-4222-8222-222222222222", finalizer: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pod := &corev1.Pod{}
			if test.annotation != "" {
				pod.Annotations = map[string]string{managedprotocol.AnnotationDrainSession: test.annotation}
			}
			if test.finalizer {
				pod.Finalizers = []string{managedprotocol.DrainPodFinalizer}
			}
			owned, err := exactDrainPodProtection(pod, token)
			if owned != test.wantOwned || (err != nil) != test.wantError {
				t.Fatalf("exactDrainPodProtection() = (%v, %v), want owned=%v error=%v",
					owned, err, test.wantOwned, test.wantError)
			}
		})
	}
}

func TestDrainNodeGuardMatchesRequiresExactSessionOwnership(t *testing.T) {
	t.Parallel()
	const token = "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name                   string
		unschedulable          bool
		maintenanceTaint       bool
		unschedulableBefore    bool
		maintenanceTaintBefore bool
		cordonOwner            string
		taintOwner             string
		want                   bool
	}{
		{
			name: "manager owns both newly applied guards", unschedulable: true, maintenanceTaint: true,
			cordonOwner: token, taintOwner: token, want: true,
		},
		{
			name: "preexisting guards remain operator owned", unschedulable: true, maintenanceTaint: true,
			unschedulableBefore: true, maintenanceTaintBefore: true, want: true,
		},
		{
			name: "preexisting cordon cannot gain manager owner", unschedulable: true, maintenanceTaint: true,
			unschedulableBefore: true, maintenanceTaintBefore: true, cordonOwner: token,
		},
		{
			name: "preexisting taint cannot gain manager owner", unschedulable: true, maintenanceTaint: true,
			unschedulableBefore: true, maintenanceTaintBefore: true, taintOwner: token,
		},
		{
			name: "new cordon requires exact session owner", unschedulable: true, maintenanceTaint: true,
			cordonOwner: "22222222-2222-4222-8222-222222222222", taintOwner: token,
		},
		{
			name: "new taint requires exact session owner", unschedulable: true, maintenanceTaint: true,
			cordonOwner: token, taintOwner: "22222222-2222-4222-8222-222222222222",
		},
		{name: "missing cordon", maintenanceTaint: true, cordonOwner: token, taintOwner: token},
		{name: "missing maintenance taint", unschedulable: true, cordonOwner: token, taintOwner: token},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				managedprotocol.AnnotationDrainCordonOwner: test.cordonOwner,
				managedprotocol.AnnotationDrainTaintOwner:  test.taintOwner,
			}}, Spec: corev1.NodeSpec{Unschedulable: test.unschedulable}}
			if test.maintenanceTaint {
				node.Spec.Taints = append(node.Spec.Taints, maintenanceGuardTaint())
			}
			drain := &opsv1alpha1.UpgradeManagerDrainStatus{
				SessionToken: token, NodeUnschedulableBefore: test.unschedulableBefore,
				MaintenanceTaintPresentBefore: test.maintenanceTaintBefore,
			}
			if got := drainNodeGuardMatches(node, drain); got != test.want {
				t.Fatalf("drainNodeGuardMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEnsureDrainGuardRestoredAcceptsExplicitOperatorHold(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	now := metav1.NewTime(time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	token := "11111111-1111-4111-8111-111111111111"
	target := opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		DeviceName: "switch-a", DeviceUID: "device-uid", NodeName: "switch-a", NodeUID: "node-uid",
	}
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1, State: opsv1alpha1.UpgradeManagerDrainRecovering,
		SessionToken: token, ControlRevision: 8, NodeUID: target.NodeUID, StartedAt: now,
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{
		Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid",
	}, Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{ManagerDrain: drain}}
	acknowledged := metav1.NewTime(now.Add(time.Second))
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: target.DeviceName, UID: types.UID(target.DeviceUID),
			Annotations: map[string]string{managedprotocol.AnnotationDrainCordonHold: "true"}},
		Status: ciskov1.DeviceStatus{MaintenanceSession: &ciskov1.DeviceMaintenanceSessionStatus{
			Phase: ciskov1.DeviceMaintenanceSessionRecovering, ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
			Purpose: ciskov1.DeviceMaintenancePurposeWorkloadDrain, SessionToken: token,
			Lease: ciskov1.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: "fleet", Name: "mutation", UID: "lease-uid"}, Holder: "software-drain/leaf-uid"},
			Operation: ciskov1.DeviceMaintenanceObjectReference{Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID)},
			DeviceUID: target.DeviceUID, NodeName: target.NodeName, NodeUID: target.NodeUID,
			RequestedAt: now, AcknowledgedAt: &acknowledged, ControlRevision: drain.ControlRevision,
		}},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: target.NodeName, UID: types.UID(target.NodeUID)},
		Spec: corev1.NodeSpec{Unschedulable: true}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node, device).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	rollout := drainTestRollout()
	rollout.Namespace = "fleet"
	if err := r.ensureDrainGuardRestored(context.Background(), rollout, target, leaf); err != nil {
		t.Fatalf("ensureDrainGuardRestored(operator hold) = %v", err)
	}
}

func TestDrainRecoveryRevalidatesHealthyCurrentControllerAndPDBGeneration(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	replicas := int32(2)
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "rs-uid", Generation: 4},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true", "app": "edge"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "image"}}},
			},
		},
		Status: appsv1.ReplicaSetStatus{ObservedGeneration: 4, Replicas: 2, AvailableReplicas: 2, ReadyReplicas: 2},
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "pdb-uid", Generation: 5},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}},
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			ObservedGeneration: 5, CurrentHealthy: 2, DesiredHealthy: 2, ExpectedPods: 2,
		},
	}
	pod := &opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: "apps", Name: "edge-old", UID: "pod-old",
		Controller: opsv1alpha1.UpgradeDrainObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Namespace: "apps",
			Name: replicaSet.Name, UID: string(replicaSet.UID), Generation: 1,
		},
		PDBs: []opsv1alpha1.UpgradeDrainPDBStatus{{UpgradeDrainObjectReference: opsv1alpha1.UpgradeDrainObjectReference{
			APIVersion: policyv1.SchemeGroupVersion.String(), Kind: "PodDisruptionBudget", Namespace: "apps",
			Name: pdb.Name, UID: string(pdb.UID), Generation: 1,
		}}},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(replicaSet, pdb).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	ready, err := r.drainControllerReady(context.Background(), pod)
	if err != nil || !ready {
		t.Fatalf("current-generation ReplicaSet readiness = (%v, %v), want ready", ready, err)
	}
	if err := r.validateRecoveredPDBs(context.Background(), pod); err != nil {
		t.Fatalf("current-generation PDB recovery was rejected: %v", err)
	}

	replicaSet.Spec.Template.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "switch-a"}
	if err := kubeClient.Update(context.Background(), replicaSet); err != nil {
		t.Fatal(err)
	}
	if ready, err := r.drainControllerReady(context.Background(), pod); err == nil || ready {
		t.Fatalf("hard-pinned current template = (%v, %v), want recovery block", ready, err)
	}
}

func TestDrainControllerReadyRejectsDeploymentSurgeMaskingUnavailableReplacements(t *testing.T) {
	t.Parallel()
	scheme := drainTestScheme(t)
	replicas := int32(2)
	pod := &opsv1alpha1.UpgradeDrainPodStatus{
		Controller: opsv1alpha1.UpgradeDrainObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment", Namespace: "apps",
			Name: "edge", UID: "deployment-uid", Generation: 4,
		},
	}
	tests := []struct {
		name   string
		status appsv1.DeploymentStatus
		ready  bool
	}{
		{
			name: "old available replicas mask unavailable replacements during surge",
			status: appsv1.DeploymentStatus{
				ObservedGeneration: 4, Replicas: 4, UpdatedReplicas: 2,
				ReadyReplicas: 2, AvailableReplicas: 2,
			},
		},
		{
			name: "only current ready and available replicas remain",
			status: appsv1.DeploymentStatus{
				ObservedGeneration: 4, Replicas: 2, UpdatedReplicas: 2,
				ReadyReplicas: 2, AvailableReplicas: 2,
			},
			ready: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "deployment-uid", Generation: 4},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true", "app": "edge"}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "image"}}},
					},
				},
				Status: tt.status,
			}
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deployment).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
			ready, err := r.drainControllerReady(context.Background(), pod)
			if err != nil {
				t.Fatalf("drainControllerReady() error = %v", err)
			}
			if ready != tt.ready {
				t.Fatalf("drainControllerReady() = %v, want %v", ready, tt.ready)
			}
		})
	}
}

func TestPersistDrainRecoverySoakSurvivesFailureAndReplanRetries(t *testing.T) {
	t.Parallel()
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Status.Targets = []opsv1alpha1.IOSXESoftwareRolloutTargetStatus{{
		DeviceName: target.DeviceName, DeviceUID: target.DeviceUID, LeafName: target.ChildName,
		Phase: opsv1alpha1.IOSXESoftwareRolloutTargetBlocked, Reason: "DrainRecovering",
	}}
	leaf := policyFenceLeaf(rollout, target, "leaf-uid")
	scheme := drainTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}).
		WithObjects(rollout).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	started := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	if err := r.persistDrainRecoveryGate(context.Background(), rollout, target, leaf,
		"HealthyPostMutationSoak", "healthy", started); err != nil {
		t.Fatal(err)
	}
	var current opsv1alpha1.IOSXESoftwareRollout
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	summary := indexTargetSummaries(current.Status.Targets)[target.DeviceUID]
	if summary.Phase != opsv1alpha1.IOSXESoftwareRolloutTargetSoaking ||
		summary.Reason != "HealthyPostMutationSoak" || !summary.LastTransitionTime.Time.Equal(started) {
		t.Fatalf("persisted fence-path soak = %#v", summary)
	}

	device := &ciskov1.CiscoDevice{Status: ciskov1.DeviceStatus{Phase: "Ready"}}
	node := nodeWithConditions(corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		LastHeartbeatTime: metav1.NewTime(started), LastTransitionTime: metav1.NewTime(started.Add(-time.Minute)),
	})
	if err := refreshManagedHealthObservation(device, node, started); err != nil {
		t.Fatal(err)
	}
	rollout = current.DeepCopy()
	for _, advance := range []time.Duration{30 * time.Second, 61 * time.Second, 2 * time.Minute} {
		node.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(started.Add(advance))
		observed, err := managedDeviceHealthObservedAt(device, node, started.Add(advance))
		if err != nil || !observed.Equal(started) {
			t.Fatalf("heartbeat-only skew at %s = %s, %v; want authenticated %s", advance, observed, err, started)
		}
		if err := r.persistDrainRecoveryGate(context.Background(), rollout, target, leaf,
			"HealthyPostMutationSoak", "still healthy", started.Add(advance)); err != nil {
			t.Fatal(err)
		}
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	summary = indexTargetSummaries(current.Status.Targets)[target.DeviceUID]
	if !summary.LastTransitionTime.Time.Equal(started) {
		t.Fatalf("heartbeat-only skew moved persisted soak start to %s, want %s", summary.LastTransitionTime, started)
	}
	deadline, ok := continuousHealthySoakDeadline(
		summary, started.Add(-time.Minute), 5*time.Minute,
	)
	if !ok || !deadline.Equal(started.Add(5*time.Minute)) {
		t.Fatalf("retry soak deadline = (%s, %v), want %s", deadline, ok, started.Add(5*time.Minute))
	}

	node.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(started.Add(3 * time.Minute))
	node.Status.Conditions[0].LastTransitionTime = metav1.NewTime(started.Add(3 * time.Minute))
	_, healthErr := managedDeviceHealthObservedAt(device, node, started.Add(3*time.Minute))
	if healthErr == nil || !strings.Contains(healthErr.Error(), "transitioned after") {
		t.Fatalf("real Ready transition health error = %v, want post-snapshot transition rejection", healthErr)
	}
	rollout = current.DeepCopy()
	if err := r.persistDrainRecoveryGate(context.Background(), rollout, target, leaf,
		"PostMutationHealthGate", healthErr.Error(), started.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	summary = indexTargetSummaries(current.Status.Targets)[target.DeviceUID]
	if summary.Phase != opsv1alpha1.IOSXESoftwareRolloutTargetBlocked ||
		summary.Reason != "PostMutationHealthGate" || !summary.LastTransitionTime.Time.Equal(started.Add(3*time.Minute)) {
		t.Fatalf("unhealthy interval did not reset persisted soak: %#v", summary)
	}

	if err := refreshManagedHealthObservation(device, node, started.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := managedDeviceHealthObservedAt(device, node, started.Add(4*time.Minute)); err != nil {
		t.Fatalf("refreshed Ready transition observation rejected: %v", err)
	}
	rollout = current.DeepCopy()
	if err := r.persistDrainRecoveryGate(context.Background(), rollout, target, leaf,
		"HealthyPostMutationSoak", "healthy again", started.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	deadline, ok = continuousHealthySoakDeadline(
		indexTargetSummaries(rollout.Status.Targets)[target.DeviceUID], started.Add(-time.Minute), 5*time.Minute,
	)
	if !ok || !deadline.Equal(started.Add(9*time.Minute)) {
		t.Fatalf("restarted soak deadline = (%s, %v), want %s", deadline, ok, started.Add(9*time.Minute))
	}
}

func TestValidateDrainSessionIdentityRejectsMismatchedSettlement(t *testing.T) {
	t.Parallel()
	now := metav1.NewTime(time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC))
	acknowledged := metav1.NewTime(now.Add(time.Second))
	target := opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		DeviceUID: "device-uid", NodeName: "switch-a", NodeUID: "node-uid",
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{Namespace: "fleet", Name: "upgrade-a", UID: "leaf-uid"}}
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{SessionToken: "11111111-1111-4111-8111-111111111111", StartedAt: now, ControlRevision: 8}
	session := &ciskov1.DeviceMaintenanceSessionStatus{
		Phase: ciskov1.DeviceMaintenanceSessionSettled, ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
		Purpose: ciskov1.DeviceMaintenancePurposeSoftwareMutation, SessionToken: drain.SessionToken,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: "fleet", Name: "mutation", UID: "lease-uid"}, Holder: "software-upgrade/leaf-uid"},
		Operation: ciskov1.DeviceMaintenanceObjectReference{Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID)},
		DeviceUID: target.DeviceUID, NodeName: target.NodeName, NodeUID: target.NodeUID,
		RequestedAt: now, AcknowledgedAt: &acknowledged, ControlRevision: drain.ControlRevision,
	}
	if err := validateDrainSessionIdentity(session, target, leaf, drain); err != nil {
		t.Fatalf("valid settled session rejected: %v", err)
	}
	changed := session.DeepCopy()
	changed.Lease.Holder = "software-drain/leaf-uid"
	if err := validateDrainSessionIdentity(changed, target, leaf, drain); err == nil {
		t.Fatal("settled software-mutation session with a drain Lease holder was accepted")
	}
}

func TestFrozenDrainRecoveryHealthUsesOnlyExactApprovedTarget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Plan.Health.MaxObservationAgeSeconds = 300
	projectionTime := metav1.NewTime(now.Add(-time.Minute))
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace, Name: target.DeviceName, UID: types.UID(target.DeviceUID), Generation: target.DeviceGeneration,
			Labels: map[string]string{
				"topology.cisco.vk/site":                 "site-a",
				managedprotocol.ImageFamilyLabel:         target.ImageFamily,
				managedprotocol.QualificationCohortLabel: target.QualificationCohort,
			},
		},
		Spec: ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, PhysicalIdentity: "SERIAL-DEVICE-A"},
		Status: ciskov1.DeviceStatus{
			Phase: "Ready",
			Conditions: []metav1.Condition{
				{Type: ciskov1.CiscoDeviceConditionNodeIdentityReady, Status: metav1.ConditionTrue, ObservedGeneration: target.DeviceGeneration},
				{Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionTrue, ObservedGeneration: target.DeviceGeneration},
				{Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue, ObservedGeneration: target.DeviceGeneration},
			},
			NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
				NodeName: target.NodeName, NodeUID: target.NodeUID, DeviceUID: target.DeviceUID, PhysicalIdentity: target.PhysicalIdentity,
			},
			TopologyProjection: &ciskov1.DeviceTopologyProjectionStatus{
				EffectiveLabelHash: target.ProjectionHash, LastSuccessfulTime: projectionTime,
			},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: target.NodeName, UID: types.UID(target.NodeUID),
			Labels: map[string]string{"topology.cisco.vk/site": "site-a"},
			Annotations: map[string]string{
				managedprotocol.AnnotationManaged:         "true",
				managedprotocol.AnnotationDeviceNamespace: device.Namespace,
				managedprotocol.AnnotationDeviceName:      device.Name,
				managedprotocol.AnnotationDeviceUID:       string(device.UID),
				managedprotocol.AnnotationNodeUID:         target.NodeUID,
				managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
				managedprotocol.AnnotationWorkerUsername:  "system:serviceaccount:lab:cvk-device-a",
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{MachineID: target.PhysicalIdentity, SystemUUID: target.PhysicalIdentity},
			Conditions: []corev1.NodeCondition{{
				Type: corev1.NodeReady, Status: corev1.ConditionTrue,
				LastHeartbeatTime: metav1.NewTime(now), LastTransitionTime: metav1.NewTime(now.Add(-time.Minute)),
			}},
		},
	}
	workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
	if err := refreshManagedHealthObservation(device, node, now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err != nil {
		t.Fatal(err)
	}
	objects := append([]client.Object{device, node}, workerObjects...)
	scheme := drainTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient, Now: func() time.Time { return now }}
	healthy, detail, err := r.targetFrozenDrainRecoveryHealthy(
		context.Background(), rollout, target, now.Add(-2*time.Minute), now,
	)
	if err != nil || !healthy {
		t.Fatalf("targetFrozenDrainRecoveryHealthy() = (%v, %q, %v), want healthy", healthy, detail, err)
	}

	other := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Namespace, Name: "duplicate", UID: "other-device",
	}, Spec: ciskov1.DeviceSpec{PhysicalIdentity: "SERIAL-DEVICE-A"}}
	if err := kubeClient.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	healthy, detail, err = r.targetFrozenDrainRecoveryHealthy(
		context.Background(), rollout, target, now.Add(-2*time.Minute), now,
	)
	if err != nil || healthy || detail != "frozen physical identity is now enrolled by another CiscoDevice" {
		t.Fatalf("duplicate physical identity result = (%v, %q, %v)", healthy, detail, err)
	}
}

func TestAllowFrozenDrainRecoveryHealthCoversSameUIDIncompatiblePolicyEdit(t *testing.T) {
	t.Parallel()
	rollout := drainTestRollout()
	rollout.Status.FrozenPlan = &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
		Policy: opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{UID: "policy-uid", ResourceVersion: "7"},
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{State: opsv1alpha1.UpgradeManagerDrainRecovering},
		ManagerAdmission: &opsv1alpha1.UpgradeManagerAdmissionStatus{
			State: opsv1alpha1.UpgradeManagerAdmissionRevoked, RevocationReason: "AdministratorPolicyChanged",
		},
	}}
	for _, policy := range []*topologyrollout.ParsedAdminPolicy{
		{PolicyUID: "policy-uid", ResourceVersion: "8"},
		{PolicyUID: "replacement-policy-uid", ResourceVersion: "1"},
	} {
		if !allowFrozenDrainRecoveryHealth(rollout, policy, leaf) {
			t.Fatalf("live incompatible policy %s/%s did not permit recovery-only frozen health proof",
				policy.PolicyUID, policy.ResourceVersion)
		}
	}
	leaf.Status.ManagerAdmission.RevocationReason = "SourceIdentityChanged"
	if allowFrozenDrainRecoveryHealth(rollout,
		&topologyrollout.ParsedAdminPolicy{PolicyUID: "policy-uid", ResourceVersion: "8"}, leaf) {
		t.Fatal("non-policy recovery fence used frozen administrator-policy health fallback")
	}
}

func TestAllowFrozenDrainRecoveryHealthCoversExactPolicyEpochTransition(t *testing.T) {
	t.Parallel()
	rollout, currentPolicy := rolloutPolicyFixture()
	next, err := freezePolicy(currentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	previous := next
	previous.ResourceVersion = "10"
	previous.SemanticHash = "sha256:previous-policy-semantics"
	rollout.Status.FrozenPlan.Policy = previous
	rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
		Epoch: 1, Policy: previous,
	}
	rollout.Status.PolicyTransition = &opsv1alpha1.IOSXESoftwareRolloutPolicyTransitionStatus{
		Epoch: 2, Policy: next,
	}
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerDrain: &opsv1alpha1.UpgradeManagerDrainStatus{
			State: opsv1alpha1.UpgradeManagerDrainRecovering, PolicyEpoch: 1,
		},
		ManagerAdmission: &opsv1alpha1.UpgradeManagerAdmissionStatus{
			State: opsv1alpha1.UpgradeManagerAdmissionRevoked, PolicyEpoch: 1,
			RevocationReason: "PolicyEpochTransition",
		},
	}}
	if !allowFrozenDrainRecoveryHealth(rollout, currentPolicy, leaf) {
		t.Fatal("exact persisted policy transition did not permit recovery-only frozen health proof")
	}

	currentPolicy.ResourceVersion = "12"
	if allowFrozenDrainRecoveryHealth(rollout, currentPolicy, leaf) {
		t.Fatal("policy transition fallback accepted a current policy outside the persisted transition")
	}
	currentPolicy.ResourceVersion = next.ResourceVersion
	rollout.Status.PolicyTransition.Epoch = 3
	if allowFrozenDrainRecoveryHealth(rollout, currentPolicy, leaf) {
		t.Fatal("policy transition fallback accepted a skipped effective epoch")
	}
}

func drainTestRollout() *opsv1alpha1.IOSXESoftwareRollout {
	return &opsv1alpha1.IOSXESoftwareRollout{Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{Plan: opsv1alpha1.IOSXESoftwareRolloutPlan{
		Workloads: opsv1alpha1.IOSXESoftwareRolloutWorkloadSpec{
			Policy: opsv1alpha1.IOSXESoftwareRolloutWorkloadDrain,
			Drain: &opsv1alpha1.IOSXESoftwareRolloutDrainSpec{
				Namespaces: []string{"apps"}, TimeoutSeconds: 600, MaxPods: 4, MaxTerminationGraceSeconds: 60,
			},
		},
	}}}
}

func drainTestTarget() opsv1alpha1.IOSXESoftwareRolloutPlannedTarget {
	return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		DeviceName: "switch-a", DeviceUID: "device-uid", NodeName: "switch-a", NodeUID: "node-uid",
		ChildName: "upgrade-a",
	}
}

func drainEvictionAuthorityFixture(
	t *testing.T,
	now time.Time,
) (*opsv1alpha1.IOSXESoftwareRollout, opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	*opsv1alpha1.IOSXESoftwareUpgrade, []client.Object) {
	t.Helper()
	target := policyFenceTarget("switch-a", "device-uid", "upgrade-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Plan.Workloads = opsv1alpha1.IOSXESoftwareRolloutWorkloadSpec{
		Policy: opsv1alpha1.IOSXESoftwareRolloutWorkloadDrain,
		Drain: &opsv1alpha1.IOSXESoftwareRolloutDrainSpec{
			Namespaces: []string{"apps"}, TimeoutSeconds: 600, MaxPods: 4, MaxTerminationGraceSeconds: 60,
		},
	}
	const siteKey = "topology.cisco.vk/site"
	policyConfig := topologyrollout.AdminPolicyConfig{
		AppHostingServiceAccountName:        managedprotocol.AppHostingServiceAccount,
		NetworkManagementServiceAccountName: managedprotocol.NetworkManagementServiceAccount,
		Version:                             topologyrollout.PolicyVersion,
		FleetSelector:                       metav1.LabelSelector{MatchLabels: map[string]string{managedprotocol.AnnotationManaged: "true"}},
		RequiredTopologyKeys:                []string{siteKey},
		ProjectedTopologyKeys:               []string{siteKey},
		GlobalMaxConcurrentTransfers:        1,
		GlobalMaxUnavailable:                1,
		DomainMaxConcurrentTransfers:        map[string]int{siteKey: 1},
		DomainMaxUnavailable:                map[string]int{siteKey: 1},
		HealthFreshnessSeconds:              300,
		MaxCampaignTargets:                  100,
		MaxActiveReservations:               256,
		MaxLedgerBytes:                      256 * 1024,
		WorkloadDrain: &topologyrollout.AdminWorkloadDrainPolicy{
			Enabled: true, AllowedNamespaces: []string{"apps"}, MaxTimeoutSeconds: 600,
			MaxPods: 4, MaxTerminationGraceSeconds: 60,
		},
		LedgerName: rollout.Status.FrozenPlan.Policy.LedgerName,
	}
	policyData, err := topologyrollout.CanonicalPolicyJSON(policyConfig)
	if err != nil {
		t.Fatal(err)
	}
	semanticHash, structuralHash, err := topologyrollout.AdminPolicyHashes(policyConfig)
	if err != nil {
		t.Fatal(err)
	}
	rollout.Status.FrozenPlan.Policy.SemanticHash = semanticHash
	rollout.Status.FrozenPlan.Policy.StructuralHash = structuralHash
	rollout.Status.EffectivePolicy.Policy = rollout.Status.FrozenPlan.Policy
	policy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Status.FrozenPlan.Policy.Namespace,
		Name:      rollout.Status.FrozenPlan.Policy.Name,
		UID:       types.UID(rollout.Status.FrozenPlan.Policy.UID),
		Annotations: map[string]string{
			topologyrollout.PolicyManagedAnnotation:        "true",
			topologyrollout.ConfigLeaseNamespaceAnnotation: "",
			topologyrollout.LedgerUIDAnnotation:            rollout.Status.FrozenPlan.Policy.LedgerUID,
			topologyrollout.AdmissionPrefixAnnotation:      "cvk-topology",
		},
	}, Data: map[string]string{topologyrollout.PolicyDataKey: policyData}}
	controller := true
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "rs-uid", Generation: 2},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: ptr.To[int32](1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{drainSafeLabel: "true", "app": "edge"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "image"}},
					Volumes: []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{},
					}}}},
			},
		},
	}
	token := "11111111-1111-4111-8111-111111111111"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "apps", Name: "edge-0", UID: "pod-uid",
			Labels:      map[string]string{drainSafeLabel: "true", "app": "edge"},
			Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
			Finalizers:  []string{managedprotocol.DrainPodFinalizer},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Name: replicaSet.Name,
				UID: replicaSet.UID, Controller: &controller,
			}},
		},
		Spec: corev1.PodSpec{
			NodeName: "switch-a", Containers: []corev1.Container{{Name: "app", Image: "image"}},
			Volumes: []corev1.Volume{{Name: "config", VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{},
			}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge", UID: "pdb-uid", Generation: 3},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "edge"}},
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			ObservedGeneration: 3, DisruptionsAllowed: 1, CurrentHealthy: 1, DesiredHealthy: 1, ExpectedPods: 1,
		},
	}
	protectedAt := metav1.NewTime(now.Add(-30 * time.Second))
	podState := opsv1alpha1.UpgradeDrainPodStatus{
		Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
		Controller: opsv1alpha1.UpgradeDrainObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Namespace: replicaSet.Namespace,
			Name: replicaSet.Name, UID: string(replicaSet.UID), Generation: replicaSet.Generation,
		},
		PDBs: []opsv1alpha1.UpgradeDrainPDBStatus{{
			UpgradeDrainObjectReference: opsv1alpha1.UpgradeDrainObjectReference{
				APIVersion: policyv1.SchemeGroupVersion.String(), Kind: "PodDisruptionBudget", Namespace: pdb.Namespace,
				Name: pdb.Name, UID: string(pdb.UID), Generation: pdb.Generation,
			},
			ObservedGeneration: pdb.Status.ObservedGeneration, DisruptionsAllowed: pdb.Status.DisruptionsAllowed,
			CurrentHealthy: pdb.Status.CurrentHealthy, DesiredHealthy: pdb.Status.DesiredHealthy,
			ExpectedPods: pdb.Status.ExpectedPods,
		}},
		TerminationGracePeriodSeconds: 30, Phase: opsv1alpha1.UpgradeDrainPodProtected,
		ProtectedAt: &protectedAt,
	}
	setDrainEligibilityHash(t, &podState)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: rollout.Namespace, Name: target.DeviceName, UID: types.UID(target.DeviceUID),
			Generation: target.DeviceGeneration,
		},
		Status: ciskov1.DeviceStatus{NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
			NodeName: target.NodeName, NodeUID: target.NodeUID, DeviceUID: target.DeviceUID,
		}},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: target.NodeName, UID: types.UID(target.NodeUID),
	}}
	workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
	leaf := policyFenceLeaf(rollout, target, "leaf-uid")
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionPending
	leaf.Status.WorkerControl = &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState:       opsv1alpha1.UpgradeManagerAdmissionPending,
		ObservedPolicyEpoch:          rollout.Status.EffectivePolicy.Epoch,
		ObservedControlRevision:      rollout.Spec.Control.Revision,
		ObservedWorkerConfigRevision: device.Status.WorkerRevision.DesiredRevision,
		EffectiveState:               opsv1alpha1.UpgradeWorkerControlReady,
		UpdatedAt:                    metav1.NewTime(now.Add(-time.Minute)),
	}
	started := metav1.NewTime(now.Add(-time.Minute))
	leaf.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
		State:           opsv1alpha1.UpgradeManagerDrainEvicting,
		SessionToken:    token,
		ReservationID:   leaf.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:     leaf.Status.ManagerAdmission.PolicyEpoch,
		ControlRevision: rollout.Spec.Control.Revision,
		NodeUID:         target.NodeUID,
		StartedAt:       started,
		DrainDeadline:   metav1.NewTime(now.Add(9 * time.Minute)),
		UpdatedAt:       started,
		Pods:            []opsv1alpha1.UpgradeDrainPodStatus{podState},
	}
	objects := []client.Object{replicaSet, pod, pdb, policy, device, node}
	objects = append(objects, workerObjects...)
	return rollout, target, leaf, objects
}

func setDrainEligibilityHash(t *testing.T, pod *opsv1alpha1.UpgradeDrainPodStatus) {
	t.Helper()
	hash, err := workloaddrain.EligibilityHash(pod)
	if err != nil {
		t.Fatal(err)
	}
	pod.EligibilityHash = hash
}

func drainTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for name, add := range map[string]func(*runtime.Scheme) error{
		"core": corev1.AddToScheme, "coordination": coordv1.AddToScheme,
		"apps": appsv1.AddToScheme, "policy": policyv1.AddToScheme,
		"ops": opsv1alpha1.AddToScheme, "cisco": ciskov1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("add %s API to scheme: %v", name, err)
		}
	}
	return scheme
}
