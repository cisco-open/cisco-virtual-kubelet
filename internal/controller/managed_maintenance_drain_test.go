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
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	providermaintenance "github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

func TestReconcileManagedDrainCordonOwnsAndRestoresOnlyItsChange(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
	drain := &opsv1alpha1.UpgradeManagerDrainStatus{NodeUnschedulableBefore: false}
	active := managedMaintenanceDecision{
		drain: drain,
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionActive,
		},
	}
	if err := reconcileManagedDrainCordon(node, active); err != nil {
		t.Fatalf("apply owned cordon: %v", err)
	}
	if !node.Spec.Unschedulable || node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != token {
		t.Fatalf("owned cordon = (%t, %q), want true and session token",
			node.Spec.Unschedulable, node.Annotations[managedprotocol.AnnotationDrainCordonOwner])
	}

	recovering := active
	recovering.session = active.session.DeepCopy()
	recovering.session.Phase = ciskov1.DeviceMaintenanceSessionRecovering
	if err := reconcileManagedDrainCordon(node, recovering); err != nil {
		t.Fatalf("restore owned cordon: %v", err)
	}
	if node.Spec.Unschedulable || node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" {
		t.Fatalf("recovered cordon = (%t, %q), want false and no owner",
			node.Spec.Unschedulable, node.Annotations[managedprotocol.AnnotationDrainCordonOwner])
	}
}

func TestReconcileManagedDrainCordonPreservesOperatorState(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	tests := []struct {
		name   string
		before bool
		hold   string
	}{
		{name: "preexisting cordon", before: true},
		{name: "operator hold", hold: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
				Spec: corev1.NodeSpec{Unschedulable: test.before}}
			drain := &opsv1alpha1.UpgradeManagerDrainStatus{NodeUnschedulableBefore: test.before}
			active := managedMaintenanceDecision{drain: drain, session: &ciskov1.DeviceMaintenanceSessionStatus{
				SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionActive,
			}}
			if err := reconcileManagedDrainCordon(node, active); err != nil {
				t.Fatalf("apply cordon: %v", err)
			}
			if test.hold != "" {
				active.drainHold = true
			}
			active.session.Phase = ciskov1.DeviceMaintenanceSessionRecovering
			if err := reconcileManagedDrainCordon(node, active); err != nil {
				t.Fatalf("recover cordon: %v", err)
			}
			if !node.Spec.Unschedulable {
				t.Fatal("operator cordon/hold was removed")
			}
			if node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" {
				t.Fatal("manager retained ownership after recovery")
			}
		})
	}
}

func TestReconcileManagedDrainCordonRejectsLateUnownedCordon(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
		Spec: corev1.NodeSpec{Unschedulable: true}}
	decision := managedMaintenanceDecision{
		drain: &opsv1alpha1.UpgradeManagerDrainStatus{NodeUnschedulableBefore: false},
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: "00000000-0000-4000-8000-000000000001",
			Phase:        ciskov1.DeviceMaintenanceSessionAcknowledged,
		},
	}
	if err := reconcileManagedDrainCordon(node, decision); err == nil {
		t.Fatal("late unowned cordon was adopted")
	}
}

func TestReconcileManagedDrainTaintOwnsAndRestoresOnlyItsChange(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
	active := managedMaintenanceDecision{
		drain: &opsv1alpha1.UpgradeManagerDrainStatus{MaintenanceTaintPresentBefore: false},
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionActive,
		},
	}
	managed, err := reconcileManagedDrainTaint(node, nil, nil, active)
	if err != nil {
		t.Fatalf("apply owned maintenance taint: %v", err)
	}
	if node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != token ||
		!hasDrainMaintenanceTaint(managed) {
		t.Fatalf("taint ownership = annotations=%v managed=%v", node.Annotations, managed)
	}
	// reconcileManagedNodeMetadata applies the returned projection taints in the
	// same optimistic patch as the ownership annotation.
	node.Spec.Taints = upsertTaint(node.Spec.Taints, maintenanceGuardTaint())
	prior := map[string]struct{}{taintIdentity(maintenanceGuardTaint()): {}}
	if _, err := reconcileManagedDrainTaint(node, prior, nil, active); err != nil {
		t.Fatalf("retry exact owned maintenance taint: %v", err)
	}

	recovering := active
	recovering.session = active.session.DeepCopy()
	recovering.session.Phase = ciskov1.DeviceMaintenanceSessionRecovering
	if _, err := reconcileManagedDrainTaint(node, prior, nil, recovering); err != nil {
		t.Fatalf("restore owned maintenance taint: %v", err)
	}
	if hasDrainMaintenanceTaint(node.Spec.Taints) ||
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		t.Fatalf("recovered maintenance taint = annotations=%v taints=%v", node.Annotations, node.Spec.Taints)
	}
}

func TestReconcileManagedDrainTaintRejectsLateUnownedState(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	active := managedMaintenanceDecision{
		drain: &opsv1alpha1.UpgradeManagerDrainStatus{MaintenanceTaintPresentBefore: false},
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionActive,
		},
	}
	for name, taint := range map[string]corev1.Taint{
		"exact operator taint": maintenanceGuardTaint(),
		"identity collision": {
			Key: maintenanceGuardTaint().Key, Value: "operator", Effect: maintenanceGuardTaint().Effect,
		},
	} {
		t.Run(name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
				Spec: corev1.NodeSpec{Taints: []corev1.Taint{taint}}}
			if _, err := reconcileManagedDrainTaint(node, nil, nil, active); err == nil {
				t.Fatal("late unowned maintenance taint was adopted")
			}
			if node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" || len(node.Spec.Taints) != 1 || node.Spec.Taints[0] != taint {
				t.Fatalf("rejected operator state was mutated: annotations=%v taints=%v", node.Annotations, node.Spec.Taints)
			}
		})
	}

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
	prior := map[string]struct{}{taintIdentity(maintenanceGuardTaint()): {}}
	if _, err := reconcileManagedDrainTaint(node, prior, nil, active); err == nil {
		t.Fatal("lost session-bound owner was accepted from generic managed-taint evidence")
	}
}

func TestReconcileManagedDrainTaintPreservesUnownedRecoveryState(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	taint := maintenanceGuardTaint()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{taint}}}
	recovering := managedMaintenanceDecision{
		drain: &opsv1alpha1.UpgradeManagerDrainStatus{MaintenanceTaintPresentBefore: false},
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionRecovering,
		},
	}
	if _, err := reconcileManagedDrainTaint(node, nil, nil, recovering); err != nil {
		t.Fatalf("preserve late operator taint during recovery: %v", err)
	}
	if len(node.Spec.Taints) != 1 || node.Spec.Taints[0] != taint {
		t.Fatalf("unowned operator taint was removed during recovery: %v", node.Spec.Taints)
	}

	preexisting := recovering
	preexisting.drain = &opsv1alpha1.UpgradeManagerDrainStatus{MaintenanceTaintPresentBefore: true}
	preexisting.session = recovering.session.DeepCopy()
	preexisting.session.Phase = ciskov1.DeviceMaintenanceSessionActive
	managed, err := reconcileManagedDrainTaint(node, nil, nil, preexisting)
	if err != nil {
		t.Fatalf("preserve pre-existing operator taint: %v", err)
	}
	if hasDrainMaintenanceTaint(managed) ||
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		t.Fatalf("pre-existing operator taint was claimed: annotations=%v managed=%v", node.Annotations, managed)
	}
}

func TestReconcileManagedNodeMetadataPersistsSessionBoundDrainGuard(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	ctx := context.Background()
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "edge", Name: "switch", UID: "device-uid",
	}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "switch-node", UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationManagedTaints: "",
		},
	}}
	scheme := newTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	r := &CiscoDeviceReconciler{Client: kubeClient, APIReader: kubeClient, Scheme: scheme}
	active := managedMaintenanceDecision{
		guard: true,
		drain: &opsv1alpha1.UpgradeManagerDrainStatus{MaintenanceTaintPresentBefore: false},
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionActive,
		},
	}
	if err := r.reconcileManagedNodeMetadata(ctx, device, node, nil,
		&topologyrollout.ParsedAdminPolicy{}, "projection", active); err != nil {
		t.Fatalf("persist session-owned drain guard: %v", err)
	}
	var guarded corev1.Node
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(node), &guarded); err != nil {
		t.Fatal(err)
	}
	if !guarded.Spec.Unschedulable || !hasDrainMaintenanceTaint(guarded.Spec.Taints) ||
		guarded.Annotations[managedprotocol.AnnotationDrainCordonOwner] != token ||
		guarded.Annotations[managedprotocol.AnnotationDrainTaintOwner] != token {
		t.Fatalf("persisted drain guard = annotations=%v spec=%+v", guarded.Annotations, guarded.Spec)
	}

	recovering := active
	recovering.guard = false
	recovering.session = active.session.DeepCopy()
	recovering.session.Phase = ciskov1.DeviceMaintenanceSessionRecovering
	if err := r.reconcileManagedNodeMetadata(ctx, device, &guarded, nil,
		&topologyrollout.ParsedAdminPolicy{}, "projection", recovering); err != nil {
		t.Fatalf("restore session-owned drain guard: %v", err)
	}
	var restored corev1.Node
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(node), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Spec.Unschedulable || hasDrainMaintenanceTaint(restored.Spec.Taints) ||
		restored.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" ||
		restored.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		t.Fatalf("restored drain guard = annotations=%v spec=%+v", restored.Annotations, restored.Spec)
	}
}

func TestReconcileManagedNodeMetadataDoesNotAdoptLateMaintenanceTaint(t *testing.T) {
	const token = "00000000-0000-4000-8000-000000000001"
	ctx := context.Background()
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{
		Namespace: "edge", Name: "switch", UID: "device-uid",
	}}
	taint := maintenanceGuardTaint()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "switch-node", UID: "node-uid", Annotations: map[string]string{
			managedprotocol.AnnotationManagedTaints: "",
		},
	}, Spec: corev1.NodeSpec{Taints: []corev1.Taint{taint}}}
	scheme := newTestScheme(t)
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(node).Build()
	r := &CiscoDeviceReconciler{Client: kubeClient, APIReader: kubeClient, Scheme: scheme}
	active := managedMaintenanceDecision{
		guard: true,
		drain: &opsv1alpha1.UpgradeManagerDrainStatus{MaintenanceTaintPresentBefore: false},
		session: &ciskov1.DeviceMaintenanceSessionStatus{
			SessionToken: token, Phase: ciskov1.DeviceMaintenanceSessionActive,
		},
	}
	if err := r.reconcileManagedNodeMetadata(ctx, device, node, nil,
		&topologyrollout.ParsedAdminPolicy{}, "projection", active); err == nil {
		t.Fatal("late unowned maintenance taint was adopted by managed metadata reconciliation")
	}
	var persisted corev1.Node
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(node), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Spec.Taints) != 1 || persisted.Spec.Taints[0] != taint ||
		persisted.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		t.Fatalf("rejected operator taint changed: annotations=%v taints=%v", persisted.Annotations, persisted.Spec.Taints)
	}
}

type staleDrainRecoveryObjects struct {
	device         *ciskov1.CiscoDevice
	node           *corev1.Node
	leaf           *opsv1alpha1.IOSXESoftwareUpgrade
	lease          *coordv1.Lease
	pod            *corev1.Pod
	projectionHash string
	workerRevision string
}

func staleDrainRecoveryManagerFixture(
	t *testing.T,
	mutate func(*staleDrainRecoveryObjects),
) (*CiscoDeviceReconciler, *staleDrainRecoveryObjects) {
	t.Helper()
	const (
		token          = "00000000-0000-4000-8000-000000000001"
		planHash       = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		projectionHash = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		workerRevision = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	)
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(-2 * time.Hour)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "switch", UID: "device-uid", Generation: 3},
		Spec: ciskov1.DeviceSpec{
			NodeName: "switch-node", PhysicalIdentity: "serial-switch",
		},
	}
	workerUsername := "system:serviceaccount:edge:cisco-vk-network-management"
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "switch-node", UID: "node-uid",
			Annotations: map[string]string{
				managedprotocol.AnnotationManaged:                "true",
				managedprotocol.AnnotationDeviceNamespace:        device.Namespace,
				managedprotocol.AnnotationDeviceName:             device.Name,
				managedprotocol.AnnotationDeviceUID:              string(device.UID),
				managedprotocol.AnnotationNodeName:               "switch-node",
				managedprotocol.AnnotationNodeUID:                "node-uid",
				managedprotocol.AnnotationWorkerUsername:         "system:serviceaccount:edge:cisco-vk-app-hosting",
				managedprotocol.AnnotationAppWorkerUsername:      "system:serviceaccount:edge:cisco-vk-app-hosting",
				managedprotocol.AnnotationAppWorkerPodName:       "app-worker",
				managedprotocol.AnnotationAppWorkerPodUID:        "worker-pod-uid",
				managedprotocol.AnnotationNetworkWorkerUsername:  workerUsername,
				managedprotocol.AnnotationNetworkWorkerPodName:   "network-worker",
				managedprotocol.AnnotationNetworkWorkerPodUID:    "network-pod-uid",
				managedprotocol.AnnotationWorkerProtocol:         managedprotocol.Version,
				managedprotocol.AnnotationProjectionHash:         projectionHash,
				managedprotocol.AnnotationProjectedKeys:          "",
				managedprotocol.AnnotationManagedTaints:          "",
				managedprotocol.AnnotationWorkerObservedRevision: workerRevision,
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{MachineID: "serial-switch"},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(now)},
				{Type: corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition),
					Status: corev1.ConditionTrue, Reason: managedprotocol.ManagedWorkerReadyReason,
					LastHeartbeatTime: metav1.NewTime(now)},
			},
		},
	}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID), PhysicalIdentity: "serial-switch",
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{
		EffectiveLabelHash: projectionHash, LastSuccessfulTime: metav1.NewTime(now.Add(-time.Minute)),
	}
	device.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
		State: ciskov1.DeviceTopologyLockActive, PolicyEpoch: 2, AcquisitionID: strings.Repeat("a", 32),
		CampaignNamespace: device.Namespace, CampaignName: "campaign", CampaignUID: "campaign-uid",
		PlanHash: planHash, ReservationID: "reservation-1", DeviceUID: string(device.UID),
		DeviceGeneration: device.Generation, NodeUID: string(node.UID), ProjectionHash: projectionHash,
		AcquiredAt: metav1.NewTime(started),
	}
	device.Status.Conditions = []metav1.Condition{{
		Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionTrue,
		ObservedGeneration: device.Generation, Reason: "ProjectionComplete", LastTransitionTime: metav1.NewTime(now),
	}}
	workerPodStart := metav1.NewTime(now.Add(-time.Minute))
	workerHeartbeat := metav1.NewTime(now)
	device.Status.WorkerRevision = &ciskov1.DeviceWorkerRevisionStatus{
		DesiredRevision: workerRevision, ObservedRevision: workerRevision,
		DeploymentUID: "worker-deployment-uid", DeploymentGeneration: 1,
		PodUID: "worker-pod-uid", PodStartTime: &workerPodStart, ReadyHeartbeatTime: &workerHeartbeat,
		ObservedAt: metav1.NewTime(now),
	}
	device.Status.NetworkWorkerRevision = &ciskov1.DeviceNetworkWorkerRevisionStatus{
		DesiredRevision: workerRevision, ObservedRevision: workerRevision,
		PodUID: "network-pod-uid", PodStartTime: &workerPodStart, PodReadyTime: &workerHeartbeat,
	}

	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "upgrade", UID: "leaf-uid",
			Annotations: map[string]string{
				managedprotocol.AnnotationManaged:          "true",
				managedprotocol.AnnotationDeviceNamespace:  device.Namespace,
				managedprotocol.AnnotationDeviceName:       device.Name,
				managedprotocol.AnnotationDeviceUID:        string(device.UID),
				managedprotocol.AnnotationDeviceGeneration: "3",
				managedprotocol.AnnotationNodeName:         node.Name,
				managedprotocol.AnnotationNodeUID:          string(node.UID),
				managedprotocol.AnnotationWorkerUsername:   workerUsername,
				managedprotocol.AnnotationWorkerProtocol:   managedprotocol.Version,
				managedprotocol.AnnotationCampaignUID:      "campaign-uid",
				managedprotocol.AnnotationPlanHash:         planHash,
				managedprotocol.AnnotationLedgerUID:        "ledger-uid",
				managedprotocol.AnnotationReservationID:    "reservation-1",
			},
		},
	}
	leaf.Spec.DeviceRef.Name = device.Name
	currentRevision := int64(8)
	leaf.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
		ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
		State:           opsv1alpha1.UpgradeManagerAdmissionRevoked, RevocationReason: "CampaignCancelled",
		CampaignUID: "campaign-uid", PlanHash: planHash,
		PolicyUID: "policy-uid", PolicyResourceVersion: "41", PolicyEpoch: 2,
		LedgerUID: "ledger-uid", ReservationID: "reservation-1", TopologyLockID: strings.Repeat("a", 32),
		LeafUID: string(leaf.UID), DeviceUID: string(device.UID), DeviceGeneration: device.Generation,
		PhysicalIdentity: "serial-switch", NodeUID: string(node.UID), ControlRevision: ptr.To(currentRevision),
		UpdatedAt: metav1.NewTime(now),
	}
	leaf.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
		Revision: currentRevision, Cancel: true, UpdatedAt: metav1.NewTime(now),
	}
	leaf.Status.WorkerControl = &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState: opsv1alpha1.UpgradeManagerAdmissionRevoked,
		ObservedPolicyEpoch:    2, ObservedControlRevision: currentRevision,
		ObservedWorkerConfigRevision: workerRevision,
		EffectiveState:               opsv1alpha1.UpgradeWorkerControlCancelled,
		UpdatedAt:                    metav1.NewTime(now),
	}
	controller := metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "app-rs", UID: "controller-uid", Controller: ptr.To(true),
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "workloads", Name: "app", UID: "pod-uid",
			Labels:      map[string]string{"operations.cisco.vk/drain-safe": "true"},
			Annotations: map[string]string{managedprotocol.AnnotationDrainSession: token},
			Finalizers:  []string{managedprotocol.DrainPodFinalizer}, OwnerReferences: []metav1.OwnerReference{controller},
		},
		Spec: corev1.PodSpec{NodeName: node.Name, Containers: []corev1.Container{{Name: "app"}}},
	}
	leaf.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
		State:           opsv1alpha1.UpgradeManagerDrainRecovering, SessionToken: token,
		ReservationID: "reservation-1", PolicyEpoch: 2, ControlRevision: currentRevision, NodeUID: string(node.UID),
		StartedAt: metav1.NewTime(started), DrainDeadline: metav1.NewTime(started.Add(time.Hour)),
		RecoveryDeadline: ptr.To(metav1.NewTime(now.Add(time.Hour))), UpdatedAt: metav1.NewTime(now),
		Pods: []opsv1alpha1.UpgradeDrainPodStatus{{
			Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
			Controller: opsv1alpha1.UpgradeDrainObjectReference{
				APIVersion: controller.APIVersion, Kind: controller.Kind, Namespace: pod.Namespace,
				Name: controller.Name, UID: string(controller.UID), Generation: 1,
			},
			PDBs: []opsv1alpha1.UpgradeDrainPDBStatus{{UpgradeDrainObjectReference: opsv1alpha1.UpgradeDrainObjectReference{
				APIVersion: "policy/v1", Kind: "PodDisruptionBudget", Namespace: pod.Namespace,
				Name: "app-pdb", UID: "pdb-uid", Generation: 1,
			}, ObservedGeneration: 1, DisruptionsAllowed: 1, CurrentHealthy: 1, ExpectedPods: 1}},
			TerminationGracePeriodSeconds: 30, Phase: opsv1alpha1.UpgradeDrainPodEvictionRequested,
			EvictionRequestedAt: ptr.To(metav1.NewTime(now.Add(-time.Minute))),
		}},
	}
	eligibilityHash, err := workloaddrain.EligibilityHash(&leaf.Status.ManagerDrain.Pods[0])
	if err != nil {
		t.Fatal(err)
	}
	leaf.Status.ManagerDrain.Pods[0].EligibilityHash = eligibilityHash

	holder := devicecoordination.HolderIdentity("software-drain", leaf.Namespace, leaf.Name, string(leaf.UID))
	baseAnnotations, leaseLabels := managedMutationLeaseMetadata(
		device, node.Name, string(node.UID), workerUsername,
	)
	copyManagedWorkerBindingAnnotations(baseAnnotations, node.Annotations)
	oldRevision := int64(7)
	for annotation, value := range map[string]string{
		managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.DrainProtocolVersion,
		managedprotocol.AnnotationMaintenanceSessionToken:    token,
		managedprotocol.AnnotationMaintenanceRequestedAt:     started.UTC().Format(time.RFC3339Nano),
		managedprotocol.AnnotationMaintenanceOperationNS:     leaf.Namespace,
		managedprotocol.AnnotationMaintenanceOperationName:   leaf.Name,
		managedprotocol.AnnotationMaintenanceOperationUID:    string(leaf.UID),
		managedprotocol.AnnotationMaintenanceControlRevision: "7",
		managedprotocol.AnnotationMaintenancePurpose:         managedprotocol.MaintenancePurposeWorkloadDrain,
	} {
		baseAnnotations[annotation] = value
	}
	renewed := metav1.NewMicroTime(now.Add(-32 * time.Minute))
	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "leases",
			Name: engine.LeaseName(
				devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily,
			),
			UID: "lease-uid", Annotations: baseAnnotations, Labels: leaseLabels,
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity: &holder, AcquireTime: &renewed, RenewTime: &renewed,
			LeaseDurationSeconds: ptr.To[int32](31 * 60), LeaseTransitions: ptr.To[int32](1),
		},
	}
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase:           ciskov1.DeviceMaintenanceSessionRecovering,
		ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
		Purpose:         ciskov1.DeviceMaintenancePurposeWorkloadDrain, SessionToken: token,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID),
			}, Holder: holder,
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
		},
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		RequestedAt: metav1.NewTime(started), AcknowledgedAt: ptr.To(metav1.NewTime(started.Add(time.Second))),
		ControlRevision: oldRevision,
	}

	objects := &staleDrainRecoveryObjects{
		device: device, node: node, leaf: leaf, lease: lease, pod: pod,
		projectionHash: projectionHash, workerRevision: workerRevision,
	}
	if mutate != nil {
		mutate(objects)
	}
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(device, node, leaf).
		WithObjects(device, node, leaf, lease, pod).Build()
	r := &CiscoDeviceReconciler{
		Client: kubeClient, APIReader: kubeClient, Scheme: scheme, LeaseNamespace: lease.Namespace,
	}
	return r, objects
}

func TestStaleDrainRecoveryPreservesNodeThenRetiresExactLease(t *testing.T) {
	for _, requestMetadata := range []string{"exact", "absent-before-publication"} {
		t.Run(requestMetadata, func(t *testing.T) {
			r, objects := staleDrainRecoveryManagerFixture(t, func(objects *staleDrainRecoveryObjects) {
				if requestMetadata == "absent-before-publication" {
					for _, annotation := range []string{
						managedprotocol.AnnotationMaintenanceRequestVersion,
						managedprotocol.AnnotationMaintenanceSessionToken,
						managedprotocol.AnnotationMaintenanceRequestedAt,
						managedprotocol.AnnotationMaintenanceOperationNS,
						managedprotocol.AnnotationMaintenanceOperationName,
						managedprotocol.AnnotationMaintenanceOperationUID,
						managedprotocol.AnnotationMaintenanceControlRevision,
						managedprotocol.AnnotationMaintenancePurpose,
					} {
						delete(objects.lease.Annotations, annotation)
					}
				}
			})
			ctx := context.Background()
			var device ciskov1.CiscoDevice
			var node corev1.Node
			if err := r.Get(ctx, client.ObjectKeyFromObject(objects.device), &device); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(objects.node), &node); err != nil {
				t.Fatal(err)
			}
			decision := r.resolveManagedMaintenance(ctx, &device, &node)
			if decision.err != nil || decision.guard || decision.drain == nil || decision.session == nil ||
				decision.reason != "StaleDrainRequestRetained" || decision.status != metav1.ConditionFalse ||
				decision.session.ControlRevision != 7 || decision.drain.ControlRevision != 8 {
				t.Fatalf("stale recovery decision = %#v", decision)
			}
			if err := r.reconcileManagedNodeMetadata(ctx, &device, &node, map[string]string{},
				&topologyrollout.ParsedAdminPolicy{}, objects.projectionHash, decision); err != nil {
				t.Fatalf("preserve restored Node metadata: %v", err)
			}
			var restored corev1.Node
			if err := r.Get(ctx, client.ObjectKeyFromObject(objects.node), &restored); err != nil {
				t.Fatal(err)
			}
			if restored.Spec.Unschedulable || hasDrainMaintenanceTaint(restored.Spec.Taints) ||
				restored.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" ||
				restored.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
				t.Fatalf("stale recovery recreated a drain guard: annotations=%v taints=%v unschedulable=%t",
					restored.Annotations, restored.Spec.Taints, restored.Spec.Unschedulable)
			}
			for _, taint := range restored.Spec.Taints {
				if taint.Key == managedprotocol.TopologyInitializingTaint {
					t.Fatalf("stale recovery invoked the generic topology failure guard: %v", restored.Spec.Taints)
				}
			}

			coordinator := &providermaintenance.Coordinator{
				Client: r.Client, Namespace: device.Namespace, DeviceName: device.Name,
				DeviceUID: string(device.UID), NodeName: restored.Name,
				WorkerRevision: objects.workerRevision, WorkerPodUID: "worker-pod-uid",
				LeaseNamespace:  objects.lease.Namespace,
				ManagedTopology: true, MutationsEnabled: true,
				WorkerMode:             managedprotocol.WorkerModeAppHosting,
				ExpectedWorkerUsername: restored.Annotations[managedprotocol.AnnotationAppWorkerUsername],
				WorkerPodName:          restored.Annotations[managedprotocol.AnnotationAppWorkerPodName],
			}
			if _, finish, err := coordinator.AcquireDrainDelete(ctx, objects.pod.DeepCopy()); finish != nil || !errors.Is(err, devicecoordination.ErrMutationIncomplete) ||
				!strings.Contains(err.Error(), "retired expired stale drain Lease revision 7") {
				t.Fatalf("provider retirement = (finish=%v, err=%v)", finish != nil, err)
			}
			var retired coordv1.Lease
			if err := r.Get(ctx, client.ObjectKeyFromObject(objects.lease), &retired); err != nil {
				t.Fatal(err)
			}
			if retired.Spec.HolderIdentity != nil || retired.Spec.AcquireTime != nil ||
				retired.Spec.RenewTime != nil || retired.Spec.LeaseDurationSeconds != nil ||
				hasMaintenanceRequestAnnotations(retired.Annotations) {
				t.Fatalf("provider did not leave a request-free idle Lease: %#v", retired)
			}

			freshDecision := r.resolveManagedMaintenance(ctx, &device, &restored)
			if freshDecision.err != nil || freshDecision.session == nil ||
				freshDecision.session.Phase != ciskov1.DeviceMaintenanceSessionRecovering ||
				freshDecision.session.ControlRevision != 8 {
				t.Fatalf("idle Lease did not require a fresh recovery acknowledgement: %#v", freshDecision)
			}
		})
	}
}

func TestStaleDrainRecoveryRequiresExactOlderBindingAndRestoredGuard(t *testing.T) {
	tests := map[string]func(*staleDrainRecoveryObjects){
		"request token": func(objects *staleDrainRecoveryObjects) {
			objects.lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] =
				"00000000-0000-4000-8000-000000000002"
		},
		"request operation": func(objects *staleDrainRecoveryObjects) {
			objects.lease.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = "foreign-leaf"
		},
		"partial request": func(objects *staleDrainRecoveryObjects) {
			delete(objects.lease.Annotations, managedprotocol.AnnotationMaintenancePurpose)
		},
		"Lease UID": func(objects *staleDrainRecoveryObjects) {
			objects.device.Status.MaintenanceSession.Lease.UID = "foreign-lease"
		},
		"Lease holder": func(objects *staleDrainRecoveryObjects) {
			objects.lease.Spec.HolderIdentity = ptr.To("software-drain/foreign-leaf")
		},
		"same revision": func(objects *staleDrainRecoveryObjects) {
			objects.leaf.Status.ManagerDrain.ControlRevision = 7
			objects.leaf.Status.ManagerControl.Revision = 7
			objects.leaf.Status.ManagerAdmission.ControlRevision = ptr.To[int64](7)
		},
		"non-recovering drain": func(objects *staleDrainRecoveryObjects) {
			objects.leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainEvicting
			objects.leaf.Status.ManagerDrain.RecoveryDeadline = nil
		},
		"guard not restored": func(objects *staleDrainRecoveryObjects) {
			objects.node.Spec.Unschedulable = true
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r, objects := staleDrainRecoveryManagerFixture(t, mutate)
			var device ciskov1.CiscoDevice
			var node corev1.Node
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(objects.device), &device); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(objects.node), &node); err != nil {
				t.Fatal(err)
			}
			decision := r.resolveManagedMaintenance(context.Background(), &device, &node)
			if decision.err == nil || !decision.guard || decision.reason == "StaleDrainRequestRetained" {
				t.Fatalf("ambiguous stale recovery did not fail closed: %#v", decision)
			}
		})
	}
}
