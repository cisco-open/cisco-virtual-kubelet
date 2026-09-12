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
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: device.Name + deploymentSuffix,
			UID: "managed-deployment-uid", ResourceVersion: "1", Generation: 1,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(device, ciskov1.GroupVersion.WithKind("CiscoDevice"))},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: managedSA}},
		},
	}

	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	baseClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &appsv1.Deployment{}, &corev1.Node{}, &corev1.Pod{}).
		WithObjects(device, node, mutationLease, policy, ledger, deployment).Build()
	writeClient := leaseUIDAssigningClient{Client: baseClient}
	r := &CiscoDeviceReconciler{
		Client: writeClient, APIReader: baseClient, Scheme: scheme, Image: "cisco-vk:test",
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
		LeaseNamespace: device.Namespace, clock: clock,
	}
	if err := r.ensureVKAccess(context.Background(), device, managedSA, true); err != nil {
		t.Fatalf("seed managed worker access: %v", err)
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

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatalf("start handoff reconcile: %v", err)
	}
	device := fixture.device(t)
	if device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffPreparing || device.Status.NodeIdentity == nil {
		t.Fatalf("initial handoff status = %#v, nodeIdentity=%#v", device.Status.LegacyHandoff, device.Status.NodeIdentity)
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
			Namespace: device.Namespace, Name: "old-legacy", UID: "old-rs-uid",
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
	if !legacyHandoffNodeMatches(&releasedNode, handoff) || !hasTaint(releasedNode.Spec.Taints, topologyInitializationTaint()) {
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
			Namespace: device.Namespace, Name: "released-legacy", UID: "released-rs-uid",
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
		t.Fatalf("complete handoff reconcile: %v", err)
	}
	device = fixture.device(t)
	if device.Status.LegacyHandoff == nil || device.Status.LegacyHandoff.Phase != ciskov1.DeviceLegacyHandoffComplete ||
		device.Status.NodeIdentity != nil || device.Status.TopologyProjection != nil || device.Status.HealthObservation != nil ||
		device.Status.WorkerRevision != nil {
		t.Fatalf("completed status = handoff=%#v identity=%#v projection=%#v health=%#v workerRevision=%#v",
			device.Status.LegacyHandoff, device.Status.NodeIdentity, device.Status.TopologyProjection,
			device.Status.HealthObservation, device.Status.WorkerRevision)
	}
	if got := r.serviceAccountForDevice(device); got != legacySA {
		t.Fatalf("completed handoff ServiceAccount = %q, want %q", got, legacySA)
	}
	deployment = fixture.deployment(t)
	if deployment.Spec.Template.Spec.ServiceAccountName != legacySA || deployment.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Fatalf("completed Deployment SA/strategy = %q/%q", deployment.Spec.Template.Spec.ServiceAccountName, deployment.Spec.Strategy.Type)
	}
	if err := fixture.client.Get(ctx, types.NamespacedName{Name: handoff.NodeName}, &releasedNode); err != nil {
		t.Fatal(err)
	}
	if hasTaint(releasedNode.Spec.Taints, topologyInitializationTaint()) {
		t.Fatal("completed legacy Node retained topology initialization guard")
	}

	// A manager restart with the global feature flag off must retain the exact
	// UID-bound worker, not fall back to the historical release-wide identity.
	restarted := &CiscoDeviceReconciler{
		Client: fixture.r.Client, APIReader: fixture.client, Scheme: fixture.r.Scheme,
		TopologyPolicyNamespace: fixture.r.TopologyPolicyNamespace, TopologyPolicyName: fixture.r.TopologyPolicyName,
		LeaseNamespace: fixture.r.LeaseNamespace, clock: fixture.clock,
	}
	restartedDevice := fixture.device(t)
	result, err := restarted.reconcileManagedTopology(ctx, restartedDevice)
	if err != nil {
		t.Fatalf("restart completed-state recovery: %v", err)
	}
	if result.Managed || !result.LegacyWorker || restarted.serviceAccountForDevice(restartedDevice, result) != legacySA {
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
	device = fixture.device(t)
	result, err = fixture.r.reconcileManagedTopology(ctx, device)
	if err != nil {
		t.Fatalf("managed re-enrollment: %v", err)
	}
	if !result.Managed || device.Status.NodeIdentity == nil || device.Status.LegacyHandoff != nil {
		t.Fatalf("re-enrollment result/status = %+v / identity=%#v handoff=%#v", result, device.Status.NodeIdentity, device.Status.LegacyHandoff)
	}
	if err := fixture.r.ensureVKAccess(ctx, device, managedWorkerServiceAccountName(device), true); err != nil {
		t.Fatal(err)
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
		completedAt := metav1.NewTime(base.Add(2 * time.Minute))
		device.Status.LegacyHandoff = &ciskov1.DeviceLegacyHandoffStatus{
			Phase: ciskov1.DeviceLegacyHandoffComplete, DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
			ProjectionHash:       "sha256:" + strings.Repeat("a", 64),
			LegacyWorkerUsername: "system:serviceaccount:" + device.Namespace + ":" + legacySA,
			RequestedAt:          base, NodeReleasedAt: &releasedAt, CompletedAt: &completedAt,
		}
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
			Name: device.Name, UID: "node-uid", Annotations: map[string]string{managedprotocol.AnnotationLegacyHandoff: "node-uid"},
		}}
		r := reconcilerFor(t, device, node)
		var current ciskov1.CiscoDevice
		if err := r.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcileManagedTopology(ctx, &current); err == nil || !strings.Contains(err.Error(), "isolated worker access evidence") {
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
		if _, err := r.reconcileManagedTopology(ctx, &current); err == nil || !strings.Contains(err.Error(), "completed legacy handoff") {
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
	completedAt := metav1.NewTime(now.Add(-time.Minute))
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
		RequestedAt:          metav1.NewTime(now.Add(-3 * time.Minute)), NodeReleasedAt: &releasedAt, CompletedAt: &completedAt,
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
			Namespace: namespace, Name: name, UID: uid,
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
	completedAt := metav1.NewTime(fixture.clock.now.Add(2 * time.Minute))
	handoff.Phase = ciskov1.DeviceLegacyHandoffComplete
	handoff.NodeReleasedAt = &releasedAt
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
}

func hasTaint(taints []corev1.Taint, want corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Key == want.Key && taint.Value == want.Value && taint.Effect == want.Effect {
			return true
		}
	}
	return false
}
