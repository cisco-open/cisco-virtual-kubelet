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
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topology"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func TestManagedTopologyDisableRejectsPriorManagedIdentity(t *testing.T) {
	ctx := context.Background()
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "switch-01", UID: "device-uid"},
		Status: ciskov1.DeviceStatus{NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
			NodeName: "switch-01", NodeUID: "node-uid", DeviceUID: "device-uid",
		}, TopologyProjection: &ciskov1.DeviceTopologyProjectionStatus{
			EffectiveLabelHash: "sha256:" + strings.Repeat("a", 64),
		}},
	}
	scheme := newTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device).Build()
	reconciler := &CiscoDeviceReconciler{Client: kubeClient, APIReader: kubeClient, Scheme: scheme}
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}

	result, err := reconciler.reconcileManagedTopology(ctx, device)
	if err == nil || !strings.Contains(err.Error(), managedprotocol.AnnotationRequestLegacyHandoff) {
		t.Fatalf("reconcileManagedTopology() error = %v, want explicit handoff-authorization failure", err)
	}
	if !result.Managed || result.NodeName != "switch-01" {
		t.Fatalf("managed result = %+v, want retained managed ownership", result)
	}
	var persisted ciskov1.CiscoDevice
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(device), &persisted); err != nil {
		t.Fatal(err)
	}
	conflict := findCondition(persisted.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyConflict)
	if conflict == nil || conflict.Status != metav1.ConditionTrue || conflict.Reason != "LegacyHandoffAuthorizationRequired" {
		t.Fatalf("persisted topology conflict = %#v", conflict)
	}
}

func TestManagedTopologyDisabledAllowsNeverManagedDevice(t *testing.T) {
	result, err := (&CiscoDeviceReconciler{}).reconcileManagedTopology(context.Background(), &ciskov1.CiscoDevice{})
	if err != nil || result.Managed {
		t.Fatalf("reconcileManagedTopology() = (%+v, %v), want unmanaged success", result, err)
	}
}

func TestManagedWorkerReadyForProjection(t *testing.T) {
	projectionTime := metav1.NewTime(time.Unix(1_800_000_000, 0).UTC())
	projection := &ciskov1.DeviceTopologyProjectionStatus{LastSuccessfulTime: projectionTime}
	ready := corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue}
	handoff := corev1.NodeCondition{
		Type:              corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition),
		Status:            corev1.ConditionTrue,
		Reason:            managedprotocol.ManagedWorkerReadyReason,
		LastHeartbeatTime: projectionTime,
	}

	tests := []struct {
		name       string
		node       *corev1.Node
		projection *ciskov1.DeviceTopologyProjectionStatus
		want       bool
	}{
		{name: "nil node", projection: projection},
		{name: "nil projection", node: &corev1.Node{}},
		{name: "ready alone", node: &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{ready}}}, projection: projection},
		{name: "handoff without ready", node: &corev1.Node{Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{handoff}}}, projection: projection},
		{name: "wrong reason", node: nodeWithConditions(ready, mutateNodeCondition(handoff, func(c *corev1.NodeCondition) { c.Reason = "Legacy" })), projection: projection},
		{name: "false handoff", node: nodeWithConditions(ready, mutateNodeCondition(handoff, func(c *corev1.NodeCondition) { c.Status = corev1.ConditionFalse })), projection: projection},
		{name: "stale heartbeat", node: nodeWithConditions(ready, mutateNodeCondition(handoff, func(c *corev1.NodeCondition) { c.LastHeartbeatTime = metav1.NewTime(projectionTime.Add(-time.Second)) })), projection: projection},
		{name: "ready false", node: nodeWithConditions(mutateNodeCondition(ready, func(c *corev1.NodeCondition) { c.Status = corev1.ConditionFalse }), handoff), projection: projection},
		{name: "current handoff", node: nodeWithConditions(ready, handoff), projection: projection, want: true},
		{name: "new handoff", node: nodeWithConditions(ready, mutateNodeCondition(handoff, func(c *corev1.NodeCondition) { c.LastHeartbeatTime = metav1.NewTime(projectionTime.Add(time.Second)) })), projection: projection, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedWorkerReadyForProjection(tc.node, tc.projection); got != tc.want {
				t.Fatalf("managedWorkerReadyForProjection() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestManagedHealthObservationBindsNodeAndDeviceEvidence(t *testing.T) {
	observedAt := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	heartbeatAt := observedAt.Add(-time.Second)
	device := &ciskov1.CiscoDevice{Status: ciskov1.DeviceStatus{
		Phase: "Ready",
		Conditions: []metav1.Condition{{
			Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue,
			Reason: "Configured", LastTransitionTime: metav1.NewTime(observedAt.Add(-time.Minute)),
		}},
	}}
	node := nodeWithConditions(corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(heartbeatAt),
	})
	if err := refreshManagedHealthObservation(device, node, observedAt); err != nil {
		t.Fatal(err)
	}
	got, err := managedDeviceHealthObservedAt(device, node, observedAt)
	if err != nil || !got.Equal(heartbeatAt) {
		t.Fatalf("managedDeviceHealthObservedAt() = %s, %v; want %s", got, err, heartbeatAt)
	}

	device.Status.Conditions[0].Status = metav1.ConditionFalse
	if _, err := managedDeviceHealthObservedAt(device, node, observedAt); err == nil ||
		!strings.Contains(err.Error(), "conditions changed") {
		t.Fatalf("device condition drift error = %v", err)
	}
	if err := refreshManagedHealthObservation(device, node, observedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := managedDeviceHealthObservedAt(device, node, observedAt.Add(time.Second)); err != nil {
		t.Fatalf("refreshed device condition snapshot rejected: %v", err)
	}

	node.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(heartbeatAt.Add(time.Minute))
	if _, err := managedDeviceHealthObservedAt(device, node, observedAt.Add(time.Minute)); err == nil ||
		!strings.Contains(err.Error(), "heartbeat is newer") {
		t.Fatalf("unobserved Node heartbeat error = %v", err)
	}
	if err := refreshManagedHealthObservation(device, node, observedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = managedDeviceHealthObservedAt(device, node, observedAt.Add(time.Minute))
	if err != nil || !got.Equal(heartbeatAt.Add(time.Minute)) {
		t.Fatalf("refreshed combined observation = %s, %v", got, err)
	}
}

func TestManagedHealthObservationRequiresExplicitBuiltInProducer(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Generation: 3}, Status: ciskov1.DeviceStatus{
		Phase: "Ready",
		Conditions: []metav1.Condition{{
			Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue,
			ObservedGeneration: 3, Reason: "Validated", LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
		}},
	}}
	node := nodeWithConditions(corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(now),
	})
	// The manager observed a new Node heartbeat but did not run the gNOI
	// condition producer. LastTransitionTime is not producer evidence.
	if err := refreshManagedHealthObservation(device, node, now); err != nil {
		t.Fatal(err)
	}
	if _, err := managedDeviceHealthObservedAt(device, node, now,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady); err == nil ||
		!strings.Contains(err.Error(), "has no producer observation time") {
		t.Fatalf("unprobed built-in condition error = %v, want missing producer observation", err)
	}
}

func TestManagedTopologyFirstBindingRejectsPrebindingHealthForgery(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "edge", Name: "switch-01", UID: "device-uid", Generation: 7,
		},
		Spec: ciskov1.DeviceSpec{PhysicalIdentity: "SERIAL-SWITCH-01"},
		Status: ciskov1.DeviceStatus{
			Phase: "Ready",
			Conditions: []metav1.Condition{{
				Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue,
				ObservedGeneration: 7, Reason: "ForgedBeforeBinding", LastTransitionTime: metav1.NewTime(now),
			}},
		},
	}
	node := nodeWithConditions(corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		LastHeartbeatTime: metav1.NewTime(now.Add(-time.Second)),
	}, corev1.NodeCondition{
		Type: corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition), Status: corev1.ConditionTrue,
		Reason: managedprotocol.ManagedWorkerReadyReason, LastHeartbeatTime: metav1.NewTime(now),
	})
	node.Name = device.Name
	node.UID = "node-uid"
	node.Status.NodeInfo.MachineID = "serial-switch-01"
	node.Status.NodeInfo.SystemUUID = "SERIAL-SWITCH-01"
	// Model the strongest pre-binding forgery: internally consistent snapshot
	// fields and an explicit entry for a condition the manager has not probed.
	if err := refreshManagedHealthObservation(device, node, now,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady); err != nil {
		t.Fatal(err)
	}
	if device.Status.HealthObservation == nil {
		t.Fatal("test fixture did not create the forged health snapshot")
	}

	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device).Build()
	testClock := &fakeClock{now: now}
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme, clock: testClock}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}
	maintenance := managedMaintenanceDecision{status: metav1.ConditionTrue, reason: "Idle", message: "idle"}
	if err := r.patchManagedTopologyStatus(ctx, device, node, "sha256:"+strings.Repeat("a", 64), maintenance); err != nil {
		t.Fatal(err)
	}

	var current ciskov1.CiscoDevice
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.NodeIdentity == nil || current.Status.NodeIdentity.NodeUID != string(node.UID) {
		t.Fatalf("first binding identity = %#v, want Node UID %q", current.Status.NodeIdentity, node.UID)
	}
	if current.Status.HealthObservation != nil {
		t.Fatalf("first binding retained or manufactured health trust: %#v", current.Status.HealthObservation)
	}
	if _, err := managedDeviceHealthObservedAt(&current, node, now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err == nil || !strings.Contains(err.Error(), "snapshot is absent") {
		t.Fatalf("first-binding rollout health error = %v, want absent snapshot", err)
	}

	// A later topology reconciliation may observe only the topology producers.
	// The inherited gNOI condition remains unusable until its own producer runs.
	testClock.Advance(time.Second)
	if err := r.patchManagedTopologyStatus(ctx, &current, node, "sha256:"+strings.Repeat("a", 64), maintenance); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.HealthObservation == nil {
		t.Fatal("later manager reconciliation did not create a health snapshot")
	}
	if _, err := managedDeviceHealthObservedAt(&current, node, testClock.now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err == nil || !strings.Contains(err.Error(),
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady+"\" has no producer observation time") {
		t.Fatalf("unprobed inherited gNOI condition error = %v, want missing producer observation", err)
	}

	// Only the explicit gNOI producer observation closes the interleaving.
	testClock.Advance(time.Second)
	if err := r.setCiscoDeviceConditionObserved(ctx, &current, metav1.Condition{
		Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue,
		ObservedGeneration: current.Generation, Reason: "Validated",
	}); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if _, err := managedDeviceHealthObservedAt(&current, node, testClock.now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err != nil {
		t.Fatalf("explicitly produced health snapshot rejected: %v", err)
	}
}

func TestManagedPhysicalIdentityControlsTopologyReadinessAndGuard(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.September, 12, 13, 0, 0, 0, time.UTC)
	hash := "sha256:" + strings.Repeat("a", 64)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "switch-identity", UID: "device-uid", Generation: 3},
		Spec:       ciskov1.DeviceSpec{PhysicalIdentity: "SERIAL-SWITCH-01"},
		Status: ciskov1.DeviceStatus{
			NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
				DeviceUID: "device-uid", NodeName: "switch-identity", NodeUID: "node-uid",
				PhysicalIdentity: "serial-switch-01",
			},
			TopologyProjection: &ciskov1.DeviceTopologyProjectionStatus{
				EffectiveLabelHash: hash, SourceResourceVersion: "1", LastSuccessfulTime: metav1.NewTime(now),
			},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "switch-identity", UID: "node-uid",
			Annotations: map[string]string{
				managedprotocol.AnnotationManaged:         "true",
				managedprotocol.AnnotationDeviceNamespace: device.Namespace,
				managedprotocol.AnnotationDeviceName:      device.Name,
				managedprotocol.AnnotationDeviceUID:       string(device.UID),
				managedprotocol.AnnotationNodeUID:         "node-uid",
				managedprotocol.AnnotationProjectedKeys:   "",
				managedprotocol.AnnotationManagedTaints:   "",
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{MachineID: "wrong-chassis", SystemUUID: "wrong-chassis"},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(now.Add(time.Second))},
				{Type: corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition), Status: corev1.ConditionTrue,
					Reason: managedprotocol.ManagedWorkerReadyReason, LastHeartbeatTime: metav1.NewTime(now.Add(time.Second))},
			},
		},
	}
	scheme := newTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{Client: kubeClient, APIReader: kubeClient, Scheme: scheme, clock: &fakeClock{now: now.Add(2 * time.Second)}}
	policy := &topologyrollout.ParsedAdminPolicy{}

	if err := r.reconcileManagedNodeMetadata(ctx, device, node, map[string]string{}, policy, hash, false); err != nil {
		t.Fatal(err)
	}
	if err := r.patchManagedTopologyStatus(ctx, device, node, hash,
		managedMaintenanceDecision{status: metav1.ConditionTrue, reason: "Idle", message: "idle"}); err != nil {
		t.Fatal(err)
	}
	var guarded corev1.Node
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(node), &guarded); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(guarded.Spec.Taints, topologyInitializationTaint()) {
		t.Fatalf("mismatched physical identity did not retain scheduling guard: %+v", guarded.Spec.Taints)
	}
	var current ciskov1.CiscoDevice
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	ready := findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyReady)
	conflict := findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyConflict)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "PhysicalIdentityObservationMismatch" ||
		conflict == nil || conflict.Status != metav1.ConditionTrue || conflict.Reason != "PhysicalIdentityObservationMismatch" {
		t.Fatalf("mismatch conditions: ready=%#v conflict=%#v", ready, conflict)
	}

	// A later authenticated worker observation for the declared chassis is the
	// only recovery path: it clears both the conflict and the NoSchedule guard.
	guarded.Status.NodeInfo.MachineID = "serial-switch-01"
	guarded.Status.NodeInfo.SystemUUID = "SERIAL-SWITCH-01"
	if err := kubeClient.Status().Update(ctx, &guarded); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(node), &guarded); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileManagedNodeMetadata(ctx, &current, &guarded, map[string]string{}, policy, hash, false); err != nil {
		t.Fatal(err)
	}
	if err := r.patchManagedTopologyStatus(ctx, &current, &guarded, hash,
		managedMaintenanceDecision{status: metav1.ConditionTrue, reason: "Idle", message: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(node), &guarded); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(guarded.Spec.Taints, topologyInitializationTaint()) {
		t.Fatalf("matching recovered identity retained scheduling guard: %+v", guarded.Spec.Taints)
	}
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	ready = findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyReady)
	conflict = findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyConflict)
	if ready == nil || ready.Status != metav1.ConditionTrue || conflict == nil || conflict.Status != metav1.ConditionFalse {
		t.Fatalf("recovered conditions: ready=%#v conflict=%#v", ready, conflict)
	}
}

func TestManagedHealthObservationRefreshesOnlyExplicitlyEvaluatedCondition(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	conditionType := ciskov1.CiscoDeviceConditionGNOIConfigurationReady
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Generation: 4}, Status: ciskov1.DeviceStatus{
		Phase: "Ready",
		Conditions: []metav1.Condition{{
			Type: conditionType, Status: metav1.ConditionTrue, ObservedGeneration: 4,
			Reason: "Validated", LastTransitionTime: metav1.NewTime(now.Add(-time.Hour)),
		}},
	}}
	node := nodeWithConditions(corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(now),
	})

	if err := refreshManagedHealthObservation(device, node, now, conditionType); err != nil {
		t.Fatal(err)
	}
	observations := device.Status.HealthObservation.ConditionObservations
	if len(observations) != 1 || observations[0].Type != conditionType ||
		!observations[0].ObservedAt.Time.Equal(now) {
		t.Fatalf("initial producer observations = %+v, want one %s observation at %s", observations, conditionType, now)
	}

	// Merely re-reading the condition inside the bounded reconciliation interval
	// does not manufacture a newer producer timestamp.
	if err := refreshManagedHealthObservation(device, node, now.Add(managedConditionProbeInterval/2), conditionType); err != nil {
		t.Fatal(err)
	}
	if got := device.Status.HealthObservation.ConditionObservations[0].ObservedAt.Time; !got.Equal(now) {
		t.Fatalf("producer observation advanced without a due evaluation: got %s, want %s", got, now)
	}

	due := now.Add(managedConditionProbeInterval)
	if err := refreshManagedHealthObservation(device, node, due, conditionType); err != nil {
		t.Fatal(err)
	}
	if got := device.Status.HealthObservation.ConditionObservations[0].ObservedAt.Time; !got.Equal(due) {
		t.Fatalf("due producer observation = %s, want %s", got, due)
	}
}

func nodeWithConditions(conditions ...corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{Status: corev1.NodeStatus{Conditions: conditions}}
}

func mutateNodeCondition(condition corev1.NodeCondition, mutate func(*corev1.NodeCondition)) corev1.NodeCondition {
	mutate(&condition)
	return condition
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func TestManagedWorkerServiceAccountNameBindsCiscoDeviceUID(t *testing.T) {
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "edge",
		Name:      strings.Repeat("long-node-", 6) + "end",
		UID:       types.UID("device-uid-old"),
	}}
	firstCopy := device.DeepCopy()
	recreated := device.DeepCopy()
	recreated.UID = types.UID("device-uid-new")

	first := managedWorkerServiceAccountName(device)
	if !strings.HasPrefix(first, "cisco-vk-managed-"+device.Name+"-") {
		t.Fatalf("managed worker ServiceAccount %q does not encode Node name %q", first, device.Name)
	}
	if got := managedWorkerServiceAccountName(firstCopy); got != first {
		t.Fatalf("same CiscoDevice incarnation produced %q then %q", first, got)
	}
	if got := managedWorkerServiceAccountName(recreated); got == first {
		t.Fatalf("recreated CiscoDevice reused UID-bound ServiceAccount name %q", got)
	}
	if len(first) > 253 {
		t.Fatalf("ServiceAccount name length=%d, want <=253: %q", len(first), first)
	}
	if problems := validation.IsDNS1123Subdomain(first); len(problems) != 0 {
		t.Fatalf("ServiceAccount name %q is invalid: %v", first, problems)
	}
	device.Spec.NodeName = "reserved-node"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: "bound-node", NodeUID: "node-uid",
	}
	if got := managedWorkerServiceAccountName(device); !strings.HasPrefix(got, "cisco-vk-managed-bound-node-") {
		t.Fatalf("durable status Node binding was not authoritative in ServiceAccount name: %q", got)
	}
}

func TestTopologyWorkerIdentityRejectsOmittedLongNodeNameBeforeReservation(t *testing.T) {
	device := newDevice(strings.Repeat("segment-", 10)+"node", "edge")
	device.UID = "device-uid"
	if len(device.Name) <= 63 {
		t.Fatal("test fixture must exceed the topology hostname-label boundary")
	}
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device).Build()
	r := &CiscoDeviceReconciler{Client: apiClient, APIReader: apiClient, Scheme: scheme, ManagedTopology: true}
	err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), device)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.reconcileManagedTopology(context.Background(), device); err == nil ||
		!strings.Contains(err.Error(), "explicit spec.nodeName") {
		t.Fatalf("long inherited Node identity error=%v", err)
	}
	var nodes corev1.NodeList
	if err := apiClient.List(context.Background(), &nodes); err != nil {
		t.Fatal(err)
	}
	if len(nodes.Items) != 0 || device.Status.NodeIdentity != nil {
		t.Fatalf("invalid topology identity reserved state: nodes=%d identity=%#v", len(nodes.Items), device.Status.NodeIdentity)
	}
}

func TestTopologyWorkerServiceAccountNamesStayExactAndValid(t *testing.T) {
	for _, nodeName := range []string{strings.Repeat("n", 63), "site-a.switch-01"} {
		device := newDevice(nodeName, "edge")
		device.UID = "device-uid"
		if err := validateTopologyWorkerNodeName(device); err != nil {
			t.Fatalf("valid Node name %q was rejected: %v", nodeName, err)
		}
		for _, name := range []string{managedWorkerServiceAccountName(device), topologyLegacyWorkerServiceAccountName(device)} {
			if len(name) > 253 {
				t.Fatalf("ServiceAccount name length=%d, want <=253: %q", len(name), name)
			}
			if problems := validation.IsDNS1123Subdomain(name); len(problems) != 0 {
				t.Fatalf("ServiceAccount name %q is invalid: %v", name, problems)
			}
			if !strings.Contains(name, nodeName) {
				t.Fatalf("ServiceAccount %q does not retain exact Node name %q", name, nodeName)
			}
		}
	}
}

func TestManagedPolicyUnavailableReappliesBoundNodeGuard(t *testing.T) {
	device := newDevice("switch-policy-loss", "edge")
	device.UID = "device-uid"
	device.Spec.PhysicalIdentity = "serial-policy-loss"
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
		PhysicalIdentity: "serial-policy-loss",
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
		},
	}}
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithObjects(device, node).Build()
	r := &CiscoDeviceReconciler{
		Client: c, APIReader: c, Scheme: scheme, ManagedTopology: true,
		TopologyPolicyNamespace: "cvk-system", TopologyPolicyName: "missing-policy",
	}
	if _, err := r.reconcileManagedTopology(context.Background(), device); err == nil ||
		!strings.Contains(err.Error(), "TopologyPolicyUnavailable") {
		t.Fatalf("policy-loss reconcile error = %v", err)
	}
	var guarded corev1.Node
	if err := c.Get(context.Background(), types.NamespacedName{Name: node.Name}, &guarded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, taint := range guarded.Spec.Taints {
		if taint == topologyInitializationTaint() {
			found = true
		}
	}
	if !found {
		t.Fatal("managed Node remained schedulable after policy loss")
	}
}

func TestManagedTopologyRejectsInvalidMaxPodsBeforeNodeReservation(t *testing.T) {
	for _, value := range []int32{0, 111} {
		t.Run(fmt.Sprintf("maxPods-%d", value), func(t *testing.T) {
			device := newDevice("switch-capacity", "edge")
			device.UID = "device-uid"
			device.Spec.MaxPods = value
			device.Spec.PhysicalIdentity = "serial-switch-capacity"
			device.Labels = map[string]string{
				managedprotocol.AnnotationManaged:          "true",
				topology.CiscoTopologyLabelPrefix + "site": "site-a",
			}
			policy, ledger := managedPolicyAndLedger(t, nil)
			scheme := newTestScheme(t)
			apiClient := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&ciskov1.CiscoDevice{}).
				WithObjects(device, policy, ledger).Build()
			r := &CiscoDeviceReconciler{
				Client: apiClient, APIReader: apiClient, Scheme: scheme, ManagedTopology: true,
				TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
			}
			if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), device); err != nil {
				t.Fatal(err)
			}
			result, err := r.reconcileManagedTopology(context.Background(), device)
			if err == nil || !strings.Contains(err.Error(), "spec.maxPods between 1 and 110") {
				t.Fatalf("maxPods=%d reconcile result/error = %+v, %v", value, result, err)
			}
			var nodes corev1.NodeList
			if err := apiClient.List(context.Background(), &nodes); err != nil {
				t.Fatal(err)
			}
			if len(nodes.Items) != 0 {
				t.Fatalf("maxPods=%d reserved %d Nodes", value, len(nodes.Items))
			}
			var current ciskov1.CiscoDevice
			if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
				t.Fatal(err)
			}
			condition := findCondition(current.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyIncomplete)
			if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "ManagedCapacityInvalid" {
				t.Fatalf("maxPods=%d topology condition = %#v", value, condition)
			}
		})
	}
}

func TestManagedReclassificationLockRetainsOldProjectionAndGuard(t *testing.T) {
	const siteKey = "topology.cisco.vk/site"
	device := newDevice("switch-locked", "edge")
	device.UID = "device-uid"
	device.Spec.PhysicalIdentity = "serial-switch-locked"
	device.Generation = 7
	device.Labels = map[string]string{managedprotocol.AnnotationManaged: "true", siteKey: "site-b"}
	oldHash := "sha256:" + strings.Repeat("a", 64)
	device.Annotations = map[string]string{managedprotocol.AnnotationReclassifyFrom: oldHash}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: device.Name, NodeUID: "node-uid",
		PhysicalIdentity: "serial-switch-locked",
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{
		EffectiveLabelHash: oldHash, SourceResourceVersion: "1", LastSuccessfulTime: metav1.Now(),
	}
	device.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
		CampaignNamespace: "edge", CampaignName: "campaign", CampaignUID: "campaign-uid",
		PlanHash: "sha256:" + strings.Repeat("b", 64), ReservationID: "reservation-1",
		DeviceUID: string(device.UID), DeviceGeneration: device.Generation, NodeUID: "node-uid",
		ProjectionHash: oldHash, AcquiredAt: metav1.Now(),
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: device.Name, UID: "node-uid", Labels: map[string]string{siteKey: "site-a"},
		Annotations: map[string]string{
			managedprotocol.AnnotationManaged:         "true",
			managedprotocol.AnnotationDeviceNamespace: device.Namespace,
			managedprotocol.AnnotationDeviceName:      device.Name,
			managedprotocol.AnnotationDeviceUID:       string(device.UID),
			managedprotocol.AnnotationNodeUID:         "node-uid",
		},
	}}
	policy, ledger := managedPolicyAndLedger(t, nil)
	scheme := newTestScheme(t)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&ciskov1.CiscoDevice{}, ciscoDevicePhysicalIdentityIndex, physicalIdentityIndexValues).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &corev1.Node{}).
		WithObjects(device, node, policy, ledger).Build()
	r := &CiscoDeviceReconciler{
		Client: apiClient, APIReader: apiClient, Scheme: scheme, ManagedTopology: true,
		TopologyPolicyNamespace: policy.Namespace, TopologyPolicyName: policy.Name,
	}
	if _, err := r.reconcileManagedTopology(context.Background(), device); err == nil ||
		!strings.Contains(err.Error(), "ReclassificationLocked") {
		t.Fatalf("locked reclassification error=%v", err)
	}
	var current corev1.Node
	if err := apiClient.Get(context.Background(), types.NamespacedName{Name: node.Name}, &current); err != nil {
		t.Fatal(err)
	}
	if current.Labels[siteKey] != "site-a" {
		t.Fatalf("locked projection changed to %q", current.Labels[siteKey])
	}
	foundGuard := false
	for _, taint := range current.Spec.Taints {
		foundGuard = foundGuard || taint == topologyInitializationTaint()
	}
	if !foundGuard {
		t.Fatal("locked reclassification did not reapply initialization guard")
	}
}

func TestManagedProjectionLabelsPreservesOnlyCompatibleLegacyLabels(t *testing.T) {
	policy := &topologyrollout.ParsedAdminPolicy{Config: topologyrollout.AdminPolicyConfig{
		RequiredTopologyKeys:  []string{corev1.LabelTopologyRegion, corev1.LabelTopologyZone},
		ProjectedTopologyKeys: []string{corev1.LabelTopologyRegion, corev1.LabelTopologyZone},
	}}
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			corev1.LabelTopologyRegion: "eu-central",
			corev1.LabelTopologyZone:   "berlin-1",
		}},
		Spec: ciskov1.DeviceSpec{
			Region: "eu-central",
			Zone:   "berlin-1",
			Labels: map[string]string{"workload": "edge", corev1.LabelTopologyZone: "berlin-1"},
		},
	}

	got, err := managedProjectionLabels(device, policy)
	if err != nil {
		t.Fatalf("managedProjectionLabels() error = %v", err)
	}
	if got["workload"] != "edge" || got[corev1.LabelTopologyRegion] != "eu-central" || got[corev1.LabelTopologyZone] != "berlin-1" {
		t.Fatalf("projection lost compatible labels: %v", got)
	}

	device.Spec.Zone = "legacy-zone"
	if _, err := managedProjectionLabels(device, policy); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflicting legacy zone error = %v", err)
	}

	device.Spec.Zone = "berlin-1"
	device.Spec.Labels[topology.CiscoTopologyLabelPrefix+"rack"] = "rack-7"
	if _, err := managedProjectionLabels(device, policy); err == nil || !strings.Contains(err.Error(), "projectedTopologyKeys") {
		t.Fatalf("unapproved legacy topology error = %v", err)
	}
}

func TestSanitizeVirtualKubeletLastAppliedMetadataRemovesLegacyOwnership(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "edge-01", UID: types.UID("node-uid"),
		Annotations: map[string]string{
			managedprotocol.VirtualKubeletLastAppliedObjectMeta: `{"name":"edge-01","uid":"node-uid","labels":{"topology.kubernetes.io/zone":"legacy"},"annotations":{"cisco.io/hostname":"legacy"}}`,
			managedprotocol.VirtualKubeletLastAppliedNodeStatus: `{"phase":"Running"}`,
		},
	}}

	if err := sanitizeVirtualKubeletLastAppliedMetadata(node); err != nil {
		t.Fatalf("sanitizeVirtualKubeletLastAppliedMetadata() error = %v", err)
	}
	var got metav1.ObjectMeta
	if err := json.Unmarshal([]byte(node.Annotations[managedprotocol.VirtualKubeletLastAppliedObjectMeta]), &got); err != nil {
		t.Fatalf("sanitized annotation is invalid JSON: %v", err)
	}
	if got.Name != node.Name || got.UID != node.UID || len(got.Labels) != 0 || len(got.Annotations) != 0 {
		t.Fatalf("unsafe sanitized metadata: %#v", got)
	}
	if node.Annotations[managedprotocol.VirtualKubeletLastAppliedNodeStatus] != `{"phase":"Running"}` {
		t.Fatal("status bookkeeping annotation was modified")
	}
}
