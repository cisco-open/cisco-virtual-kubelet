// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
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
)

func managedCoordinatorFixture(t *testing.T) (*Coordinator, *ciskov1.CiscoDevice, *corev1.Node, *ops.IOSXESoftwareUpgrade, *coordv1.Lease) {
	t.Helper()
	device := &ciskov1.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "switch", UID: "device-uid", Generation: 1}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "separate-node", UID: "node-uid", Annotations: map[string]string{
		managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName: device.Name, managedprotocol.AnnotationDeviceUID: string(device.UID),
		managedprotocol.AnnotationNodeName: "separate-node", managedprotocol.AnnotationNodeUID: "node-uid",
		managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:edge:worker", managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version,
	}}}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID)}
	device.Status.Conditions = []metav1.Condition{{Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionTrue, ObservedGeneration: 1, Reason: "Ready", LastTransitionTime: metav1.Now()}}
	device.Status.TopologyProjection = &ciskov1.DeviceTopologyProjectionStatus{EffectiveLabelHash: "projection"}
	node.Annotations[managedprotocol.AnnotationProjectionHash] = "projection"
	up := &ops.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "upgrade", UID: "leaf-uid", Annotations: map[string]string{
		managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceUID: string(device.UID),
	}}}
	up.Spec.DeviceRef.Name = device.Name
	up.Status.ManagerAdmission = &ops.UpgradeManagerAdmissionStatus{State: ops.UpgradeManagerAdmissionGranted}
	up.Status.ManagerControl = &ops.UpgradeManagerControlStatus{Revision: 7}
	annotations := map[string]string{devicecoordination.RetainLeaseAnnotation: "true"}
	for key, value := range node.Annotations {
		annotations[key] = value
	}
	annotations[managedprotocol.AnnotationLeasePurpose] = managedprotocol.LeasePurposeDeviceMutation
	now := metav1.NewMicroTime(time.Now())
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: engine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily), UID: "lease-uid", Annotations: annotations,
		Labels: map[string]string{"cisco.vk/device": devicecoordination.DeviceKey(device.Namespace, device.Name), "cisco.vk/family": devicecoordination.MutationLeaseFamily}}, Spec: coordv1.LeaseSpec{
		HolderIdentity: ptr.To(mutationguard.UpgradeHolderIdentity(up)), AcquireTime: &now, RenewTime: &now, LeaseDurationSeconds: ptr.To[int32](3600), LeaseTransitions: ptr.To[int32](1),
	}}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{ciskov1.AddToScheme, ops.AddToScheme, corev1.AddToScheme, coordv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(device, up).WithObjects(device, node, up, lease).Build()
	return &Coordinator{Client: c, Namespace: device.Namespace, DeviceName: device.Name, DeviceUID: string(device.UID), NodeName: node.Name, LeaseNamespace: lease.Namespace, ManagedTopology: true, MutationsEnabled: true}, device, node, up, lease
}

func acknowledgement(request publishedRequest, c *Coordinator, node *corev1.Node) *ciskov1.DeviceMaintenanceSessionStatus {
	return &ciskov1.DeviceMaintenanceSessionStatus{Phase: ciskov1.DeviceMaintenanceSessionAcknowledged,
		SessionToken: request.Token, RequestedAt: metav1.NewTime(request.RequestedAt), AcknowledgedAt: ptr.To(metav1.Now()),
		DeviceUID: c.DeviceUID, NodeName: node.Name, NodeUID: string(node.UID), ControlRevision: request.ControlRevision,
		Operation: request.Operation, Lease: ciskov1.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: ciskov1.DeviceMaintenanceObjectReference{
			Namespace: request.LeaseNamespace, Name: request.LeaseName, UID: request.LeaseUID}, Holder: request.Holder}}
}

func TestManagedMaintenanceAcknowledgementSurvivesJSONAndRestart(t *testing.T) {
	c, device, node, up, _ := managedCoordinatorFixture(t)
	ctx := context.Background()
	if err := c.BeforeSoftwareUpgradeMutation(ctx, up); err == nil {
		t.Fatal("mutation allowed without acknowledgement")
	}
	request, err := c.publishMaintenanceRequest(ctx, up, node, mutationguard.UpgradeHolderIdentity(up))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(acknowledgement(request, c, node))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &device.Status.MaintenanceSession); err != nil {
		t.Fatal(err)
	}
	if err := c.Client.Status().Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	if err := c.BeforeSoftwareUpgradeMutation(ctx, up); err == nil {
		t.Fatal("acknowledgement without Node taint allowed mutation")
	}
	node.Spec.Taints = []corev1.Taint{{Key: TaintKey, Value: TaintValue, Effect: corev1.TaintEffectNoSchedule}}
	if err := c.Client.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := c.BeforeSoftwareUpgradeMutation(ctx, up); err != nil {
			t.Fatalf("persisted exact acknowledgement rejected: %v", err)
		}
		// A new process reuses the persisted token, not a process-local token.
		c = &Coordinator{Client: c.Client, Namespace: c.Namespace, DeviceName: c.DeviceName, DeviceUID: c.DeviceUID, NodeName: c.NodeName, LeaseNamespace: c.LeaseNamespace, ManagedTopology: true}
	}
	up.Status.ManagerControl.Revision++
	if err := c.BeforeSoftwareUpgradeMutation(ctx, up); err == nil {
		t.Fatal("stale control acknowledgement authorized new revision")
	}
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.BeforeMutation(ctx); err == nil {
		t.Fatal("managed mode permitted direct disruptive Node writer")
	}
}

func TestManagedMaintenanceRejectsMismatchedAcknowledgements(t *testing.T) {
	c, _, node, up, _ := managedCoordinatorFixture(t)
	request, err := c.publishMaintenanceRequest(context.Background(), up, node, mutationguard.UpgradeHolderIdentity(up))
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ciskov1.DeviceMaintenanceSessionStatus){
		"phase":         func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.Phase = ciskov1.DeviceMaintenanceSessionSettled },
		"ack absent":    func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.AcknowledgedAt = nil },
		"token":         func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.SessionToken += "other" },
		"device UID":    func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.DeviceUID += "other" },
		"node UID":      func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.NodeUID += "other" },
		"lease UID":     func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.Lease.UID += "other" },
		"holder":        func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.Lease.Holder += "other" },
		"operation UID": func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.Operation.UID += "other" },
		"revision":      func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.ControlRevision++ },
		"timestamp":     func(s *ciskov1.DeviceMaintenanceSessionStatus) { s.RequestedAt.Time = s.RequestedAt.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			session := acknowledgement(request, c, node)
			change(session)
			if err := validateMaintenanceAcknowledgement(session, request, node); err == nil {
				t.Fatal("mismatched acknowledgement accepted")
			}
		})
	}
}

func TestManagedMaintenanceCannotRewriteForeignLeaseBinding(t *testing.T) {
	for _, field := range []string{managedprotocol.AnnotationDeviceUID, managedprotocol.AnnotationNodeUID, managedprotocol.AnnotationWorkerUsername, managedprotocol.AnnotationWorkerProtocol, devicecoordination.RetainLeaseAnnotation, "holder", "expired", "acquire time", "transitions", "excess duration"} {
		t.Run(field, func(t *testing.T) {
			c, _, node, up, lease := managedCoordinatorFixture(t)
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "holder":
				lease.Spec.HolderIdentity = ptr.To("other")
			case "expired":
				lease.Spec.RenewTime = ptr.To(metav1.NewMicroTime(time.Now().Add(-2 * time.Hour)))
			case "acquire time":
				lease.Spec.AcquireTime = nil
			case "transitions":
				lease.Spec.LeaseTransitions = ptr.To[int32](0)
			case "excess duration":
				lease.Spec.LeaseDurationSeconds = ptr.To(maxManagedMutationLeaseSeconds + 1)
			default:
				lease.Annotations[field] = "other"
			}
			if err := c.Client.Update(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			version := lease.ResourceVersion
			if _, err := c.publishMaintenanceRequest(context.Background(), up, node, mutationguard.UpgradeHolderIdentity(up)); err == nil {
				t.Fatal("foreign/expired binding accepted")
			}
			if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			if lease.ResourceVersion != version {
				t.Fatal("rejected request modified Lease")
			}
		})
	}
}

func TestManagedMaintenanceReplacesMalformedPersistedToken(t *testing.T) {
	c, _, node, up, lease := managedCoordinatorFixture(t)
	ctx := context.Background()
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	lease.Annotations[managedprotocol.AnnotationMaintenanceRequestVersion] = managedprotocol.Version
	lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "not-a-canonical-uuid"
	lease.Annotations[managedprotocol.AnnotationMaintenanceRequestedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	lease.Annotations[managedprotocol.AnnotationMaintenanceOperationNS] = up.Namespace
	lease.Annotations[managedprotocol.AnnotationMaintenanceOperationName] = up.Name
	lease.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = string(up.UID)
	lease.Annotations[managedprotocol.AnnotationMaintenanceControlRevision] = "7"
	if err := c.Client.Update(ctx, lease); err != nil {
		t.Fatal(err)
	}
	request, err := c.publishMaintenanceRequest(ctx, up, node, mutationguard.UpgradeHolderIdentity(up))
	if err != nil {
		t.Fatal(err)
	}
	if request.Token == "not-a-canonical-uuid" {
		t.Fatal("worker reused malformed persisted maintenance token")
	}
}

type maintenanceConflictClient struct {
	client.Client
	conflicts int
	takeover  bool
}

func (c *maintenanceConflictClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if lease, ok := obj.(*coordv1.Lease); ok && c.conflicts == 0 {
		c.conflicts++
		if c.takeover {
			var current coordv1.Lease
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
				return err
			}
			current.Spec.HolderIdentity = ptr.To("other")
			if err := c.Client.Update(ctx, &current); err != nil {
				return err
			}
		}
		return apierrors.NewConflict(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, lease.Name, errors.New("concurrent writer"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestManagedMaintenancePublishConflictRechecksHolder(t *testing.T) {
	for _, takeover := range []bool{false, true} {
		c, _, node, up, _ := managedCoordinatorFixture(t)
		c.Client = &maintenanceConflictClient{Client: c.Client, takeover: takeover}
		_, err := c.publishMaintenanceRequest(context.Background(), up, node, mutationguard.UpgradeHolderIdentity(up))
		if (err != nil) != takeover {
			t.Fatalf("takeover=%v: %v", takeover, err)
		}
	}
}

func TestManagedOrdinaryWritesWaitForManagerSettlement(t *testing.T) {
	for _, phase := range []ciskov1.DeviceMaintenanceSessionPhase{"", ciskov1.DeviceMaintenanceSessionAcknowledged, ciskov1.DeviceMaintenanceSessionActive, "Unknown", ciskov1.DeviceMaintenanceSessionSettled} {
		t.Run(string(phase), func(t *testing.T) {
			c, device, node, up, lease := managedCoordinatorFixture(t)
			ctx := context.Background()
			request, err := c.publishMaintenanceRequest(ctx, up, node, mutationguard.UpgradeHolderIdentity(up))
			if err != nil {
				t.Fatal(err)
			}
			if phase != "" {
				device.Status.MaintenanceSession = acknowledgement(request, c, node)
				device.Status.MaintenanceSession.Phase = phase
			}
			if err := c.Client.Status().Update(ctx, device); err != nil {
				t.Fatal(err)
			}
			leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: lease.Namespace}
			if err := leaser.Release(ctx, devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily, request.Holder); err != nil {
				t.Fatal(err)
			}
			_, finish, err := c.AcquireWrite(ctx)
			allowed := phase == "" || phase == ciskov1.DeviceMaintenanceSessionSettled
			if (err == nil) != allowed {
				t.Fatalf("phase=%s allowed=%v error=%v", phase, allowed, err)
			}
			if err == nil {
				finish(nil)
			}
			if err := c.Client.Get(ctx, types.NamespacedName{Namespace: lease.Namespace, Name: lease.Name}, lease); err != nil {
				t.Fatal(err)
			}
			if lease.UID != "lease-uid" {
				t.Fatal("canonical managed Lease replaced")
			}
			for key := range lease.Annotations {
				if strings.HasPrefix(key, "topology.cisco.vk/maintenance-") {
					t.Fatal("released Lease retained old maintenance request")
				}
			}
		})
	}
}

func TestManagedOrdinaryWritesRequireReadyBoundTopology(t *testing.T) {
	for name, change := range map[string]func(*ciskov1.CiscoDevice, *corev1.Node){
		"condition absent":  func(d *ciskov1.CiscoDevice, _ *corev1.Node) { d.Status.Conditions = nil },
		"condition false":   func(d *ciskov1.CiscoDevice, _ *corev1.Node) { d.Status.Conditions[0].Status = metav1.ConditionFalse },
		"stale condition":   func(d *ciskov1.CiscoDevice, _ *corev1.Node) { d.Status.Conditions[0].ObservedGeneration = 0 },
		"projection absent": func(d *ciskov1.CiscoDevice, _ *corev1.Node) { d.Status.TopologyProjection = nil },
		"empty hash": func(d *ciskov1.CiscoDevice, n *corev1.Node) {
			d.Status.TopologyProjection.EffectiveLabelHash = ""
			n.Annotations[managedprotocol.AnnotationProjectionHash] = ""
		},
		"different hash": func(_ *ciskov1.CiscoDevice, n *corev1.Node) {
			n.Annotations[managedprotocol.AnnotationProjectionHash] = "other"
		},
		"initializing": func(_ *ciskov1.CiscoDevice, n *corev1.Node) {
			n.Spec.Taints = []corev1.Taint{{Key: managedprotocol.TopologyInitializingTaint, Value: "any", Effect: corev1.TaintEffectNoSchedule}}
		},
		"node status binding": func(d *ciskov1.CiscoDevice, _ *corev1.Node) { d.Status.NodeIdentity.NodeUID = "replacement" },
		"node annotation binding": func(_ *ciskov1.CiscoDevice, n *corev1.Node) {
			n.Annotations[managedprotocol.AnnotationDeviceUID] = "replacement"
		},
		"worker binding": func(_ *ciskov1.CiscoDevice, n *corev1.Node) {
			n.Annotations[managedprotocol.AnnotationWorkerUsername] = "system:serviceaccount:edge:"
		},
		"foreign settled session": func(d *ciskov1.CiscoDevice, n *corev1.Node) {
			d.Status.MaintenanceSession = &ciskov1.DeviceMaintenanceSessionStatus{Phase: ciskov1.DeviceMaintenanceSessionSettled, DeviceUID: "replacement", NodeName: n.Name, NodeUID: string(n.UID)}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, device, node, up, lease := managedCoordinatorFixture(t)
			ctx := context.Background()
			change(device, node)
			if err := c.Client.Status().Update(ctx, device); err != nil {
				t.Fatal(err)
			}
			if err := c.Client.Update(ctx, node); err != nil {
				t.Fatal(err)
			}
			leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: lease.Namespace}
			if err := leaser.Release(ctx, devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily, mutationguard.UpgradeHolderIdentity(up)); err != nil {
				t.Fatal(err)
			}
			if _, finish, err := c.AcquireWrite(ctx); err == nil {
				finish(nil)
				t.Fatal("unready/foreign topology authorized ordinary write")
			}
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			if lease.Spec.HolderIdentity != nil {
				t.Fatal("rejected write acquired Lease")
			}
		})
	}
}

type managedReadFailureClient struct {
	client.Client
	kind string
}

func (c managedReadFailureClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if (c.kind == "device" && key.Namespace == "edge") || (c.kind == "node" && key.Namespace == "") || (c.kind == "lease" && key.Namespace == "leases") {
		return errors.New("injected unavailable API read")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestManagedOrdinaryWritesFailClosedOnMissingLeaseAndReadErrors(t *testing.T) {
	for _, kind := range []string{"device", "node", "lease", "missing lease", "foreign lease"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _, _, lease := managedCoordinatorFixture(t)
			ctx := context.Background()
			switch kind {
			case "missing lease":
				if err := c.Client.Delete(ctx, lease); err != nil {
					t.Fatal(err)
				}
			case "foreign lease":
				if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					t.Fatal(err)
				}
				lease.Annotations[managedprotocol.AnnotationDeviceUID] = "replacement"
				if err := c.Client.Update(ctx, lease); err != nil {
					t.Fatal(err)
				}
			default:
				c.Client = managedReadFailureClient{Client: c.Client, kind: kind}
			}
			if _, finish, err := c.AcquireWrite(ctx); err == nil {
				finish(nil)
				t.Fatal("unreadable/foreign canonical state authorized ordinary write")
			}
		})
	}
}

type managedSessionOnAcquireClient struct {
	client.Client
	device  *ciskov1.CiscoDevice
	session *ciskov1.DeviceMaintenanceSessionStatus
	changed bool
}

func (c *managedSessionOnAcquireClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if lease, ok := obj.(*coordv1.Lease); ok && !c.changed && lease.Spec.HolderIdentity != nil && strings.HasPrefix(*lease.Spec.HolderIdentity, routineHolderPrefix) {
		c.changed = true
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(c.device), c.device); err != nil {
			return err
		}
		c.device.Status.MaintenanceSession = c.session
		if err := c.Client.Status().Update(ctx, c.device); err != nil {
			return err
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

func TestManagedOrdinaryWritesRecheckSessionAfterLeaseAcquisition(t *testing.T) {
	c, device, node, up, lease := managedCoordinatorFixture(t)
	ctx := context.Background()
	request, err := c.publishMaintenanceRequest(ctx, up, node, mutationguard.UpgradeHolderIdentity(up))
	if err != nil {
		t.Fatal(err)
	}
	leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: lease.Namespace}
	if err := leaser.Release(ctx, devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily, request.Holder); err != nil {
		t.Fatal(err)
	}
	c.Client = &managedSessionOnAcquireClient{Client: c.Client, device: device, session: acknowledgement(request, c, node)}
	if _, finish, err := c.AcquireWrite(ctx); err == nil {
		finish(nil)
		t.Fatal("session created during acquisition bypassed shared write fence")
	}
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity != nil {
		t.Fatal("rejected write retained its newly acquired holder")
	}
}
