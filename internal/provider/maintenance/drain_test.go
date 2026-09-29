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

package maintenance

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

const (
	drainSessionToken = "00000000-0000-4000-8000-000000000001"
	drainWorkerHash   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	drainPlanHash     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type drainFixtureObjects struct {
	device *ciskov1.CiscoDevice
	node   *corev1.Node
	leaf   *opsv1alpha1.IOSXESoftwareUpgrade
	lease  *coordv1.Lease
	pod    *corev1.Pod
}

type drainPreDispatchFailureClient struct {
	client.Client
	failPublish                    bool
	invalidateAfterPublish         bool
	advanceDeviceCleanAfterPublish bool
}

type drainReleaseFailureClient struct {
	client.Client
	failures int
}

type drainPodReadFailureClient struct{ client.Client }

type drainPodSecondReadNotFoundClient struct {
	client.Client
	podReads int
}

func (c *drainPodSecondReadNotFoundClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if _, ok := obj.(*corev1.Pod); ok {
		c.podReads++
		if c.podReads == 2 {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, key.Name)
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c drainPodReadFailureClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	if _, ok := obj.(*corev1.Pod); ok {
		return errors.New("injected live Pod read failure")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *drainReleaseFailureClient) Update(
	ctx context.Context,
	obj client.Object,
	opts ...client.UpdateOption,
) error {
	if lease, ok := obj.(*coordv1.Lease); ok && lease.Spec.HolderIdentity == nil && c.failures > 0 {
		c.failures--
		return errors.New("injected retained Lease release failure")
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *drainPreDispatchFailureClient) Patch(
	ctx context.Context,
	obj client.Object,
	patch client.Patch,
	opts ...client.PatchOption,
) error {
	lease, isLease := obj.(*coordv1.Lease)
	if !isLease || lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] == "" {
		return c.Client.Patch(ctx, obj, patch, opts...)
	}
	if c.failPublish {
		return errors.New("injected drain request publication failure")
	}
	if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	if c.invalidateAfterPublish {
		var node corev1.Node
		if err := c.Client.Get(ctx, types.NamespacedName{Name: "switch-node"}, &node); err != nil {
			return err
		}
		node.Annotations[managedprotocol.AnnotationProjectionHash] = "changed-after-acquire"
		return c.Client.Update(ctx, &node)
	}
	if c.advanceDeviceCleanAfterPublish {
		c.advanceDeviceCleanAfterPublish = false
		var leaf opsv1alpha1.IOSXESoftwareUpgrade
		if err := c.Client.Get(ctx, types.NamespacedName{Namespace: "edge", Name: "upgrade"}, &leaf); err != nil {
			return err
		}
		for i := range leaf.Status.ManagerDrain.Pods {
			pod := &leaf.Status.ManagerDrain.Pods[i]
			if pod.UID != "pod-uid" {
				continue
			}
			now := metav1.Now()
			pod.Phase = opsv1alpha1.UpgradeDrainPodDeviceClean
			pod.DeviceCleanAt = &now
			pod.DeviceCleanInventoryRevision = 1
		}
		return c.Client.Status().Update(ctx, &leaf)
	}
	return nil
}

func drainCoordinatorFixture(
	t *testing.T,
	mutate func(*drainFixtureObjects),
) (*Coordinator, *drainFixtureObjects) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "switch", UID: "device-uid", Generation: 3},
		Spec:       ciskov1.DeviceSpec{PhysicalIdentity: "serial-switch"},
	}
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
				managedprotocol.AnnotationNetworkWorkerUsername:  "system:serviceaccount:edge:cisco-vk-network-management",
				managedprotocol.AnnotationNetworkWorkerPodName:   "network-worker",
				managedprotocol.AnnotationNetworkWorkerPodUID:    "network-pod-uid",
				managedprotocol.AnnotationWorkerProtocol:         managedprotocol.Version,
				managedprotocol.AnnotationProjectionHash:         "projection-hash",
				managedprotocol.AnnotationWorkerObservedRevision: drainWorkerHash,
				managedprotocol.AnnotationDrainCordonOwner:       drainSessionToken,
				managedprotocol.AnnotationDrainTaintOwner:        drainSessionToken,
			},
		},
		Spec: corev1.NodeSpec{Unschedulable: true, Taints: []corev1.Taint{{
			Key: TaintKey, Value: TaintValue, Effect: corev1.TaintEffectNoSchedule,
		}}},
	}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		PhysicalIdentity: "serial-switch",
	}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{EffectiveLabelHash: "projection-hash"}
	device.Status.TopologyLock = &ciskov1.DeviceTopologyLockStatus{
		State: ciskov1.DeviceTopologyLockActive, PolicyEpoch: 2, AcquisitionID: strings.Repeat("c", 32),
		CampaignNamespace: device.Namespace, CampaignName: "campaign", CampaignUID: "campaign-uid",
		PlanHash: drainPlanHash, ReservationID: "reservation-1", DeviceUID: string(device.UID),
		DeviceGeneration: device.Generation, NodeUID: string(node.UID), ProjectionHash: "projection-hash",
		AcquiredAt: metav1.NewTime(now),
	}
	device.Status.Conditions = []metav1.Condition{{
		Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionTrue,
		ObservedGeneration: device.Generation, Reason: "Ready", LastTransitionTime: metav1.NewTime(now),
	}}
	podStart := metav1.NewTime(now.Add(-time.Minute))
	readyHeartbeat := metav1.NewTime(now)
	device.Status.WorkerRevision = &ciskov1.DeviceWorkerRevisionStatus{
		DesiredRevision: drainWorkerHash, ObservedRevision: drainWorkerHash,
		DeploymentUID: "worker-deployment-uid", DeploymentGeneration: 1,
		PodUID: "worker-pod-uid", PodStartTime: &podStart, ReadyHeartbeatTime: &readyHeartbeat,
		ObservedAt: metav1.NewTime(now),
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
				managedprotocol.AnnotationWorkerUsername:   node.Annotations[managedprotocol.AnnotationWorkerUsername],
				managedprotocol.AnnotationWorkerProtocol:   managedprotocol.Version,
				managedprotocol.AnnotationCampaignUID:      "campaign-uid",
				managedprotocol.AnnotationPlanHash:         drainPlanHash,
				managedprotocol.AnnotationLedgerUID:        "ledger-uid",
				managedprotocol.AnnotationReservationID:    "reservation-1",
			},
		},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{DeviceRef: configv1alpha1.DeviceRef{Name: device.Name}},
	}
	revision := int64(7)
	leaf.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
		ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1,
		State:           opsv1alpha1.UpgradeManagerAdmissionPending,
		CampaignUID:     "campaign-uid", PlanHash: drainPlanHash,
		PolicyUID: "policy-uid", PolicyResourceVersion: "41", PolicyEpoch: 2,
		LedgerUID: "ledger-uid", ReservationID: "reservation-1", TopologyLockID: strings.Repeat("c", 32),
		LeafUID: string(leaf.UID), DeviceUID: string(device.UID), DeviceGeneration: device.Generation,
		PhysicalIdentity: "serial-switch", NodeUID: string(node.UID), ControlRevision: &revision,
		UpdatedAt: metav1.NewTime(now),
	}
	leaf.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
		Revision: revision, UpdatedAt: metav1.NewTime(now), Reason: "CampaignControl",
	}
	leaf.Status.WorkerControl = &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState: opsv1alpha1.UpgradeManagerAdmissionPending,
		ObservedPolicyEpoch:    2, ObservedControlRevision: revision,
		ObservedWorkerConfigRevision: drainWorkerHash,
		EffectiveState:               opsv1alpha1.UpgradeWorkerControlReady,
		UpdatedAt:                    metav1.NewTime(now),
	}
	controller := metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "app-rs", UID: "controller-uid", Controller: ptr.To(true),
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "workloads", Name: "app", UID: "pod-uid",
			Labels:      map[string]string{"operations.cisco.vk/drain-safe": "true"},
			Annotations: map[string]string{managedprotocol.AnnotationDrainSession: drainSessionToken},
			Finalizers:  []string{managedprotocol.DrainPodFinalizer}, OwnerReferences: []metav1.OwnerReference{controller},
		},
		Spec: corev1.PodSpec{NodeName: node.Name, Containers: []corev1.Container{{Name: "app"}}},
	}
	leaf.Status.ManagerDrain = &opsv1alpha1.UpgradeManagerDrainStatus{
		ProtocolVersion: opsv1alpha1.ManagedDrainProtocolPDBV1,
		State:           opsv1alpha1.UpgradeManagerDrainEvicting,
		SessionToken:    drainSessionToken,
		ReservationID:   "reservation-1", PolicyEpoch: 2, ControlRevision: revision, NodeUID: string(node.UID),
		StartedAt: metav1.NewTime(now), DrainDeadline: metav1.NewTime(now.Add(time.Hour)), UpdatedAt: metav1.NewTime(now),
		Pods: []opsv1alpha1.UpgradeDrainPodStatus{{
			Namespace: pod.Namespace, Name: pod.Name, UID: string(pod.UID),
			Controller: opsv1alpha1.UpgradeDrainObjectReference{
				APIVersion: controller.APIVersion, Kind: controller.Kind, Namespace: pod.Namespace,
				Name: controller.Name, UID: string(controller.UID), Generation: 1,
			},
			PDBs: []opsv1alpha1.UpgradeDrainPDBStatus{{
				UpgradeDrainObjectReference: opsv1alpha1.UpgradeDrainObjectReference{
					APIVersion: "policy/v1", Kind: "PodDisruptionBudget", Namespace: pod.Namespace,
					Name: "app-pdb", UID: "pdb-uid", Generation: 1,
				},
				ObservedGeneration: 1, DisruptionsAllowed: 1, CurrentHealthy: 1, ExpectedPods: 1,
			}},
			TerminationGracePeriodSeconds: 30, Phase: opsv1alpha1.UpgradeDrainPodEvictionRequested,
			EvictionRequestedAt: ptr.To(metav1.NewTime(now)),
		}},
	}
	eligibilityHash, err := workloaddrain.EligibilityHash(&leaf.Status.ManagerDrain.Pods[0])
	if err != nil {
		t.Fatal(err)
	}
	leaf.Status.ManagerDrain.Pods[0].EligibilityHash = eligibilityHash
	holder := devicecoordination.HolderIdentity("software-drain", leaf.Namespace, leaf.Name, string(leaf.UID))
	annotations := map[string]string{devicecoordination.RetainLeaseAnnotation: "true"}
	for key, value := range node.Annotations {
		annotations[key] = value
	}
	annotations[managedprotocol.AnnotationLeasePurpose] = managedprotocol.LeasePurposeDeviceMutation
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: "leases",
		Name: engine.LeaseName(
			devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily,
		),
		UID: "lease-uid", Annotations: annotations,
		Labels: map[string]string{
			"cisco.vk/device": devicecoordination.DeviceKey(device.Namespace, device.Name),
			"cisco.vk/family": devicecoordination.MutationLeaseFamily,
		},
	}}
	lease.Annotations[managedprotocol.AnnotationWorkerUsername] = node.Annotations[managedprotocol.AnnotationNetworkWorkerUsername]
	for _, key := range []string{managedprotocol.AnnotationAppWorkerUsername, managedprotocol.AnnotationAppWorkerPodName, managedprotocol.AnnotationAppWorkerPodUID, managedprotocol.AnnotationNetworkWorkerUsername, managedprotocol.AnnotationNetworkWorkerPodName, managedprotocol.AnnotationNetworkWorkerPodUID} {
		lease.Annotations[key] = node.Annotations[key]
	}
	device.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{
		Phase:           ciskov1.DeviceMaintenanceSessionActive,
		ProtocolVersion: ciskov1.DeviceMaintenanceProtocolPDBDrainV1,
		Purpose:         ciskov1.DeviceMaintenancePurposeWorkloadDrain,
		SessionToken:    drainSessionToken,
		Lease: ciskov1.DeviceMaintenanceLeaseReference{
			DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
				Namespace: lease.Namespace, Name: lease.Name, UID: string(lease.UID),
			},
			Holder: holder,
		},
		Operation: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: leaf.Namespace, Name: leaf.Name, UID: string(leaf.UID),
		},
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID),
		RequestedAt: metav1.NewTime(now), AcknowledgedAt: ptr.To(metav1.NewTime(now)),
		ControlRevision: revision,
	}
	objects := &drainFixtureObjects{device: device, node: node, leaf: leaf, lease: lease, pod: pod}
	if mutate != nil {
		mutate(objects)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		ciskov1.AddToScheme, opsv1alpha1.AddToScheme, corev1.AddToScheme, coordv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(device, leaf).WithObjects(device, node, leaf, lease, pod).Build()
	coordinator := &Coordinator{
		Client: kubeClient, Namespace: device.Namespace, DeviceName: device.Name, DeviceUID: string(device.UID),
		NodeName: node.Name, WorkerRevision: drainWorkerHash, WorkerPodUID: "worker-pod-uid", LeaseNamespace: lease.Namespace,
		ManagedTopology: true, MutationsEnabled: true,
		WorkerMode:             managedprotocol.WorkerModeAppHosting,
		ExpectedWorkerUsername: node.Annotations[managedprotocol.AnnotationAppWorkerUsername],
		WorkerPodName:          node.Annotations[managedprotocol.AnnotationAppWorkerPodName],
	}
	return coordinator, objects
}

func releasedDrainCompletionFixture(
	t *testing.T,
	phase opsv1alpha1.UpgradeDrainPodPhase,
	mutate func(*drainFixtureObjects),
) (*Coordinator, *drainFixtureObjects) {
	t.Helper()
	return drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		base := o.leaf.Status.ManagerDrain.StartedAt.Time
		protected := metav1.NewTime(base)
		evicted := metav1.NewTime(base.Add(time.Second))
		observed := metav1.NewTime(base.Add(2 * time.Second))
		clean := metav1.NewTime(base.Add(3 * time.Second))
		released := metav1.NewTime(base.Add(4 * time.Second))
		selected := &o.leaf.Status.ManagerDrain.Pods[0]
		selected.Phase = phase
		selected.ProtectedAt = &protected
		selected.EvictionRequestedAt = &evicted
		selected.DeletionObservedAt = &observed
		selected.DeletionObservedInventoryRevision = 3
		selected.DeviceCleanAt = &clean
		selected.DeviceCleanInventoryRevision = 4
		if phase == opsv1alpha1.UpgradeDrainPodReleased {
			selected.ReleasedAt = &released
		}
		o.pod.DeletionTimestamp = &observed
		delete(o.pod.Annotations, managedprotocol.AnnotationDrainSession)
		// The fake client rejects seeding a deleting object without any
		// finalizer. This unrelated finalizer is not part of the drain protocol.
		o.pod.Finalizers = []string{"example.com/fake-retain"}
		if mutate != nil {
			mutate(o)
		}
	})
}

func TestAcquireDrainDeleteOwnsAndReleasesExactLease(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	if deleteCtx.Err() != nil {
		t.Fatalf("authorized context is already cancelled: %v", deleteCtx.Err())
	}
	var held coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	wantHolder := "software-drain/" + string(objects.leaf.UID)
	if held.Spec.HolderIdentity == nil || *held.Spec.HolderIdentity != wantHolder ||
		held.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] != drainSessionToken ||
		held.Annotations[managedprotocol.AnnotationMaintenancePurpose] != managedprotocol.MaintenancePurposeWorkloadDrain {
		t.Fatalf("drain Lease was not acquired with exact request binding: %#v", held)
	}
	finish(nil)
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.HolderIdentity != nil || held.Spec.AcquireTime != nil || held.Spec.RenewTime != nil ||
		held.Spec.LeaseDurationSeconds != nil {
		t.Fatalf("successful drain delete retained active Lease state: %#v", held.Spec)
	}
	for _, key := range []string{
		managedprotocol.AnnotationMaintenanceRequestVersion,
		managedprotocol.AnnotationMaintenanceSessionToken,
		managedprotocol.AnnotationMaintenancePurpose,
	} {
		if _, exists := held.Annotations[key]; exists {
			t.Fatalf("successful drain delete retained request annotation %s", key)
		}
	}
}

func TestAcquireDrainDeleteRetainsLeaseOnUncertainOutcome(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	_, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	finish(errors.New("device response lost"))
	var held coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.HolderIdentity == nil || *held.Spec.HolderIdentity != "software-drain/leaf-uid" ||
		held.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] != drainSessionToken {
		t.Fatal("uncertain drain delete released its quarantine")
	}
}

func TestAcquireDrainDeleteReportsReleaseFailureAndCanRetry(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	c.Client = &drainReleaseFailureClient{Client: c.Client, failures: 1}
	_, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err == nil || !strings.Contains(err.Error(), "release managed drain Lease") {
		t.Fatalf("finish error = %v, want observable Lease release failure", err)
	}
	var held coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.HolderIdentity == nil || *held.Spec.HolderIdentity != "software-drain/leaf-uid" {
		t.Fatalf("failed release lost its exact holder: %#v", held.Spec)
	}

	_, retryFinish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatalf("same-holder cleanup retry failed: %v", err)
	}
	if err := retryFinish(nil); err != nil {
		t.Fatalf("same-holder cleanup retry could not release: %v", err)
	}
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.HolderIdentity != nil || held.Spec.AcquireTime != nil || held.Spec.RenewTime != nil ||
		held.Spec.LeaseDurationSeconds != nil ||
		held.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] != "" {
		t.Fatalf("cleanup retry did not return the canonical Lease to idle: %#v", held)
	}
}

func TestAcquireDrainDeleteReportsCancellationAfterSuccessfulWork(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	deleteCtx, finish, err := c.AcquireDrainDelete(ctx, objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-deleteCtx.Done()
	if err := finish(nil); !errors.Is(err, devicecoordination.ErrMutationIncomplete) {
		t.Fatalf("finish error = %v, want mutation-incomplete cancellation", err)
	}
	var held coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.HolderIdentity == nil || *held.Spec.HolderIdentity != "software-drain/leaf-uid" {
		t.Fatalf("cancelled callback released an uncertain Lease: %#v", held.Spec)
	}

	_, retryFinish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatalf("fresh-context same-holder cleanup retry failed: %v", err)
	}
	if err := retryFinish(nil); err != nil {
		t.Fatalf("fresh-context cleanup retry could not release: %v", err)
	}
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.HolderIdentity != nil || held.Spec.AcquireTime != nil || held.Spec.RenewTime != nil ||
		held.Spec.LeaseDurationSeconds != nil ||
		held.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] != "" {
		t.Fatalf("fresh-context retry did not return the canonical Lease to idle: %#v", held)
	}
}

func TestAcquireDrainDeleteAuthorizationLossDoesNotRenewLease(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	c.renewInterval = 100 * time.Millisecond
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}

	var before coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &before); err != nil {
		t.Fatal(err)
	}
	var node corev1.Node
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.node), &node); err != nil {
		t.Fatal(err)
	}
	node.Annotations[managedprotocol.AnnotationProjectionHash] = "authorization-lost-before-renewal"
	if err := c.Client.Update(context.Background(), &node); err != nil {
		t.Fatal(err)
	}

	select {
	case <-deleteCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("managed drain callback was not cancelled after renewal authorization was lost")
	}
	if err := finish(errors.New("authorization lost before renewal")); err != nil {
		t.Fatalf("finish after authorization loss = %v", err)
	}

	var after coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("authorization loss mutated the retained Lease\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestAcquireDrainDeleteReleasesLeaseOnPreDispatchFailure(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		failPublish                    bool
		invalidateAfterPublish         bool
		advanceDeviceCleanAfterPublish bool
	}{
		{name: "request publication", failPublish: true},
		{name: "post-acquire reauthorization", invalidateAfterPublish: true},
		{name: "DeviceClean fence after initial authorization", advanceDeviceCleanAfterPublish: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
				if tc.advanceDeviceCleanAfterPublish {
					now := metav1.Now()
					o.leaf.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodTerminationObserved
					o.leaf.Status.ManagerDrain.Pods[0].DeletionObservedAt = &now
				}
			})
			c.Client = &drainPreDispatchFailureClient{
				Client: c.Client, failPublish: tc.failPublish,
				invalidateAfterPublish:         tc.invalidateAfterPublish,
				advanceDeviceCleanAfterPublish: tc.advanceDeviceCleanAfterPublish,
			}
			if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); err == nil {
				finish(nil)
				t.Fatal("pre-dispatch failure unexpectedly authorized device teardown")
			} else if tc.advanceDeviceCleanAfterPublish && !strings.Contains(err.Error(), "entered DeviceClean before device dispatch") {
				t.Fatalf("DeviceClean fence error = %v", err)
			}
			var lease coordv1.Lease
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
				t.Fatal(err)
			}
			if lease.Spec.HolderIdentity != nil || lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil ||
				lease.Spec.LeaseDurationSeconds != nil {
				t.Fatalf("pre-dispatch failure retained active Lease state: %#v", lease.Spec)
			}
			for _, key := range []string{
				managedprotocol.AnnotationMaintenanceRequestVersion,
				managedprotocol.AnnotationMaintenanceSessionToken,
				managedprotocol.AnnotationMaintenancePurpose,
			} {
				if _, exists := lease.Annotations[key]; exists {
					t.Fatalf("pre-dispatch failure retained request annotation %s", key)
				}
			}
		})
	}
}

func TestAcquireDrainDeleteRejectsIdleDeviceCleanFence(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		now := metav1.Now()
		pod := &o.leaf.Status.ManagerDrain.Pods[0]
		pod.Phase = opsv1alpha1.UpgradeDrainPodDeviceClean
		pod.DeletionObservedAt = &now
		pod.DeviceCleanAt = &now
		pod.DeviceCleanInventoryRevision = 1
	})
	if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); err == nil {
		finish(nil)
		t.Fatal("idle DeviceClean fence authorized a new device deletion")
	} else if !strings.Contains(err.Error(), "DeviceClean fences new managed drain deletion") {
		t.Fatalf("idle DeviceClean error = %v", err)
	}
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity != nil || lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil ||
		lease.Spec.LeaseDurationSeconds != nil || hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
		t.Fatalf("idle DeviceClean attempt changed the canonical Lease: %#v", lease)
	}
}

func TestValidateDrainPreDispatchFenceRequiresSameRetainedAcquisition(t *testing.T) {
	holder := "software-drain/leaf-uid"
	acquired := metav1.NewMicroTime(time.Now().UTC().Add(-time.Second))
	initial := drainDeleteAuthority{
		holder: holder, podPhase: opsv1alpha1.UpgradeDrainPodDeviceClean,
		lease: coordv1.Lease{
			ObjectMeta: metav1.ObjectMeta{UID: "lease-uid"},
			Spec: coordv1.LeaseSpec{
				HolderIdentity: &holder, AcquireTime: &acquired, LeaseTransitions: ptr.To[int32](4),
			},
		},
	}
	if err := validateDrainPreDispatchFence(initial, initial); err != nil {
		t.Fatalf("same retained DeviceClean acquisition was rejected: %v", err)
	}

	reacquired := initial
	reacquired.lease = *initial.lease.DeepCopy()
	reacquired.lease.Spec.AcquireTime = ptr.To(metav1.NewMicroTime(acquired.Add(time.Second)))
	reacquired.lease.Spec.LeaseTransitions = ptr.To[int32](5)
	if err := validateDrainPreDispatchFence(initial, reacquired); err == nil ||
		!strings.Contains(err.Error(), "no longer owns the retained Lease acquisition") {
		t.Fatalf("released-and-reacquired DeviceClean Lease error = %v", err)
	}

	preFence := initial
	preFence.podPhase = opsv1alpha1.UpgradeDrainPodTerminationObserved
	if err := validateDrainPreDispatchFence(preFence, initial); err == nil ||
		!strings.Contains(err.Error(), "entered DeviceClean before device dispatch") {
		t.Fatalf("pre-fence authorization crossing DeviceClean error = %v", err)
	}
}

func TestAcquireDrainDeleteSerializesSameHolderCallbacks(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	_, finishFirst, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		finish func(error) error
		err    error
	}
	started := make(chan struct{})
	returned := make(chan result, 1)
	go func() {
		close(started)
		_, finish, acquireErr := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
		returned <- result{finish: finish, err: acquireErr}
	}()
	<-started
	select {
	case second := <-returned:
		if second.finish != nil {
			second.finish(nil)
		}
		t.Fatalf("same-holder callback bypassed serialization: %v", second.err)
	case <-time.After(25 * time.Millisecond):
	}
	finishFirst(nil)
	select {
	case second := <-returned:
		if second.err != nil {
			t.Fatalf("serialized callback failed after release: %v", second.err)
		}
		second.finish(nil)
	case <-time.After(time.Second):
		t.Fatal("serialized callback did not proceed after first completion")
	}
}

func TestAcquireDrainDeleteRequiresEveryAuthorityBinding(t *testing.T) {
	tests := map[string]func(*drainFixtureObjects){
		"manager drain absent": func(o *drainFixtureObjects) { o.leaf.Status.ManagerDrain = nil },
		"manager state": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainDrained
		},
		"admission state": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
		},
		"session token": func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession.SessionToken = "00000000-0000-4000-8000-000000000002"
		},
		"malformed session token": func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession.SessionToken = "not-a-uuid"
			o.leaf.Status.ManagerDrain.SessionToken = "not-a-uuid"
			o.pod.Annotations[managedprotocol.AnnotationDrainSession] = "not-a-uuid"
		},
		"session purpose": func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession.Purpose = ciskov1.DeviceMaintenancePurposeSoftwareMutation
		},
		"session requested-at": func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession.RequestedAt = metav1.NewTime(
				o.leaf.Status.ManagerDrain.StartedAt.Add(time.Second),
			)
		},
		"reservation": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.ReservationID = "other"
		},
		"policy epoch": func(o *drainFixtureObjects) { o.leaf.Status.ManagerDrain.PolicyEpoch++ },
		"control revision": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.ControlRevision++
		},
		"node UID":   func(o *drainFixtureObjects) { o.leaf.Status.ManagerDrain.NodeUID = "other" },
		"device UID": func(o *drainFixtureObjects) { o.device.Status.MaintenanceSession.DeviceUID = "other" },
		"worker acknowledgement": func(o *drainFixtureObjects) {
			o.leaf.Status.WorkerControl.ObservedPolicyEpoch++
		},
		"worker revision status": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision = nil
		},
		"worker desired revision": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.DesiredRevision =
				"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		},
		"worker observed revision": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.ObservedRevision =
				"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		},
		"worker Deployment identity": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.DeploymentUID = ""
		},
		"worker Pod identity": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.PodUID = ""
		},
		"worker Pod incarnation": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.PodUID = "replacement-worker-pod-uid"
		},
		"worker Pod start": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.PodStartTime = nil
		},
		"worker ready heartbeat": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.ReadyHeartbeatTime = nil
		},
		"worker heartbeat ordering": func(o *drainFixtureObjects) {
			o.device.Status.WorkerRevision.ReadyHeartbeatTime =
				ptr.To(metav1.NewTime(o.device.Status.WorkerRevision.PodStartTime.Add(-time.Second)))
		},
		"worker Node observation": func(o *drainFixtureObjects) {
			o.node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] =
				"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		},
		"foreign runtime worker revision": func(o *drainFixtureObjects) {
			revision := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			o.leaf.Status.WorkerControl.ObservedWorkerConfigRevision = revision
			o.node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = revision
		},
		"campaign annotation": func(o *drainFixtureObjects) {
			o.leaf.Annotations[managedprotocol.AnnotationCampaignUID] = "other"
		},
		"Node projection": func(o *drainFixtureObjects) {
			o.node.Annotations[managedprotocol.AnnotationProjectionHash] = "other"
		},
		"maintenance guard": func(o *drainFixtureObjects) { o.node.Spec.Taints = nil },
		"missing maintenance-taint owner": func(o *drainFixtureObjects) {
			delete(o.node.Annotations, managedprotocol.AnnotationDrainTaintOwner)
		},
		"foreign maintenance-taint owner": func(o *drainFixtureObjects) {
			o.node.Annotations[managedprotocol.AnnotationDrainTaintOwner] = "00000000-0000-4000-8000-000000000002"
		},
		"Pod UID snapshot": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.Pods[0].UID = "other"
		},
		"duplicate Pod snapshot": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.Pods = append(
				o.leaf.Status.ManagerDrain.Pods, o.leaf.Status.ManagerDrain.Pods[0],
			)
		},
		"Pod session annotation": func(o *drainFixtureObjects) {
			o.pod.Annotations[managedprotocol.AnnotationDrainSession] = "00000000-0000-4000-8000-000000000002"
		},
		"Pod finalizer": func(o *drainFixtureObjects) { o.pod.Finalizers = nil },
		"Pod Node":      func(o *drainFixtureObjects) { o.pod.Spec.NodeName = "other" },
		"Pod opt-in label": func(o *drainFixtureObjects) {
			delete(o.pod.Labels, "operations.cisco.vk/drain-safe")
		},
		"controller UID": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.Pods[0].Controller.UID = "other"
		},
		"Lease UID": func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession.Lease.UID = "other"
		},
		"Lease binding": func(o *drainFixtureObjects) {
			o.lease.Annotations[managedprotocol.AnnotationDeviceUID] = "other"
		},
		"expired deadline": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.DrainDeadline = metav1.NewTime(time.Now().Add(-time.Minute))
		},
		"zero drain duration": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.DrainDeadline = o.leaf.Status.ManagerDrain.StartedAt
		},
		"excessive drain duration": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.DrainDeadline = metav1.NewTime(
				o.leaf.Status.ManagerDrain.StartedAt.Add(maxManagedDrainDuration + time.Second),
			)
		},
		"recovery deadline outside recovery": func(o *drainFixtureObjects) {
			deadline := metav1.NewTime(o.leaf.Status.ManagerDrain.UpdatedAt.Add(time.Hour))
			o.leaf.Status.ManagerDrain.RecoveryDeadline = &deadline
		},
		"unaccepted deletion": func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodProtected
			o.leaf.Status.ManagerDrain.Pods[0].EvictionRequestedAt = nil
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c, objects := drainCoordinatorFixture(t, mutate)
			if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); err == nil {
				finish(nil)
				t.Fatal("incomplete or mismatched drain authority was accepted")
			}
			var lease coordv1.Lease
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
				t.Fatal(err)
			}
			if lease.Spec.HolderIdentity != nil {
				t.Fatal("rejected drain authorization acquired the mutation Lease")
			}
		})
	}
}

func TestAcquireDrainDeleteAcceptsExternalDeletionAndBoundedRecovery(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		now := metav1.Now()
		o.pod.DeletionTimestamp = &now
		o.leaf.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodProtected
		o.leaf.Status.ManagerDrain.Pods[0].EvictionRequestedAt = nil
		setDrainFixtureRecovering(o, metav1.NewTime(time.Now().Add(time.Hour)))
	})
	_, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	finish(nil)
}

func TestAcquireDrainDeleteRejectsRecoveryDeadlineBeyondRenewalWindow(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		drain := o.leaf.Status.ManagerDrain
		drainDuration := drain.DrainDeadline.Sub(drain.StartedAt.Time)
		deadline := metav1.NewTime(drain.UpdatedAt.Add(drainDuration + drainRecoveryExtraLimit + time.Second))
		setDrainFixtureRecovering(o, deadline)
	})
	if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); err == nil {
		finish(nil)
		t.Fatal("recovery deadline beyond the protocol window was accepted")
	}
}

func setDrainFixtureRecovering(objects *drainFixtureObjects, deadline metav1.Time) {
	objects.leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainRecovering
	objects.leaf.Status.ManagerDrain.RecoveryDeadline = &deadline
	objects.leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
	objects.leaf.Status.ManagerAdmission.RevocationReason = "CampaignCancelled"
	objects.leaf.Status.ManagerControl.Cancel = true
	objects.leaf.Status.WorkerControl.EffectiveState = opsv1alpha1.UpgradeWorkerControlCancelled
	objects.device.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionRecovering
	objects.node.Spec.Unschedulable = false
	objects.node.Spec.Taints = nil
	delete(objects.node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
	delete(objects.node.Annotations, managedprotocol.AnnotationDrainTaintOwner)
}

func setDrainFixtureStaleRecoveryLease(
	objects *drainFixtureObjects,
	now time.Time,
	renewTime time.Time,
	publishRequest bool,
) {
	staleRevision := objects.device.Status.MaintenanceSession.ControlRevision
	currentRevision := staleRevision + 1
	updated := metav1.NewTime(now)
	recoveryDeadline := metav1.NewTime(now.Add(time.Hour))

	objects.leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
	objects.leaf.Status.ManagerAdmission.RevocationReason = "CampaignCancelled"
	objects.leaf.Status.ManagerAdmission.ControlRevision = ptr.To(currentRevision)
	objects.leaf.Status.ManagerAdmission.UpdatedAt = updated
	objects.leaf.Status.ManagerControl.Revision = currentRevision
	objects.leaf.Status.ManagerControl.Cancel = true
	objects.leaf.Status.ManagerControl.UpdatedAt = updated
	objects.leaf.Status.WorkerControl.ObservedAdmissionState = opsv1alpha1.UpgradeManagerAdmissionRevoked
	objects.leaf.Status.WorkerControl.ObservedControlRevision = currentRevision
	objects.leaf.Status.WorkerControl.EffectiveState = opsv1alpha1.UpgradeWorkerControlCancelled
	objects.leaf.Status.WorkerControl.UpdatedAt = updated
	objects.leaf.Status.ManagerDrain.State = opsv1alpha1.UpgradeManagerDrainRecovering
	objects.leaf.Status.ManagerDrain.ControlRevision = currentRevision
	objects.leaf.Status.ManagerDrain.RecoveryDeadline = &recoveryDeadline
	objects.leaf.Status.ManagerDrain.UpdatedAt = updated

	holder := objects.device.Status.MaintenanceSession.Lease.Holder
	renewed := metav1.NewMicroTime(renewTime)
	objects.lease.Spec.HolderIdentity = ptr.To(holder)
	objects.lease.Spec.AcquireTime = &renewed
	objects.lease.Spec.RenewTime = &renewed
	objects.lease.Spec.LeaseDurationSeconds = ptr.To(int32(writeLeaseTTL / time.Second))
	objects.lease.Spec.LeaseTransitions = ptr.To[int32](1)
	if publishRequest {
		staleDrain := *objects.leaf.Status.ManagerDrain
		staleDrain.ControlRevision = staleRevision
		for annotation, value := range drainLeaseAnnotations(objects.device.Status.MaintenanceSession, &staleDrain) {
			objects.lease.Annotations[annotation] = value
		}
	}
}

func TestRetireExpiredStaleDrainRecoveryLeaseNeverReleasesEarly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		setDrainFixtureStaleRecoveryLease(o, now, now.Add(-writeLeaseTTL+time.Second), true)
	})

	handled, err := c.retireExpiredStaleDrainRecoveryLease(
		context.Background(), objects.pod.DeepCopy(), now,
	)
	if !handled || !errors.Is(err, devicecoordination.ErrMutationIncomplete) ||
		!strings.Contains(err.Error(), "remains quarantined until") {
		t.Fatalf("unexpired stale Lease result = (handled=%t, err=%v), want explicit quarantine wait", handled, err)
	}
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != objects.device.Status.MaintenanceSession.Lease.Holder ||
		lease.Spec.RenewTime == nil || !lease.Spec.RenewTime.Equal(&metav1.MicroTime{Time: now.Add(-writeLeaseTTL + time.Second)}) ||
		lease.Annotations[managedprotocol.AnnotationMaintenanceControlRevision] != "7" {
		t.Fatalf("unexpired stale Lease was changed: %#v", lease)
	}
}

func TestRetireExpiredStaleDrainRecoveryLeaseAtExactExpiry(t *testing.T) {
	for _, publishRequest := range []bool{false, true} {
		name := "request-absent"
		if publishRequest {
			name = "exact-request"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
				setDrainFixtureStaleRecoveryLease(o, now, now.Add(-writeLeaseTTL), publishRequest)
			})

			handled, err := c.retireExpiredStaleDrainRecoveryLease(
				context.Background(), objects.pod.DeepCopy(), now,
			)
			if !handled || !errors.Is(err, devicecoordination.ErrMutationIncomplete) ||
				!strings.Contains(err.Error(), "retired expired stale drain Lease revision 7") {
				t.Fatalf("exact-expiry retirement = (handled=%t, err=%v)", handled, err)
			}
			var lease coordv1.Lease
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
				t.Fatal(err)
			}
			if lease.Spec.HolderIdentity != nil || lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil ||
				lease.Spec.LeaseDurationSeconds != nil || lease.Spec.LeaseTransitions == nil ||
				*lease.Spec.LeaseTransitions != 1 || hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
				t.Fatalf("exact-expiry retirement did not leave a canonical idle Lease: %#v", lease)
			}
		})
	}
}

func TestAcquireDrainDeleteRequiresFreshAcknowledgementAfterStaleLeaseRetirement(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		setDrainFixtureStaleRecoveryLease(o, now, now.Add(-writeLeaseTTL-time.Second), true)
	})

	if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); finish != nil || !errors.Is(err, devicecoordination.ErrMutationIncomplete) ||
		!strings.Contains(err.Error(), "retired expired stale drain Lease revision 7") {
		t.Fatalf("stale retirement result = (finish=%v, err=%v)", finish != nil, err)
	}
	if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); finish != nil || !errors.Is(err, devicecoordination.ErrMutationIncomplete) ||
		!strings.Contains(err.Error(), "is idle; waiting for fresh manager acknowledgement at revision 8") {
		t.Fatalf("pre-acknowledgement retry = (finish=%v, err=%v)", finish != nil, err)
	}

	var node corev1.Node
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.node), &node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Unschedulable = false
	node.Spec.Taints = nil
	delete(node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
	delete(node.Annotations, managedprotocol.AnnotationDrainTaintOwner)
	if err := c.Client.Update(context.Background(), &node); err != nil {
		t.Fatal(err)
	}
	var device ciskov1.CiscoDevice
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.device), &device); err != nil {
		t.Fatal(err)
	}
	device.Status.MaintenanceSession.ControlRevision = 8
	device.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionRecovering
	if err := c.Client.Status().Update(context.Background(), &device); err != nil {
		t.Fatal(err)
	}

	_, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatalf("fresh revision acknowledgement did not authorize recovery cleanup: %v", err)
	}
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Annotations[managedprotocol.AnnotationMaintenanceControlRevision] != "8" {
		t.Fatalf("cleanup request control revision = %q, want 8", lease.Annotations[managedprotocol.AnnotationMaintenanceControlRevision])
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
}

func TestRetireExpiredStaleDrainRecoveryLeaseRejectsAmbiguousBinding(t *testing.T) {
	tests := map[string]func(*drainFixtureObjects){
		"partial request": func(o *drainFixtureObjects) {
			o.lease.Annotations[managedprotocol.AnnotationMaintenanceRequestVersion] = managedprotocol.DrainProtocolVersion
		},
		"foreign request operation": func(o *drainFixtureObjects) {
			staleDrain := *o.leaf.Status.ManagerDrain
			staleDrain.ControlRevision = o.device.Status.MaintenanceSession.ControlRevision
			for annotation, value := range drainLeaseAnnotations(o.device.Status.MaintenanceSession, &staleDrain) {
				o.lease.Annotations[annotation] = value
			}
			o.lease.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = "foreign-leaf-uid"
		},
		"foreign holder": func(o *drainFixtureObjects) {
			o.lease.Spec.HolderIdentity = ptr.To("software-drain/foreign-leaf-uid")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
				setDrainFixtureStaleRecoveryLease(o, now, now.Add(-writeLeaseTTL), false)
				mutate(o)
			})
			handled, err := c.retireExpiredStaleDrainRecoveryLease(
				context.Background(), objects.pod.DeepCopy(), now,
			)
			if !handled || err == nil {
				t.Fatalf("ambiguous stale Lease result = (handled=%t, err=%v), want fail-closed error", handled, err)
			}
			var lease coordv1.Lease
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
				t.Fatal(err)
			}
			if lease.Spec.HolderIdentity == nil {
				t.Fatal("ambiguous stale Lease was released")
			}
		})
	}
}

func TestRetireExpiredDrainLeaseRequiresStrictlyOlderSessionRevision(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c, objects := drainCoordinatorFixture(t, func(o *drainFixtureObjects) {
		setDrainFixtureStaleRecoveryLease(o, now, now.Add(-writeLeaseTTL), true)
		currentRevision := o.leaf.Status.ManagerDrain.ControlRevision
		o.device.Status.MaintenanceSession.ControlRevision = currentRevision
		o.device.Status.MaintenanceSession.Phase = ciskov1.DeviceMaintenanceSessionRecovering
		o.node.Spec.Unschedulable = false
		o.node.Spec.Taints = nil
		delete(o.node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
		delete(o.node.Annotations, managedprotocol.AnnotationDrainTaintOwner)
		staleDrain := *o.leaf.Status.ManagerDrain
		for annotation, value := range drainLeaseAnnotations(o.device.Status.MaintenanceSession, &staleDrain) {
			o.lease.Annotations[annotation] = value
		}
	})

	handled, err := c.retireExpiredStaleDrainRecoveryLease(
		context.Background(), objects.pod.DeepCopy(), now,
	)
	if handled || err != nil {
		t.Fatalf("current-revision Lease retirement = (handled=%t, err=%v), want no rollover handling", handled, err)
	}
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil {
		t.Fatal("current-revision Lease was released by the rollover path")
	}
}

func TestAcquireDrainDeleteUsesFreshPodUID(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	stale := objects.pod.DeepCopy()
	stale.UID = types.UID("recreated-uid")
	if _, finish, err := c.AcquireDrainDelete(context.Background(), stale); err == nil {
		finish(nil)
		t.Fatal("same-name Pod with a different UID was authorized")
	}
}

func TestResolveDrainDeletePodUsesLiveRecoveryProtection(t *testing.T) {
	c, objects := prepareManagedDrainRecovery(t)
	stale := objects.pod.DeepCopy()
	stale.Annotations = nil
	stale.Finalizers = nil

	resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	if disposition != PodDeleteDrainTeardown || resolved == stale || resolved.UID != objects.pod.UID ||
		resolved.Annotations[managedprotocol.AnnotationDrainSession] != drainSessionToken {
		t.Fatalf("live exact-UID protection disposition=%v pod=%#v", disposition, resolved)
	}
}

func TestResolveDrainDeletePodRejectsProtectedReplacementUID(t *testing.T) {
	c, objects := prepareManagedDrainRecovery(t)
	stale := objects.pod.DeepCopy()
	stale.UID = "previous-pod-uid"
	stale.Annotations = nil
	stale.Finalizers = nil

	if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), stale); err == nil ||
		disposition != PodDeleteOrdinary || resolved != nil || !strings.Contains(err.Error(), "not callback UID") {
		t.Fatalf("protected replacement resolution = (pod=%#v, disposition=%v, err=%v), want fail closed", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodRejectsUnprotectedReplacementUID(t *testing.T) {
	c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, nil)
	stale := objects.pod.DeepCopy()
	stale.UID = "previous-pod-uid"

	if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), stale); err == nil ||
		disposition != PodDeleteOrdinary || resolved != nil || !strings.Contains(err.Error(), "not callback UID") {
		t.Fatalf("unprotected replacement resolution = (pod=%#v, disposition=%v, err=%v), want fail closed", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodRejectsForeignLiveNode(t *testing.T) {
	c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, func(o *drainFixtureObjects) {
		o.pod.Spec.NodeName = "other-node"
	})

	if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy()); err == nil ||
		disposition != PodDeleteOrdinary || resolved != nil || !strings.Contains(err.Error(), "not runtime Node") {
		t.Fatalf("foreign-Node resolution = (pod=%#v, disposition=%v, err=%v), want fail closed", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodFailsClosedOnLiveReadError(t *testing.T) {
	c, objects := prepareManagedDrainRecovery(t)
	c.Client = drainPodReadFailureClient{Client: c.Client}
	stale := objects.pod.DeepCopy()
	stale.Annotations = nil
	stale.Finalizers = nil

	if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), stale); err == nil ||
		disposition != PodDeleteOrdinary || resolved != nil || !strings.Contains(err.Error(), "injected live Pod read failure") {
		t.Fatalf("failed live read resolution = (pod=%#v, disposition=%v, err=%v), want fail closed", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodPreservesOrdinaryCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(context.Context, *Coordinator, *drainFixtureObjects) error
	}{
		{
			name: "not found",
			change: func(ctx context.Context, c *Coordinator, objects *drainFixtureObjects) error {
				var live corev1.Pod
				if err := c.Client.Get(ctx, client.ObjectKeyFromObject(objects.pod), &live); err != nil {
					return err
				}
				live.Finalizers = nil
				if err := c.Client.Update(ctx, &live); err != nil {
					return err
				}
				return c.Client.Delete(ctx, &live)
			},
		},
		{
			name: "live unprotected",
			change: func(ctx context.Context, c *Coordinator, objects *drainFixtureObjects) error {
				var live corev1.Pod
				if err := c.Client.Get(ctx, client.ObjectKeyFromObject(objects.pod), &live); err != nil {
					return err
				}
				delete(live.Annotations, managedprotocol.AnnotationDrainSession)
				live.Finalizers = nil
				return c.Client.Update(ctx, &live)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := prepareManagedDrainRecovery(t)
			stale := objects.pod.DeepCopy()
			stale.Annotations = nil
			stale.Finalizers = nil
			if err := tc.change(context.Background(), c, objects); err != nil {
				t.Fatal(err)
			}
			resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), stale)
			if err != nil || disposition != PodDeleteOrdinary || resolved != stale {
				t.Fatalf("ordinary resolution = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
			}
		})
	}
}

func TestResolveDrainDeletePodCallerMarkerForcesStrictWhenLiveMissing(t *testing.T) {
	c, objects := prepareManagedDrainRecovery(t)
	marked := objects.pod.DeepCopy()
	marked.Finalizers = nil
	if err := c.Client.Update(context.Background(), marked); err != nil {
		t.Fatal(err)
	}
	if err := c.Client.Delete(context.Background(), marked); err != nil {
		t.Fatal(err)
	}

	marked.Finalizers = []string{managedprotocol.DrainPodFinalizer}
	resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), marked)
	if err != nil || disposition != PodDeleteDrainTeardown || resolved != marked {
		t.Fatalf("caller-marked resolution = (pod=%#v, disposition=%v, err=%v), want strict when live Pod is absent", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodRoutesDeviceCleanCompletion(t *testing.T) {
	for _, phase := range []opsv1alpha1.UpgradeDrainPodPhase{
		opsv1alpha1.UpgradeDrainPodDeviceClean,
		opsv1alpha1.UpgradeDrainPodReleased,
	} {
		for _, staleMarkedCallback := range []bool{false, true} {
			t.Run(string(phase)+"/stale-marked="+strconv.FormatBool(staleMarkedCallback), func(t *testing.T) {
				c, objects := releasedDrainCompletionFixture(t, phase, nil)
				callback := objects.pod.DeepCopy()
				if staleMarkedCallback {
					callback.Annotations[managedprotocol.AnnotationDrainSession] = drainSessionToken
					callback.Finalizers = []string{managedprotocol.DrainPodFinalizer}
				}

				resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), callback)
				if err != nil {
					t.Fatal(err)
				}
				if disposition != PodDeleteReleasedCompletion || resolved == callback ||
					resolved.UID != objects.pod.UID || hasDrainPodMarker(resolved) {
					t.Fatalf("completion resolution = (pod=%#v, disposition=%v)", resolved, disposition)
				}
			})
		}
	}
}

func TestResolveDrainDeletePodSecondReadDisappearanceStillRequiresDeviceCleanProof(t *testing.T) {
	for _, tc := range []struct {
		name            string
		phase           opsv1alpha1.UpgradeDrainPodPhase
		mutate          func(*drainFixtureObjects)
		wantDisposition PodDeleteDisposition
		wantError       bool
	}{
		{
			name: "valid device-clean proof", phase: opsv1alpha1.UpgradeDrainPodDeviceClean,
			wantDisposition: PodDeleteReleasedCompletion,
		},
		{
			name: "termination only", phase: opsv1alpha1.UpgradeDrainPodTerminationObserved,
			wantDisposition: PodDeleteOrdinary, wantError: true,
		},
		{
			name: "held Lease", phase: opsv1alpha1.UpgradeDrainPodDeviceClean,
			wantDisposition: PodDeleteOrdinary, wantError: true,
			mutate: func(o *drainFixtureObjects) {
				now := metav1.NowMicro()
				o.lease.Spec.HolderIdentity = ptr.To("software-drain/" + string(o.leaf.UID))
				o.lease.Spec.AcquireTime = &now
				o.lease.Spec.RenewTime = &now
				o.lease.Spec.LeaseDurationSeconds = ptr.To[int32](1800)
				o.lease.Spec.LeaseTransitions = ptr.To[int32](1)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := releasedDrainCompletionFixture(t, tc.phase, tc.mutate)
			c.Client = &drainPodSecondReadNotFoundClient{Client: c.Client}
			resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy())
			if (err != nil) != tc.wantError || disposition != tc.wantDisposition ||
				(!tc.wantError && resolved == nil) || (tc.wantError && resolved != nil) {
				t.Fatalf("second-read disappearance = (pod=%#v, disposition=%v, err=%v), want disposition=%v error=%t",
					resolved, disposition, err, tc.wantDisposition, tc.wantError)
			}
		})
	}
}

func TestResolveDrainDeletePodCompletionRequiresAcceptedDeviceCleanEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phase  opsv1alpha1.UpgradeDrainPodPhase
		mutate func(*opsv1alpha1.UpgradeDrainPodStatus)
	}{
		{name: "termination observed", phase: opsv1alpha1.UpgradeDrainPodTerminationObserved},
		{name: "complete", phase: opsv1alpha1.UpgradeDrainPodComplete},
		{name: "missing protected time", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) { p.ProtectedAt = nil }},
		{name: "missing eviction time", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) { p.EvictionRequestedAt = nil }},
		{name: "missing deletion time", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) { p.DeletionObservedAt = nil }},
		{name: "missing clean time", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) { p.DeviceCleanAt = nil }},
		{name: "missing released time", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) { p.ReleasedAt = nil }},
		{name: "eviction before protection", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) {
			p.EvictionRequestedAt = ptr.To(metav1.NewTime(p.ProtectedAt.Add(-time.Second)))
		}},
		{name: "deletion before eviction", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) {
			p.DeletionObservedAt = ptr.To(metav1.NewTime(p.EvictionRequestedAt.Add(-time.Second)))
		}},
		{name: "clean before deletion", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) {
			p.DeviceCleanAt = ptr.To(metav1.NewTime(p.DeletionObservedAt.Add(-time.Second)))
		}},
		{name: "release before clean", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) {
			p.ReleasedAt = ptr.To(metav1.NewTime(p.DeviceCleanAt.Add(-time.Second)))
		}},
		{name: "zero clean revision", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) { p.DeviceCleanInventoryRevision = 0 }},
		{name: "clean revision not newer", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) {
			p.DeviceCleanInventoryRevision = p.DeletionObservedInventoryRevision
		}},
		{name: "unaccepted recovery release", phase: opsv1alpha1.UpgradeDrainPodReleased, mutate: func(p *opsv1alpha1.UpgradeDrainPodStatus) {
			p.EvictionRequestedAt = nil
			p.DeletionObservedAt = nil
			p.DeviceCleanAt = nil
			p.DeviceCleanInventoryRevision = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := releasedDrainCompletionFixture(t, tc.phase, func(o *drainFixtureObjects) {
				if tc.mutate != nil {
					tc.mutate(&o.leaf.Status.ManagerDrain.Pods[0])
				}
			})
			if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy()); err == nil || resolved != nil || disposition != PodDeleteOrdinary {
				t.Fatalf("invalid completion evidence = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
			}
		})
	}
}

func TestResolveDrainDeletePodCompletionSurvivesMutationAuthorityTurnover(t *testing.T) {
	c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, func(o *drainFixtureObjects) {
		o.device.Status.WorkerRevision = nil
		o.device.Status.TopologyProjection = nil
		o.device.Status.TopologyLock = nil
		o.device.Status.Conditions = nil
		o.leaf.Status.WorkerControl = nil
		o.node.Spec.Unschedulable = false
		o.node.Spec.Taints = nil
		delete(o.node.Annotations, managedprotocol.AnnotationDrainCordonOwner)
		delete(o.node.Annotations, managedprotocol.AnnotationDrainTaintOwner)
		o.node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = "sha256:" + strings.Repeat("f", 64)
		o.leaf.Status.ManagerDrain.StartedAt = metav1.NewTime(time.Now().Add(-3 * time.Hour))
		o.device.Status.MaintenanceSession.RequestedAt = o.leaf.Status.ManagerDrain.StartedAt
		o.leaf.Status.ManagerDrain.DrainDeadline = metav1.NewTime(time.Now().Add(-2 * time.Hour))
	})

	resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy())
	if err != nil || disposition != PodDeleteReleasedCompletion || resolved == nil {
		t.Fatalf("durable completion after authority turnover = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodCompletionSurvivesStaleRecoveryAcknowledgement(t *testing.T) {
	c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, func(o *drainFixtureObjects) {
		now := metav1.Now()
		deadline := metav1.NewTime(now.Add(time.Hour))
		setDrainFixtureRecovering(o, deadline)
		currentRevision := o.device.Status.MaintenanceSession.ControlRevision + 1
		o.leaf.Status.ManagerAdmission.ControlRevision = ptr.To(currentRevision)
		o.leaf.Status.ManagerControl.Revision = currentRevision
		o.leaf.Status.ManagerDrain.ControlRevision = currentRevision
		o.leaf.Status.ManagerDrain.UpdatedAt = now
	})

	resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy())
	if err != nil || disposition != PodDeleteReleasedCompletion || resolved == nil {
		t.Fatalf("completion with stale recovery acknowledgement = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodCompletionRequiresIdleRequestFreeLease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*drainFixtureObjects)
	}{
		{name: "held", mutate: func(o *drainFixtureObjects) {
			now := metav1.NowMicro()
			o.lease.Spec.HolderIdentity = ptr.To("software-drain/" + string(o.leaf.UID))
			o.lease.Spec.AcquireTime = &now
			o.lease.Spec.RenewTime = &now
			o.lease.Spec.LeaseDurationSeconds = ptr.To[int32](1800)
			o.lease.Spec.LeaseTransitions = ptr.To[int32](1)
		}},
		{name: "timing residue", mutate: func(o *drainFixtureObjects) { o.lease.Spec.LeaseDurationSeconds = ptr.To[int32](1800) }},
		{name: "whitespace holder", mutate: func(o *drainFixtureObjects) { o.lease.Spec.HolderIdentity = ptr.To(" ") }},
		{name: "request metadata", mutate: func(o *drainFixtureObjects) {
			o.lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = drainSessionToken
		}},
		{name: "wrong lease UID", mutate: func(o *drainFixtureObjects) { o.lease.UID = "replacement-lease" }},
		{name: "wrong binding", mutate: func(o *drainFixtureObjects) {
			o.lease.Annotations[managedprotocol.AnnotationDeviceUID] = "other-device"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, tc.mutate)
			if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy()); err == nil || resolved != nil || disposition != PodDeleteOrdinary {
				t.Fatalf("unsafe Lease completion = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
			}
		})
	}
}

func TestResolveDrainDeletePodCompletionAcceptsExplicitEmptyLeaseHolder(t *testing.T) {
	c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, func(o *drainFixtureObjects) {
		o.lease.Spec.HolderIdentity = ptr.To("")
	})

	resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy())
	if err != nil || disposition != PodDeleteReleasedCompletion || resolved == nil {
		t.Fatalf("explicit-empty-holder completion = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodCompletionRequiresExactManagerIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*drainFixtureObjects)
	}{
		{name: "foreign device", mutate: func(o *drainFixtureObjects) { o.device.UID = "replacement-device" }},
		{name: "wrong Node identity", mutate: func(o *drainFixtureObjects) { o.device.Status.NodeIdentity.NodeUID = "replacement-node" }},
		{name: "wrong session token", mutate: func(o *drainFixtureObjects) {
			o.leaf.Status.ManagerDrain.SessionToken = "00000000-0000-4000-8000-000000000002"
		}},
		{name: "wrong session requested-at", mutate: func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession.RequestedAt = metav1.NewTime(
				o.leaf.Status.ManagerDrain.StartedAt.Add(time.Second),
			)
		}},
		{name: "wrong operation UID", mutate: func(o *drainFixtureObjects) { o.device.Status.MaintenanceSession.Operation.UID = "replacement-leaf" }},
		{name: "invalid selection hash", mutate: func(o *drainFixtureObjects) { o.leaf.Status.ManagerDrain.Pods[0].EligibilityHash = drainPlanHash }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, tc.mutate)
			if resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy()); err == nil || resolved != nil || disposition != PodDeleteOrdinary {
				t.Fatalf("foreign completion identity = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
			}
		})
	}
}

func TestResolveDrainDeletePodCompletionSurvivesBoundObjectDeletion(t *testing.T) {
	c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, func(o *drainFixtureObjects) {
		now := metav1.Now()
		o.device.DeletionTimestamp = &now
		o.device.Finalizers = []string{"example.com/fake-retain"}
		o.leaf.DeletionTimestamp = &now
		o.leaf.Finalizers = []string{"example.com/fake-retain"}
	})

	resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy())
	if err != nil || disposition != PodDeleteReleasedCompletion || resolved == nil {
		t.Fatalf("completion during bound-object deletion = (pod=%#v, disposition=%v, err=%v)", resolved, disposition, err)
	}
}

func TestResolveDrainDeletePodCompletionProtectionAndOrdinaryBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mutate          func(*drainFixtureObjects)
		wantDisposition PodDeleteDisposition
	}{
		{name: "annotation remains", wantDisposition: PodDeleteDrainTeardown, mutate: func(o *drainFixtureObjects) {
			o.pod.Annotations[managedprotocol.AnnotationDrainSession] = drainSessionToken
		}},
		{name: "finalizer remains", wantDisposition: PodDeleteDrainTeardown, mutate: func(o *drainFixtureObjects) {
			o.pod.Finalizers = []string{managedprotocol.DrainPodFinalizer}
		}},
		{name: "not terminating", wantDisposition: PodDeleteOrdinary, mutate: func(o *drainFixtureObjects) {
			o.pod.DeletionTimestamp = nil
		}},
		{name: "unselected exact Pod", wantDisposition: PodDeleteOrdinary, mutate: func(o *drainFixtureObjects) {
			deadline := metav1.NewTime(time.Now().Add(time.Hour))
			setDrainFixtureRecovering(o, deadline)
			selected := &o.leaf.Status.ManagerDrain.Pods[0]
			selected.Name = "other-app"
			selected.UID = "other-pod-uid"
			hash, err := workloaddrain.EligibilityHash(selected)
			if err != nil {
				t.Fatal(err)
			}
			selected.EligibilityHash = hash
		}},
		{name: "no active session", wantDisposition: PodDeleteOrdinary, mutate: func(o *drainFixtureObjects) {
			o.device.Status.MaintenanceSession = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := releasedDrainCompletionFixture(t, opsv1alpha1.UpgradeDrainPodReleased, tc.mutate)
			resolved, disposition, err := c.ResolveDrainDeletePod(context.Background(), objects.pod.DeepCopy())
			if err != nil || resolved == nil || disposition != tc.wantDisposition {
				t.Fatalf("boundary resolution = (pod=%#v, disposition=%v, err=%v), want %v", resolved, disposition, err, tc.wantDisposition)
			}
		})
	}
}

func TestAcquireDrainDeleteRequiresAvailableCanonicalLease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(context.Context, *Coordinator, *drainFixtureObjects) error
	}{
		{
			name: "missing",
			change: func(ctx context.Context, c *Coordinator, o *drainFixtureObjects) error {
				return c.Client.Delete(ctx, o.lease)
			},
		},
		{
			name: "foreign active holder",
			change: func(ctx context.Context, c *Coordinator, o *drainFixtureObjects) error {
				var lease coordv1.Lease
				if err := c.Client.Get(ctx, client.ObjectKeyFromObject(o.lease), &lease); err != nil {
					return err
				}
				now := metav1.NewMicroTime(time.Now())
				lease.Spec.HolderIdentity = ptr.To("device-write/foreign")
				lease.Spec.AcquireTime = &now
				lease.Spec.RenewTime = &now
				lease.Spec.LeaseDurationSeconds = ptr.To[int32](1800)
				lease.Spec.LeaseTransitions = ptr.To[int32](1)
				return c.Client.Update(ctx, &lease)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, objects := drainCoordinatorFixture(t, nil)
			if err := tc.change(context.Background(), c, objects); err != nil {
				t.Fatal(err)
			}
			if _, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); err == nil {
				finish(nil)
				t.Fatal("unavailable canonical Lease authorized drain delete")
			}
		})
	}
}
