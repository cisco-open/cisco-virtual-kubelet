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
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestObserveManagedWorkerRevisionRejectsStaleOrUnboundEvidence(t *testing.T) {
	now := time.Date(2026, time.September, 12, 14, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice, *corev1.Node, []client.Object)
		ready  bool
	}{
		{name: "exact", ready: true},
		{name: "old Pod revision", mutate: func(_ *ciskov1.CiscoDevice, _ *corev1.Node, objects []client.Object) {
			objects[2].(*corev1.Pod).Annotations[managedprotocol.AnnotationWorkerConfigRevision] =
				"sha256:" + strings.Repeat("b", 64)
		}},
		{name: "old worker heartbeat revision", mutate: func(_ *ciskov1.CiscoDevice, node *corev1.Node, _ []client.Object) {
			node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "pre-start heartbeat", mutate: func(device *ciskov1.CiscoDevice, node *corev1.Node, _ []client.Object) {
			for i := range node.Status.Conditions {
				if node.Status.Conditions[i].Type == corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition) {
					node.Status.Conditions[i].LastHeartbeatTime = metav1.NewTime(device.Status.WorkerRevision.PodStartTime.Add(-time.Second))
				}
			}
		}},
		{name: "Deployment not observed", mutate: func(_ *ciskov1.CiscoDevice, _ *corev1.Node, objects []client.Object) {
			objects[0].(*appsv1.Deployment).Status.ObservedGeneration = 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
				Namespace: "lab", Name: "device-a", UID: "device-uid", Generation: 7,
			}}
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "device-a", UID: "node-uid"}}
			device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
				NodeName: node.Name, NodeUID: string(node.UID), DeviceUID: string(device.UID),
				PhysicalIdentity: "serial-a",
			}
			workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
			if tc.mutate != nil {
				tc.mutate(device, node, workerObjects)
			}
			objects := append([]client.Object{device, node}, workerObjects...)
			apiClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objects...).Build()
			status, ready, err := observeManagedWorkerRevision(
				context.Background(), apiClient, now.Add(time.Minute), device,
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}},
				device.Status.WorkerRevision.DesiredRevision,
			)
			if err != nil {
				t.Fatal(err)
			}
			if ready != tc.ready {
				t.Fatalf("ready=%t status=%#v, want %t", ready, status, tc.ready)
			}
		})
	}
}

func TestObserveManagedWorkerRevisionRejectsTerminatingPredecessor(t *testing.T) {
	now := time.Date(2026, time.September, 12, 14, 30, 0, 0, time.UTC)
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "lab", Name: "device-a", UID: "device-uid", Generation: 7,
	}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "device-a", UID: "node-uid"}}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		NodeName: node.Name, NodeUID: string(node.UID), DeviceUID: string(device.UID),
		PhysicalIdentity: "serial-a",
	}
	workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
	predecessor := workerObjects[2].(*corev1.Pod).DeepCopy()
	predecessor.Name = "terminating-worker-pod"
	predecessor.UID = "terminating-worker-pod-uid"
	predecessor.Finalizers = []string{"test.cisco.vk/hold-termination"}
	deletedAt := metav1.NewTime(now.Add(-time.Second))
	predecessor.DeletionTimestamp = &deletedAt
	objects := append([]client.Object{device, node, predecessor}, workerObjects...)
	apiClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objects...).Build()

	status, ready, err := observeManagedWorkerRevision(
		context.Background(), apiClient, now.Add(time.Minute), device,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}},
		device.Status.WorkerRevision.DesiredRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatalf("worker revision authenticated while an owned predecessor was terminating: %#v", status)
	}
}

func TestManagedWorkerRevisionObservationIsIdempotent(t *testing.T) {
	baseTime := time.Date(2026, time.September, 12, 15, 0, 0, 0, time.UTC)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "device-a", UID: "device-uid", Generation: 7},
		Spec: ciskov1.DeviceSpec{GNOI: &ciskov1.GNOIConfig{TLS: &ciskov1.GNOITLSConfig{
			SecretRef: &ciskov1.GNOITLSSecretReference{Name: "gnoi-tls"},
		}}},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "device-a", UID: "node-uid"}}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		NodeName: node.Name, NodeUID: string(node.UID), DeviceUID: string(device.UID),
		PhysicalIdentity: "serial-a",
	}
	workerObjects := attachReadyManagedWorkerProof(t, device, node, baseTime)
	objects := append([]client.Object{device, node}, workerObjects...)
	apiClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(objects...).Build()
	now := baseTime.Add(time.Hour)
	testClock := &fakeClock{now: now}
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, clock: testClock}
	key := client.ObjectKeyFromObject(device)
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), key, &current); err != nil {
		t.Fatal(err)
	}
	desiredRevision := current.Status.WorkerRevision.DesiredRevision
	if err := r.updateGNOIConfigurationCondition(context.Background(), &current,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}},
		desiredRevision, nil); err != nil {
		t.Fatal(err)
	}
	var afterFirst ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), key, &afterFirst); err != nil {
		t.Fatal(err)
	}
	if !afterFirst.Status.WorkerRevision.ObservedAt.Equal(&metav1.Time{Time: baseTime}) {
		t.Fatalf("unchanged evidence moved ObservedAt to %s", afterFirst.Status.WorkerRevision.ObservedAt)
	}
	firstResourceVersion := afterFirst.ResourceVersion
	now = now.Add(time.Hour)
	testClock.now = now
	if err := r.updateGNOIConfigurationCondition(context.Background(), &afterFirst,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}},
		desiredRevision, nil); err != nil {
		t.Fatal(err)
	}
	var afterSecond ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), key, &afterSecond); err != nil {
		t.Fatal(err)
	}
	if afterSecond.ResourceVersion != firstResourceVersion {
		t.Fatalf("unchanged evidence rewrote status: resourceVersion %q -> %q", firstResourceVersion, afterSecond.ResourceVersion)
	}
	if !afterSecond.Status.WorkerRevision.ObservedAt.Equal(&metav1.Time{Time: baseTime}) {
		t.Fatalf("unchanged evidence moved ObservedAt to %s", afterSecond.Status.WorkerRevision.ObservedAt)
	}
}

func TestManagedWorkerRevisionPreFenceClearsOldProof(t *testing.T) {
	now := time.Date(2026, time.September, 12, 16, 0, 0, 0, time.UTC)
	oldRevision := "sha256:" + strings.Repeat("a", 64)
	newRevision := "sha256:" + strings.Repeat("b", 64)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "device-a", UID: "device-uid", Generation: 7},
		Status: ciskov1.DeviceStatus{WorkerRevision: &ciskov1.DeviceWorkerRevisionStatus{
			DesiredRevision: oldRevision, ObservedRevision: oldRevision,
			DeploymentUID: "deployment-uid", DeploymentGeneration: 3,
			PodUID: "pod-uid", PodStartTime: &metav1.Time{Time: now.Add(-time.Minute)},
			ReadyHeartbeatTime: &metav1.Time{Time: now}, ObservedAt: metav1.NewTime(now),
		}},
	}
	apiClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).WithObjects(device).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, clock: &fakeClock{now: now.Add(time.Minute)}}
	if !managedWorkerRevisionNeedsPreFence("deployment-uid", oldRevision, newRevision, device.Status.WorkerRevision) {
		t.Fatal("existing Deployment revision change did not require a pre-fence")
	}
	if err := r.fenceManagedWorkerRevision(context.Background(), device, newRevision); err != nil {
		t.Fatal(err)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	status := current.Status.WorkerRevision
	if status == nil || status.DesiredRevision != newRevision || status.ObservedRevision != "" ||
		status.DeploymentUID != "" || status.DeploymentGeneration != 0 || status.PodUID != "" ||
		status.PodStartTime != nil || status.ReadyHeartbeatTime != nil {
		t.Fatalf("pre-fence retained old worker evidence: %#v", status)
	}
	condition := meta.FindStatusCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionGNOIConfigurationReady)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkerRolloutPending" {
		t.Fatalf("pre-fence gNOI condition = %#v", condition)
	}
	if managedWorkerRevisionNeedsPreFence("deployment-uid", oldRevision, newRevision, status) {
		t.Fatal("persisted desired pre-fence did not permit the next Deployment rollout reconcile")
	}
	if managedWorkerRevisionNeedsPreFence(types.UID(""), "", newRevision, nil) {
		t.Fatal("a new Deployment incorrectly required a pre-fence")
	}
}

func TestWorkerRevisionAcknowledgementRejectsRotationAtGrantFences(t *testing.T) {
	now := time.Date(2026, time.September, 12, 17, 0, 0, 0, time.UTC)
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})

	newRevisionFixture := func(t *testing.T, image string) (*IOSXESoftwareRolloutReconciler, string) {
		t.Helper()
		device := &ciskov1.CiscoDevice{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: rollout.Namespace, Name: target.DeviceName,
				UID: types.UID(target.DeviceUID), Generation: target.DeviceGeneration,
			},
			Spec: ciskov1.DeviceSpec{PhysicalIdentity: target.PhysicalIdentity},
		}
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: target.NodeName, UID: types.UID(target.NodeUID)}}
		device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
			NodeName: target.NodeName, NodeUID: target.NodeUID, DeviceUID: target.DeviceUID,
			PhysicalIdentity: target.PhysicalIdentity,
		}
		workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
		deployment := workerObjects[0].(*appsv1.Deployment)
		deployment.Spec.Template.Spec.Containers[0].Image = image
		revision, err := managedWorkerPodTemplateRevision(&deployment.Spec.Template)
		if err != nil {
			t.Fatal(err)
		}
		deployment.Spec.Template.Annotations[managedprotocol.AnnotationWorkerConfigRevision] = revision
		workerObjects[2].(*corev1.Pod).Annotations[managedprotocol.AnnotationWorkerConfigRevision] = revision
		node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = revision
		device.Status.WorkerRevision.DesiredRevision = revision
		device.Status.WorkerRevision.ObservedRevision = revision

		objects := append([]client.Object{device, node}, workerObjects...)
		apiClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objects...).Build()
		return &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}, revision
	}

	oldReconciler, oldRevision := newRevisionFixture(t, "worker:old")
	oldControl := &opsv1alpha1.UpgradeWorkerControlStatus{ObservedWorkerConfigRevision: oldRevision}
	if got, err := oldReconciler.requireWorkerRevisionAcknowledgement(
		context.Background(), rollout, target, oldControl,
	); err != nil || got != oldRevision {
		t.Fatalf("exact worker acknowledgement = %q, %v; want %q", got, err, oldRevision)
	}

	newReconciler, newRevision := newRevisionFixture(t, "worker:new")
	if newRevision == oldRevision {
		t.Fatal("test worker PodTemplate change did not change its content revision")
	}
	for _, fence := range []string{"initial admission", "ledger CAS", "leaf status CAS"} {
		t.Run(fence, func(t *testing.T) {
			if _, err := newReconciler.requireWorkerRevisionAcknowledgement(
				context.Background(), rollout, target, oldControl,
			); err == nil || !strings.Contains(err.Error(), "no longer matches") {
				t.Fatalf("stale pre-rotation acknowledgement error = %v, want revision mismatch", err)
			}
		})
	}
	newControl := &opsv1alpha1.UpgradeWorkerControlStatus{ObservedWorkerConfigRevision: newRevision}
	if got, err := newReconciler.requireWorkerRevisionAcknowledgement(
		context.Background(), rollout, target, newControl,
	); err != nil || got != newRevision {
		t.Fatalf("replacement worker acknowledgement = %q, %v; want %q", got, err, newRevision)
	}
	if _, err := newReconciler.requireWorkerRevisionAcknowledgement(
		context.Background(), rollout, target, nil,
	); err == nil || !strings.Contains(err.Error(), "no worker configuration revision acknowledgement") {
		t.Fatalf("missing worker acknowledgement error = %v", err)
	}
}
