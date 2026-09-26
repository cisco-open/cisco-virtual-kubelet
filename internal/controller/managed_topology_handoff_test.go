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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configengine "github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	configprovider "github.com/cisco/virtual-kubelet-cisco/internal/provider"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
)

type legacyHandoffFixture struct {
	r      *CiscoDeviceReconciler
	client client.Client
	key    types.NamespacedName
	clock  *fakeClock
}

func newLegacyHandoffFixture(t *testing.T) legacyHandoffFixture {
	t.Helper()
	const projectionHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	device := newDevice("switch-handoff", "edge")
	device.UID = "device-uid"
	device.Spec.PhysicalIdentity = "serial-switch-handoff"
	device.Finalizers = []string{ciscoDeviceFinalizer}
	device.Annotations = map[string]string{managedprotocol.AnnotationRequestLegacyHandoff: "node-uid"}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{
		EffectiveLabelHash: projectionHash, SourceResourceVersion: "1", LastSuccessfulTime: metav1.NewTime(base.Add(-time.Minute)),
	}

	managedSA := managedWorkerServiceAccountName(device)
	managedUsername := "system:serviceaccount:" + device.Namespace + ":" + managedSA
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: device.Name, UID: "node-uid", ResourceVersion: "1",
			Annotations: map[string]string{
				managedprotocol.AnnotationManaged:         "true",
				managedprotocol.AnnotationDeviceNamespace: device.Namespace,
				managedprotocol.AnnotationDeviceName:      device.Name,
				managedprotocol.AnnotationDeviceUID:       string(device.UID),
				managedprotocol.AnnotationNodeName:        device.Name,
				managedprotocol.AnnotationNodeUID:         "node-uid",
				managedprotocol.AnnotationWorkerUsername:  managedUsername,
				managedprotocol.AnnotationWorkerProtocol:  managedprotocol.Version,
				managedprotocol.AnnotationProjectedKeys:   "",
				managedprotocol.AnnotationProjectionHash:  projectionHash,
				managedprotocol.AnnotationManagedTaints:   "",
			},
		},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{topologyInitializationTaint()}},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(base.Add(-time.Minute)),
		}}},
	}

	leaseAnnotations, leaseLabels := managedMutationLeaseMetadata(device, node.Name, string(node.UID), managedUsername)
	mutationLease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace,
		Name:      configengine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily),
		UID:       "mutation-lease-uid", ResourceVersion: "1",
		Annotations: leaseAnnotations, Labels: leaseLabels,
	}}
	policy, ledger := managedPolicyAndLedger(t, nil)
	labels := perDeviceDeploymentLabels(device.Name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: device.Name + deploymentSuffix,
			UID: "managed-deployment-uid", ResourceVersion: "1", Generation: 1,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice"))},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{ServiceAccountName: managedSA},
			},
		},
	}

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &appsv1.Deployment{}, &corev1.Node{}, &corev1.Pod{}).
		WithObjects(device, node, mutationLease, policy, ledger).Build()
	writeClient := leaseUIDAssigningClient{Client: baseClient}
	r := &CiscoDeviceReconciler{
		Client: writeClient, APIReader: baseClient, Scheme: scheme, Image: "cisco-vk:test",
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		WorkerServiceAccountPolicyEpoch: testWorkerServiceAccountPolicyEpoch, clock: clock,
	}
	if err := r.ensureVKAccess(context.Background(), device, managedSA, true); err != nil {
		t.Fatalf("seed managed worker access: %v", err)
	}
	deployment.ResourceVersion = ""
	if err := r.Create(context.Background(), deployment); err != nil {
		t.Fatalf("seed managed worker Deployment: %v", err)
	}
	return legacyHandoffFixture{
		r: r, client: baseClient, key: types.NamespacedName{Namespace: device.Namespace, Name: device.Name}, clock: clock,
	}
}

func (f legacyHandoffFixture) device(t *testing.T) *ciskov1.CiscoDevice {
	t.Helper()
	var device ciskov1.CiscoDevice
	if err := f.client.Get(context.Background(), f.key, &device); err != nil {
		t.Fatal(err)
	}
	return &device
}

func (f legacyHandoffFixture) deployment(t *testing.T) *appsv1.Deployment {
	t.Helper()
	var deployment appsv1.Deployment
	if err := f.client.Get(context.Background(), types.NamespacedName{Namespace: f.key.Namespace, Name: f.key.Name + deploymentSuffix}, &deployment); err != nil {
		t.Fatal(err)
	}
	return &deployment
}

func TestLegacyHandoffFullReconcileUsesIsolatedWorkerAndCompletes(t *testing.T) {
	ctx := context.Background()
	fixture := newLegacyHandoffFixture(t)
	r := fixture.r
	r.ServiceAccount = "cvk-compatibility"

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("start handoff reconcile: %v", err)
	}
	device := fixture.device(t)
	if device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffPreparing || device.Status.NodeIdentity == nil {
		t.Fatalf("initial handoff status = %#v, nodeIdentity=%#v", device.Status.LegacyHandoff, device.Status.NodeIdentity)
	}
	if got := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; got != string(device.UID) {
		t.Fatalf("initial handoff marker = %q, want device UID %q", got, device.UID)
	}
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	deployment := fixture.deployment(t)
	if deployment.Spec.Template.Spec.ServiceAccountName != legacySA || deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("pre-release Deployment SA/strategy = %q/%q, want isolated legacy/Recreate", deployment.Spec.Template.Spec.ServiceAccountName, deployment.Spec.Strategy.Type)
	}
	if _, present := findEnvVar(deployment.Spec.Template.Spec.Containers[0].Env, "CISCO_VK_MANAGED_TOPOLOGY"); present {
		t.Fatal("legacy handoff worker retained managed-topology identity environment")
	}
	if value := deployment.Spec.Template.Annotations[managedprotocol.AnnotationLegacyHandoffRelease]; value != "" {
		t.Fatalf("pre-release Deployment has release epoch %q", value)
	}

	// This old legacy Pod proves that merely changing the ServiceAccount is not
	// enough: it started before Node ownership was released and its ReplicaSet
	// does not carry the manager-issued release epoch.
	oldReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "old-legacy", UID: "old-rs-uid", Labels: deployment.Spec.Template.Labels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{Template: *deployment.Spec.Template.DeepCopy()},
	}
	if err := fixture.client.Create(ctx, oldReplicaSet); err != nil {
		t.Fatal(err)
	}
	oldStart := metav1.NewTime(fixture.clock.now.Add(30 * time.Second))
	oldPod := readyLegacyPod(device.Namespace, "old-legacy-pod", "old-pod-uid", legacySA, oldReplicaSet, oldStart)
	if err := fixture.client.Create(ctx, oldPod); err != nil {
		t.Fatal(err)
	}
	// An exact but unowned maintenance taint is operator state. Releasing the
	// managed writer must not infer ownership merely from the shared taint key.
	var operatorGuardedNode corev1.Node
	if err := fixture.client.Get(ctx, types.NamespacedName{Name: device.Status.NodeIdentity.NodeName}, &operatorGuardedNode); err != nil {
		t.Fatal(err)
	}
	operatorGuardedNode.Spec.Taints = upsertTaint(operatorGuardedNode.Spec.Taints, maintenanceGuardTaint())
	if err := fixture.client.Update(ctx, &operatorGuardedNode); err != nil {
		t.Fatal(err)
	}

	fixture.clock.Advance(time.Minute)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("release handoff reconcile: %v", err)
	}
	device = fixture.device(t)
	handoff := device.Status.LegacyHandoff
	if handoff == nil || handoff.Phase != ciskov1.DeviceLegacyHandoffLegacyWriterPending || handoff.NodeReleasedAt == nil || device.Status.NodeIdentity == nil {
		t.Fatalf("released handoff status = %#v, nodeIdentity=%#v", handoff, device.Status.NodeIdentity)
	}
	deployment = fixture.deployment(t)
	releaseEpoch := handoff.NodeReleasedAt.UTC().Format(time.RFC3339Nano)
	if got := deployment.Spec.Template.Annotations[managedprotocol.AnnotationLegacyHandoffRelease]; got != releaseEpoch {
		t.Fatalf("release rollout epoch = %q, want %q", got, releaseEpoch)
	}
	if revoked, err := r.managedWorkerAccessRevoked(ctx, device); err != nil || !revoked {
		t.Fatalf("managed access revoked = %v, %v", revoked, err)
	}
	var releasedNode corev1.Node
	if err := fixture.client.Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &releasedNode); err != nil {
		t.Fatal(err)
	}
	if !legacyHandoffNodeMatches(&releasedNode, handoff) ||
		!hasTaint(releasedNode.Spec.Taints, topologyInitializationTaint()) ||
		!hasTaint(releasedNode.Spec.Taints, maintenanceGuardTaint()) {
		t.Fatalf("released Node metadata/guard = annotations=%v taints=%v", releasedNode.Annotations, releasedNode.Spec.Taints)
	}

	markLegacyDeploymentRolledOut(t, fixture.client, deployment)
	if err := publishLegacyNodeHeartbeat(t, fixture.client, device, handoff, fixture.clock.now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(30 * time.Second)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("pre-release Pod rejection reconcile: %v", err)
	}
	if got := fixture.device(t).Status.LegacyHandoff.Phase; got != ciskov1.DeviceLegacyHandoffLegacyWriterPending {
		t.Fatalf("old ReplicaSet/Pod completed handoff: phase=%q", got)
	}

	deployment = fixture.deployment(t)
	newReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "released-legacy", UID: "released-rs-uid", Labels: deployment.Spec.Template.Labels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{Template: *deployment.Spec.Template.DeepCopy()},
	}
	if err := fixture.client.Create(ctx, newReplicaSet); err != nil {
		t.Fatal(err)
	}
	newStart := metav1.NewTime(handoff.NodeReleasedAt.Add(time.Minute))
	newPod := readyLegacyPod(device.Namespace, "released-legacy-pod", "released-pod-uid", legacySA, newReplicaSet, newStart)
	if err := fixture.client.Create(ctx, newPod); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(time.Minute)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("begin shared compatibility transition: %v", err)
	}
	device = fixture.device(t)
	if device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffSharedWriterPending ||
		device.Status.NodeIdentity != nil || device.Status.TopologyProjection != nil || device.Status.HealthObservation != nil ||
		device.Status.WorkerRevision != nil {
		t.Fatalf("shared-pending status = handoff=%#v identity=%#v projection=%#v health=%#v workerRevision=%#v",
			device.Status.LegacyHandoff, device.Status.NodeIdentity, device.Status.TopologyProjection,
			device.Status.HealthObservation, device.Status.WorkerRevision)
	}
	if err := fixture.client.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: device.Name + deploymentSuffix}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("isolated Deployment was not removed by Recreate barrier: %v", err)
	}
	for _, object := range []client.Object{oldPod, newPod, oldReplicaSet, newReplicaSet} {
		if err := fixture.client.Delete(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("create shared compatibility worker: %v", err)
	}
	device = fixture.device(t)
	sharedSA := r.vkServiceAccountName()
	deployment = fixture.deployment(t)
	if deployment.Spec.Template.Spec.ServiceAccountName != sharedSA || deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("shared compatibility Deployment SA/strategy = %q/%q", deployment.Spec.Template.Spec.ServiceAccountName, deployment.Spec.Strategy.Type)
	}
	// fake.Client does not assign the API-server generation used by the
	// readiness proof.
	deployment.Generation = 1
	if err := fixture.client.Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	markLegacyDeploymentRolledOut(t, fixture.client, deployment)
	sharedReplicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "shared-legacy", UID: "shared-rs-uid", Labels: deployment.Spec.Template.Labels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(deployment, appsv1.SchemeGroupVersion.WithKind("Deployment"))},
		},
		Spec: appsv1.ReplicaSetSpec{Template: *deployment.Spec.Template.DeepCopy()},
	}
	if err := fixture.client.Create(ctx, sharedReplicaSet); err != nil {
		t.Fatal(err)
	}
	sharedReadyAt := device.Status.LegacyHandoff.IsolatedReadyAt.Add(30 * time.Second)
	sharedPod := readyLegacyPod(device.Namespace, "shared-legacy-pod", "shared-pod-uid", sharedSA, sharedReplicaSet, metav1.NewTime(sharedReadyAt))
	sharedPod.Labels = deployment.Spec.Template.Labels
	if err := fixture.client.Create(ctx, sharedPod); err != nil {
		t.Fatal(err)
	}
	if err := publishLegacyNodeHeartbeat(t, fixture.client, device, device.Status.LegacyHandoff, sharedReadyAt); err != nil {
		t.Fatal(err)
	}
	fixture.clock.Advance(time.Minute)
	var debugNode corev1.Node
	if err := fixture.client.Get(ctx, types.NamespacedName{Name: device.Status.LegacyHandoff.NodeName}, &debugNode); err != nil {
		t.Fatal(err)
	}
	debugDeployment := fixture.deployment(t)
	if ready, err := r.legacyWriterReadyForServiceAccount(ctx, device, &debugNode, sharedSA, device.Status.LegacyHandoff.IsolatedReadyAt.Time); err != nil || !ready {
		t.Fatalf("shared readiness proof before completion = %v, %v; deployment=%#v template=%#v node=%#v", ready, err, debugDeployment.Status, debugDeployment.Spec.Template, debugNode.Status.Conditions)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("complete shared compatibility transition: %v", err)
	}
	device = fixture.device(t)
	if device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete ||
		device.Status.LegacyHandoff.CompletedAt == nil {
		t.Fatalf("completed shared handoff status = %#v", device.Status.LegacyHandoff)
	}
	if got := r.serviceAccountForDevice(device); got != sharedSA {
		t.Fatalf("completed handoff ServiceAccount = %q, want shared %q", got, sharedSA)
	}
	assertWorkerAccessAbsent(t, fixture.client, device, legacySA)
	if err := r.verifySharedLegacyAccess(ctx, device); err != nil {
		t.Fatalf("shared compatibility access: %v", err)
	}
	if err := fixture.client.Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &releasedNode); err != nil {
		t.Fatal(err)
	}
	if hasTaint(releasedNode.Spec.Taints, topologyInitializationTaint()) {
		t.Fatal("completed legacy Node retained topology initialization guard")
	}

	// A manager restart with the global feature flag off must recover the
	// namespace-shared compatibility identity, never the temporary UID account.
	restarted := &CiscoDeviceReconciler{
		Client: fixture.r.Client, APIReader: fixture.client, Scheme: fixture.r.Scheme,
		TopologyPolicyNamespace: fixture.r.TopologyPolicyNamespace, TopologyPolicyName: fixture.r.TopologyPolicyName,
		LeaseNamespace: fixture.r.LeaseNamespace, ServiceAccount: r.ServiceAccount,
		WorkerServiceAccountPolicyEpoch: testWorkerServiceAccountPolicyEpoch, clock: fixture.clock,
	}
	restartedDevice := fixture.device(t)
	result, err := restarted.reconcileManagedTopology(ctx, restartedDevice)
	if err != nil {
		t.Fatalf("restart completed-state recovery: %v", err)
	}
	if result.Managed || !result.LegacyWorker || restarted.serviceAccountForDevice(restartedDevice, result) != sharedSA {
		t.Fatalf("restart topology result = %+v, SA=%q", result, restarted.serviceAccountForDevice(restartedDevice, result))
	}
}

func TestLegacyHandoffSelectorExitAndSecondCycle(t *testing.T) {
	ctx := context.Background()
	fixture := newLegacyHandoffFixture(t)
	fixture.r.ManagedTopology = true
	device := fixture.device(t)
	result, err := fixture.r.reconcileManagedTopology(ctx, device)
	if err != nil {
		t.Fatalf("selector-exit handoff: %v", err)
	}
	if !result.Managed || !result.LegacyWorker || device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffPreparing {
		t.Fatalf("selector-exit result/status = %+v / %#v", result, device.Status.LegacyHandoff)
	}

	// Exercise the manager-controlled status cycle without replaying every
	// workload observation already covered by the full happy-path test.
	completeLegacyHandoffFixture(t, fixture)
	device = fixture.device(t)
	delete(device.Annotations, managedprotocol.AnnotationRequestLegacyHandoff)
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	if err := fixture.client.Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		device = fixture.device(t)
		result, err = fixture.r.reconcileManagedTopology(ctx, device)
		if err != nil {
			t.Fatalf("managed re-enrollment attempt %d: %v", attempt, err)
		}
		if result.Managed {
			break
		}
		if !result.HoldWorker {
			t.Fatalf("re-enrollment did not hold the worker while shared authority drained: %+v", result)
		}
	}
	if !result.Managed || device.Status.NodeIdentity == nil || device.Status.LegacyHandoff != nil {
		t.Fatalf("re-enrollment result/status = %+v / identity=%#v handoff=%#v", result, device.Status.NodeIdentity, device.Status.LegacyHandoff)
	}
	if err := fixture.r.ensureVKAccess(ctx, device, managedWorkerServiceAccountName(device), true); err != nil {
		t.Fatal(err)
	}
	if device.Annotations == nil {
		device.Annotations = map[string]string{}
	}
	device.Annotations[managedprotocol.AnnotationRequestLegacyHandoff] = device.Status.NodeIdentity.NodeUID
	delete(device.Labels, managedprotocol.AnnotationManaged)
	if err := fixture.client.Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	device = fixture.device(t)
	result, err = fixture.r.reconcileManagedTopology(ctx, device)
	if err != nil {
		t.Fatalf("second selector-exit handoff: %v", err)
	}
	if !result.LegacyWorker || device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffPreparing {
		t.Fatalf("second-cycle result/status = %+v / %#v", result, device.Status.LegacyHandoff)
	}
}

func TestLegacyHandoffReleasePreservesOnlyOperatorCordon(t *testing.T) {
	for _, test := range []struct {
		name              string
		managedCordon     bool
		wantUnschedulable bool
	}{
		{name: "CVK cordon is released", managedCordon: true, wantUnschedulable: false},
		{name: "operator cordon is preserved", managedCordon: false, wantUnschedulable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newLegacyHandoffFixture(t)
			device := fixture.device(t)
			if _, err := fixture.r.reconcileManagedTopology(ctx, device); err != nil {
				t.Fatal(err)
			}
			var node corev1.Node
			if err := fixture.client.Get(ctx, types.NamespacedName{Name: device.Status.NodeIdentity.NodeName}, &node); err != nil {
				t.Fatal(err)
			}
			node.Spec.Unschedulable = true
			if test.managedCordon {
				node.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID] = string(device.UID)
			}
			if err := fixture.client.Update(ctx, &node); err != nil {
				t.Fatal(err)
			}
			handoff := copyLegacyHandoff(device.Status.LegacyHandoff)
			handoff.Phase = ciskov1.DeviceLegacyHandoffLegacyWriterPending
			releasedAt := metav1.NewTime(fixture.clock.now.Add(time.Minute))
			handoff.NodeReleasedAt = &releasedAt
			released, _, err := fixture.r.releaseManagedNodeToLegacy(ctx, device, handoff)
			if err != nil {
				t.Fatal(err)
			}
			if released.Spec.Unschedulable != test.wantUnschedulable {
				t.Fatalf("released Node unschedulable=%v, want %v", released.Spec.Unschedulable, test.wantUnschedulable)
			}
			if _, present := released.Annotations[managedprotocol.AnnotationAppHostingCordonDeviceUID]; present {
				t.Fatal("released Node retained CVK app-hosting cordon ownership marker")
			}
		})
	}
}

func TestManagedWriterStopCheckScopesSharedAccountsToDevice(t *testing.T) {
	ctx := context.Background()
	device := managedAccessDevice("switch-a")
	other := managedAccessDevice("switch-b")
	sharedAccount := managedprotocol.AppHostingServiceAccount

	otherDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: other.Namespace, Name: other.Name + deploymentSuffix, UID: "other-deployment-uid",
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(other, ciskov1.GroupVersion.WithKind("CiscoDevice")),
		},
	}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: perDeviceDeploymentLabels(other.Name)},
		Spec:       corev1.PodSpec{ServiceAccountName: sharedAccount},
	}}}
	otherReplicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: other.Namespace, Name: "switch-b-rs", UID: "other-rs-uid",
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(otherDeployment, appsv1.SchemeGroupVersion.WithKind("Deployment")),
		},
	}}
	otherPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: other.Namespace, Name: "switch-b-pod", UID: "other-pod-uid",
		Labels: perDeviceDeploymentLabels(other.Name), OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(otherReplicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
		},
	}, Spec: corev1.PodSpec{ServiceAccountName: sharedAccount}}
	r := reconcilerFor(t, device, other, otherDeployment, otherReplicaSet, otherPod)

	stopped, err := r.managedWriterWorkloadsStopped(ctx, device)
	if err != nil || !stopped {
		t.Fatalf("other device shared worker blocked handoff: stopped=%v err=%v", stopped, err)
	}

	// Once an exact Pod for this device is present, it must block even after
	// its Deployment template has already switched to the isolated legacy SA.
	localDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: device.Name + deploymentSuffix, UID: "local-deployment-uid",
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice")),
		},
	}, Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		ServiceAccountName: topologyLegacyWorkerServiceAccountName(device),
	}}}}
	localReplicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "switch-a-old-rs", UID: "local-rs-uid",
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(localDeployment, appsv1.SchemeGroupVersion.WithKind("Deployment")),
		},
	}}
	localPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "switch-a-old-pod", UID: "local-pod-uid",
		Labels: perDeviceDeploymentLabels(device.Name), OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(localReplicaSet, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
		},
	}, Spec: corev1.PodSpec{ServiceAccountName: sharedAccount}}
	for _, object := range []client.Object{localDeployment, localReplicaSet, localPod} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	stopped, err = r.managedWriterWorkloadsStopped(ctx, device)
	if err != nil || stopped {
		t.Fatalf("exact old shared worker did not block handoff: stopped=%v err=%v", stopped, err)
	}
}

func TestLegacyHandoffCannotRollbackAfterAuthorizationRecorded(t *testing.T) {
	ctx := context.Background()
	fixture := newLegacyHandoffFixture(t)
	device := fixture.device(t)
	result, err := fixture.r.reconcileManagedTopology(ctx, device)
	if err != nil || !result.LegacyWorker || device.Status.LegacyHandoff == nil {
		t.Fatalf("record initial handoff = %+v, %v, status=%#v", result, err, device.Status.LegacyHandoff)
	}

	// Simulate both possible operator rollbacks: re-enable managed topology,
	// re-enter the fleet selector, and remove the request annotation. The
	// immutable status record has already consumed that authorization, so the
	// reconciler must keep selecting the legacy worker until Complete.
	delete(device.Annotations, managedprotocol.AnnotationRequestLegacyHandoff)
	device.Labels = map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		topology.CiscoTopologyLabelPrefix + "site": "site-a",
	}
	if err := fixture.client.Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	fixture.r.ManagedTopology = true
	device = fixture.device(t)
	result, err = fixture.r.reconcileManagedTopology(ctx, device)
	if err != nil {
		t.Fatalf("continue handoff after configuration rollback: %v", err)
	}
	if !result.Managed || !result.LegacyWorker || device.Status.LegacyHandoff == nil ||
		device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffPreparing {
		t.Fatalf("rollback escaped handoff: result=%+v status=%#v", result, device.Status.LegacyHandoff)
	}
}

func TestIsolatedLegacyRecoveryUsesAccessEvidenceBeforeMarker(t *testing.T) {
	ctx := context.Background()
	device := newDevice("interrupted-isolation", "edge")
	device.UID = "interrupted-device-uid"
	r := reconcilerFor(t, device)
	r.ManagedTopology = true
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
		t.Fatalf("seed pre-marker access: %v", err)
	}
	r.ManagedTopology = false
	var current ciskov1.CiscoDevice
	if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	result, err := r.reconcileManagedTopology(ctx, &current)
	if err != nil {
		t.Fatalf("recover access-before-marker interruption: %v", err)
	}
	if !result.LegacyWorker || current.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker] != string(current.UID) {
		t.Fatalf("recovered result/marker = %+v / %q", result, current.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker])
	}
}

func TestIsolatedLegacyRecoveryRevokesExactPartialAccessBeforeMarker(t *testing.T) {
	tests := []struct {
		name           string
		removeRoleBind bool
	}{
		{name: "service account only", removeRoleBind: true},
		{name: "service account and RoleBinding"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			device := newDevice("interrupted-partial-isolation", "edge")
			device.UID = "interrupted-partial-device-uid"
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			legacySA := topologyLegacyWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
				t.Fatalf("seed generated access: %v", err)
			}
			crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
				Name: vkAccessClusterRoleBindingName(device.Namespace, legacySA),
			}}
			if err := r.Delete(ctx, crb); err != nil {
				t.Fatal(err)
			}
			if test.removeRoleBind {
				rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: legacySA}}
				if err := r.Delete(ctx, rb); err != nil {
					t.Fatal(err)
				}
			}

			// Model a default-off manager restart: the partial namespaced state did
			// not enter retirement preflight, so no policy epoch was derived.
			r.ManagedTopology = false
			r.WorkerServiceAccountPolicyEpoch = ""
			var current ciskov1.CiscoDevice
			if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
				t.Fatal(err)
			}
			result, err := r.reconcileManagedTopology(ctx, &current)
			if err != nil {
				t.Fatalf("recover exact partial access: %v", err)
			}
			if result.LegacyWorker || result.Managed {
				t.Fatalf("partial generated access was adopted: %+v", result)
			}
			if marker := current.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; marker != "" {
				t.Fatalf("partial access created isolated-worker marker %q", marker)
			}
			assertWorkerAccessAbsent(t, r.Client, device, legacySA)
		})
	}
}

func TestIsolatedLegacyRecoveryRetainsDriftedOrAdditivePartialAccess(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(context.Context, *CiscoDeviceReconciler, *ciskov1.CiscoDevice, string) error
		wantErrContains string
	}{
		{
			name: "drifted canonical RoleBinding",
			mutate: func(ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) error {
				var binding rbacv1.RoleBinding
				if err := r.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: serviceAccount}, &binding); err != nil {
					return err
				}
				binding.Annotations[managedprotocol.AnnotationDeviceUID] = "foreign-device-uid"
				return r.Update(ctx, &binding)
			},
			wantErrContains: "existing generated worker RoleBinding is invalid",
		},
		{
			name: "additive RoleBinding",
			mutate: func(ctx context.Context, r *CiscoDeviceReconciler, device *ciskov1.CiscoDevice, serviceAccount string) error {
				return r.Create(ctx, &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "partial-additive-binding"},
					RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "view"},
					Subjects:   exactWorkerSubject(device.Namespace, serviceAccount),
				})
			},
			wantErrContains: "unexpected additive RoleBinding",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			device := newDevice("interrupted-untrusted-partial", "edge")
			device.UID = "interrupted-untrusted-partial-device-uid"
			r := reconcilerFor(t, device)
			r.ManagedTopology = true
			legacySA := topologyLegacyWorkerServiceAccountName(device)
			if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
				t.Fatalf("seed generated access: %v", err)
			}
			if err := r.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
				Name: vkAccessClusterRoleBindingName(device.Namespace, legacySA),
			}}); err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(ctx, r, device, legacySA); err != nil {
				t.Fatal(err)
			}
			r.ManagedTopology = false
			r.WorkerServiceAccountPolicyEpoch = ""
			var current ciskov1.CiscoDevice
			if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
				t.Fatal(err)
			}
			if _, err := r.reconcileManagedTopology(ctx, &current); err == nil ||
				!strings.Contains(err.Error(), test.wantErrContains) {
				t.Fatalf("untrusted partial recovery error=%v", err)
			}
			for _, object := range []client.Object{
				&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: legacySA}},
				&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: legacySA}},
			} {
				if err := r.Get(ctx, client.ObjectKeyFromObject(object), object); err != nil {
					t.Fatalf("fail-closed recovery mutated %T: %v", object, err)
				}
			}
		})
	}
}

func TestLegacyHandoffPreparingRecoversStatusBeforeMarkerCrash(t *testing.T) {
	ctx := context.Background()
	fixture := newLegacyHandoffFixture(t)
	device := fixture.device(t)
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	if err := fixture.r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
		t.Fatalf("seed isolated access: %v", err)
	}
	handoff := &ciskov1.DeviceLegacyHandoffStatus{
		Phase:                ciskov1.DeviceLegacyHandoffPreparing,
		DeviceUID:            string(device.UID),
		NodeName:             device.Status.NodeIdentity.NodeName,
		NodeUID:              device.Status.NodeIdentity.NodeUID,
		ProjectionHash:       device.Status.TopologyProjection.EffectiveLabelHash,
		LegacyWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + legacySA,
		RequestedAt:          metav1.NewTime(fixture.clock.now),
	}
	if err := fixture.r.patchLegacyHandoffStatus(ctx, device, handoff, "TestCrashWindow", "status persisted before marker"); err != nil {
		t.Fatal(err)
	}
	device = fixture.device(t)
	if got := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; got != "" {
		t.Fatalf("crash fixture unexpectedly has marker %q", got)
	}
	result, err := fixture.r.reconcileManagedTopology(ctx, device)
	if err != nil {
		t.Fatalf("recover status-before-marker interruption: %v", err)
	}
	if !result.Managed || !result.LegacyWorker {
		t.Fatalf("recovery result = %+v, want managed isolated worker", result)
	}
	device = fixture.device(t)
	if got := device.Annotations[managedprotocol.AnnotationIsolatedLegacyWorker]; got != string(device.UID) {
		t.Fatalf("recovered marker = %q, want %q", got, device.UID)
	}
}

func TestLegacyHandoffRejectsForgedRecoveryState(t *testing.T) {
	ctx := context.Background()
	t.Run("isolated marker without access evidence", func(t *testing.T) {
		device := newDevice("forged-marker", "edge")
		device.UID = "forged-device-uid"
		device.Annotations = map[string]string{managedprotocol.AnnotationIsolatedLegacyWorker: string(device.UID)}
		r := reconcilerFor(t, device)
		var current ciskov1.CiscoDevice
		if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcileManagedTopology(ctx, &current); err == nil || !strings.Contains(err.Error(), "no exact owned ServiceAccount evidence") {
			t.Fatalf("forged marker error = %v", err)
		}
		assertWorkerAccessAbsent(t, r.Client, device, topologyLegacyWorkerServiceAccountName(device))
	})

	t.Run("completed status without manager-created access", func(t *testing.T) {
		base := metav1.NewTime(time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC))
		device := newDevice("forged-complete", "edge")
		device.UID = "forged-complete-uid"
		legacySA := topologyLegacyWorkerServiceAccountName(device)
		releasedAt := metav1.NewTime(base.Add(time.Minute))
		isolatedReadyAt := metav1.NewTime(base.Add(2 * time.Minute))
		completedAt := metav1.NewTime(base.Add(3 * time.Minute))
		device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{
			Phase: ciskov1.DeviceLegacyHandoffComplete, DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			ProjectionHash:       "sha256:" + strings.Repeat("a", 64),
			LegacyWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + legacySA,
			RequestedAt:          base, NodeReleasedAt: &releasedAt, IsolatedReadyAt: &isolatedReadyAt, CompletedAt: &completedAt,
		}
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name: device.Name, UID: "node-uid", Annotations: map[string]string{managedprotocol.AnnotationLegacyHandoff: "node-uid"},
		}}
		r := reconcilerFor(t, device, node)
		var current ciskov1.CiscoDevice
		if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcileManagedTopology(ctx, &current); err == nil || !strings.Contains(err.Error(), "shared compatibility access evidence") {
			t.Fatalf("forged completed status error = %v", err)
		}
		assertWorkerAccessAbsent(t, r.Client, device, legacySA)
	})

	t.Run("pending status without managed identity", func(t *testing.T) {
		base := metav1.NewTime(time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC))
		device := newDevice("broken-pending", "edge")
		device.UID = "broken-device-uid"
		releasedAt := metav1.NewTime(base.Add(time.Minute))
		device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{
			Phase: ciskov1.DeviceLegacyHandoffLegacyWriterPending, DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			ProjectionHash: "sha256:" + strings.Repeat("a", 64), RequestedAt: base, NodeReleasedAt: &releasedAt,
			LegacyWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + topologyLegacyWorkerServiceAccountName(device),
		}
		r := reconcilerFor(t, device)
		var current ciskov1.CiscoDevice
		if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcileManagedTopology(ctx, &current); err == nil || !strings.Contains(err.Error(), "shared-writer transition or complete phase") {
			t.Fatalf("inconsistent pending restart error = %v", err)
		}
		assertWorkerAccessAbsent(t, r.Client, device, topologyLegacyWorkerServiceAccountName(device))
	})
}

func TestLegacyHandoffSafetyBlocksActiveControlState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice)
	}{
		{
			name: "active topology lock",
			mutate: func(device *ciskov1.CiscoDevice) {
				device.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
					State: ciskov1.DeviceTopologyLockActive, ReservationID: "reservation",
				}
			},
		},
		{
			name: "releasing topology lock",
			mutate: func(device *ciskov1.CiscoDevice) {
				device.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
					State: ciskov1.DeviceTopologyLockReleasing, ReservationID: "reservation",
				}
			},
		},
		{
			name: "unsettled maintenance",
			mutate: func(device *ciskov1.CiscoDevice) {
				device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
					Phase: ciskov1.DeviceMaintenanceSessionActive, SessionToken: "session",
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newLegacyHandoffFixture(t)
			device := fixture.device(t)
			tc.mutate(device)
			if err := fixture.client.Status().Update(ctx, device); err != nil {
				t.Fatal(err)
			}
			device = fixture.device(t)
			result, err := fixture.r.reconcileManagedTopology(ctx, device)
			if err == nil || !strings.Contains(err.Error(), "LegacyHandoffBlocked") {
				t.Fatalf("blocked handoff error = %v", err)
			}
			if !result.Managed || device.Status.LegacyHandoff != nil {
				t.Fatalf("blocked handoff result/status = %+v / %#v", result, device.Status.LegacyHandoff)
			}
			assertWorkerAccessAbsent(t, fixture.client, device, topologyLegacyWorkerServiceAccountName(device))
		})
	}
}

type nodeDeleteRequiresRevokedLegacyAccessClient struct {
	client.Client
	namespace, serviceAccount string
}

func (c nodeDeleteRequiresRevokedLegacyAccessClient) Delete(
	ctx context.Context,
	obj client.Object,
	opts ...client.DeleteOption,
) error {
	if _, deletingNode := obj.(*corev1.Node); deletingNode {
		key := types.NamespacedName{Name: vkAccessClusterRoleBindingName(c.namespace, c.serviceAccount)}
		if err := c.Client.Get(ctx, key, &rbacv1.ClusterRoleBinding{}); err == nil {
			return fmt.Errorf("Node delete observed before isolated legacy access revocation")
		} else if !apierrors.IsNotFound(err) {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestCompletedLegacyDeviceDeletionRevokesWorkerBeforeNode(t *testing.T) {
	ctx := context.Background()
	now := metav1.NewTime(time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC))
	releasedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	isolatedReadyAt := metav1.NewTime(now.Add(-time.Minute))
	completedAt := now
	device := newDevice("delete-isolated", "edge")
	device.UID = "delete-device-uid"
	device.Finalizers = []string{ciscoDeviceFinalizer}
	device.DeletionTimestamp = &now
	device.Annotations = map[string]string{managedprotocol.AnnotationIsolatedLegacyWorker: string(device.UID)}
	legacySA := topologyLegacyWorkerServiceAccountName(device)
	device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{
		Phase: ciskov1.DeviceLegacyHandoffComplete, DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
		ProjectionHash:       "sha256:" + strings.Repeat("a", 64),
		LegacyWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + legacySA,
		RequestedAt:          metav1.NewTime(now.Add(-3 * time.Minute)), NodeReleasedAt: &releasedAt,
		IsolatedReadyAt: &isolatedReadyAt, CompletedAt: &completedAt,
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{managedprotocol.AnnotationLegacyHandoff: "node-uid"},
	}}
	r := reconcilerFor(t, device, node)
	r.APIReader = r.Client
	if err := r.ensureVKAccess(ctx, device, legacySA, false, true); err != nil {
		t.Fatal(err)
	}
	r.Client = nodeDeleteRequiresRevokedLegacyAccessClient{
		Client: r.Client, namespace: device.Namespace, serviceAccount: legacySA,
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(device)}); err != nil {
		t.Fatalf("delete completed legacy device: %v", err)
	}
	if err := r.APIReader.Get(ctx, types.NamespacedName{Name: node.Name}, &corev1.Node{}); !apierrors.IsNotFound(err) {
		t.Fatalf("legacy Node remains after deletion: %v", err)
	}
	assertWorkerAccessAbsent(t, r.APIReader.(client.Client), device, legacySA)
}

func readyLegacyPod(namespace, name string, uid types.UID, serviceAccount string, rs *appsv1.ReplicaSet, start metav1.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace, Name: name, UID: uid, Labels: rs.Spec.Template.Labels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(rs, appsv1.SchemeGroupVersion.WithKind("ReplicaSet"))},
		},
		Spec: corev1.PodSpec{ServiceAccountName: serviceAccount},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning, StartTime: &start,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func markLegacyDeploymentRolledOut(t *testing.T, kubeClient client.Client, deployment *appsv1.Deployment) {
	t.Helper()
	deployment = deployment.DeepCopy()
	deployment.Status.ObservedGeneration = deployment.Generation
	deployment.Status.Replicas = 1
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.ReadyReplicas = 1
	deployment.Status.AvailableReplicas = 1
	deployment.Status.UnavailableReplicas = 0
	if err := kubeClient.Status().Update(context.Background(), deployment); err != nil {
		t.Fatal(err)
	}
}

func publishLegacyNodeHeartbeat(
	t *testing.T,
	kubeClient client.Client,
	device *ciskov1.CiscoDevice,
	handoff *ciskov1.DeviceLegacyHandoffStatus,
	heartbeat time.Time,
) error {
	t.Helper()
	var node corev1.Node
	if err := kubeClient.Get(context.Background(), types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return err
	}
	desired, err := configprovider.GetInitialNodeSpecWithTopologyMode(
		handoff.NodeName, device.Spec.DeepCopy(), topology.ProjectionModeStandaloneCompatibility)
	if err != nil {
		return err
	}
	observed := metav1.ObjectMeta{Name: node.Name, UID: node.UID, Labels: desired.Labels}
	encoded, err := json.Marshal(observed)
	if err != nil {
		return err
	}
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[managedprotocol.VirtualKubeletLastAppliedObjectMeta] = string(encoded)
	if err := kubeClient.Update(context.Background(), &node); err != nil {
		return err
	}
	if err := kubeClient.Get(context.Background(), types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		return err
	}
	node.Status.Conditions = []corev1.NodeCondition{{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(heartbeat),
	}}
	return kubeClient.Status().Update(context.Background(), &node)
}

func assertWorkerAccessAbsent(t *testing.T, kubeClient client.Client, device *ciskov1.CiscoDevice, saName string) {
	t.Helper()
	ctx := context.Background()
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("ServiceAccount unexpectedly exists: %v", err)
	}
	if err := kubeClient.Get(ctx, types.NamespacedName{Namespace: device.Namespace, Name: saName}, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("RoleBinding unexpectedly exists: %v", err)
	}
	if err := kubeClient.Get(ctx, types.NamespacedName{Name: vkAccessClusterRoleBindingName(device.Namespace, saName)}, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("ClusterRoleBinding unexpectedly exists: %v", err)
	}
}

// completeLegacyHandoffFixture advances a Preparing fixture to the same exact
// retained Complete state produced by the full transition. It exists only to
// keep the second-cycle test focused on status replacement and re-enrollment.
func completeLegacyHandoffFixture(t *testing.T, fixture legacyHandoffFixture) {
	t.Helper()
	ctx := context.Background()
	device := fixture.device(t)
	handoff := copyLegacyHandoff(device.Status.LegacyHandoff)
	if handoff == nil {
		t.Fatal("missing Preparing handoff")
	}
	releasedAt := metav1.NewTime(fixture.clock.now.Add(time.Minute))
	isolatedReadyAt := metav1.NewTime(fixture.clock.now.Add(2 * time.Minute))
	completedAt := metav1.NewTime(fixture.clock.now.Add(3 * time.Minute))
	handoff.Phase = ciskov1.DeviceLegacyHandoffComplete
	handoff.NodeReleasedAt = &releasedAt
	handoff.IsolatedReadyAt = &isolatedReadyAt
	handoff.CompletedAt = &completedAt
	if err := fixture.r.cleanupGeneratedWorkerAccess(ctx, device, managedWorkerServiceAccountName(device), true); err != nil {
		t.Fatal(err)
	}
	if err := fixture.r.cleanupManagedWorkerLeases(ctx, device); err != nil {
		t.Fatal(err)
	}
	var node corev1.Node
	if err := fixture.client.Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &node); err != nil {
		t.Fatal(err)
	}
	before := node.DeepCopy()
	for _, key := range managedNodeBindingAnnotationKeys {
		delete(node.Annotations, key)
	}
	node.Annotations[managedprotocol.AnnotationLegacyHandoff] = handoff.NodeUID
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	node.Labels[topology.LabelType] = topology.TypeVirtualKubelet
	if err := fixture.client.Patch(ctx, &node, client.MergeFrom(before)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.r.completeLegacyHandoffStatus(ctx, device, handoff, "TestComplete"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.r.ensureVKAccess(ctx, device, fixture.r.vkServiceAccountName(), false); err != nil {
		t.Fatal(err)
	}
	if err := fixture.r.cleanupGeneratedWorkerAccess(ctx, device, topologyLegacyWorkerServiceAccountName(device), false); err != nil {
		t.Fatal(err)
	}
	if err := fixture.r.removeIsolatedLegacyWorkerMarker(ctx, device); err != nil {
		t.Fatal(err)
	}
	deployment := fixture.deployment(t)
	deployment.Spec.Template.Spec.ServiceAccountName = fixture.r.vkServiceAccountName()
	deployment.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	deployment.Spec.Template.Labels = perDeviceDeploymentLabels(device.Name)
	if err := fixture.client.Update(ctx, deployment); err != nil {
		t.Fatal(err)
	}
}

func hasTaint(taints []corev1.Taint, want corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Key == want.Key && taint.Value == want.Value && taint.Effect == want.Effect {
			return true
		}
	}
	return false
}
