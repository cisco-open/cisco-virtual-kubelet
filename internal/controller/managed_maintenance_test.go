// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func managerMaintenanceFixture(t *testing.T) (*CiscoDeviceReconciler, *ciskov1.CiscoDevice, *corev1.Node, *ops.IOSXESoftwareUpgrade, *coordv1.Lease) {
	t.Helper()
	device := newDevice("switch", "edge")
	device.UID = "device-uid"
	device.Spec.NodeName = "separate-node"
	device.Spec.PhysicalIdentity = "serial-switch"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "separate-node", UID: "node-uid", Annotations: map[string]string{
		managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: device.Namespace,
		managedprotocol.AnnotationDeviceName: device.Name, managedprotocol.AnnotationDeviceUID: string(device.UID),
		managedprotocol.AnnotationNodeName: "separate-node", managedprotocol.AnnotationNodeUID: "node-uid",
		managedprotocol.AnnotationWorkerUsername: "system:serviceaccount:edge:" + managedWorkerServiceAccountName(device),
		managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version,
	}}}
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	device.Status.NodeIdentity = &ciskov1.DeviceNodeIdentityStatus{
		DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID), PhysicalIdentity: "serial-switch",
	}
	leaf := &ops.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{Namespace: device.Namespace, Name: "upgrade", UID: "leaf-uid", Annotations: map[string]string{}}}
	leaf.Spec.DeviceRef.Name = device.Name
	for key, value := range node.Annotations {
		leaf.Annotations[key] = value
	}
	for key, value := range map[string]string{managedprotocol.AnnotationCampaignUID: "campaign-uid", managedprotocol.AnnotationLedgerUID: "ledger-uid", managedprotocol.AnnotationPlanHash: "sha256:" + strings.Repeat("a", 64), managedprotocol.AnnotationReservationID: "reservation"} {
		leaf.Annotations[key] = value
	}
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	leaf.Status.ManagerAdmission = &ops.UpgradeManagerAdmissionStatus{ProtocolVersion: ops.ManagedUpgradeProtocolRolloutV1,
		State: ops.UpgradeManagerAdmissionGranted, CampaignUID: "campaign-uid", LedgerUID: "ledger-uid", PlanHash: leaf.Annotations[managedprotocol.AnnotationPlanHash], ReservationID: "reservation",
		LeafUID: string(leaf.UID), DeviceUID: string(device.UID), NodeUID: string(node.UID), ControlRevision: ptr.To[int64](7), UpdatedAt: now}
	leaf.Status.ManagerControl = &ops.UpgradeManagerControlStatus{Revision: 7, UpdatedAt: now}
	leaf.Status.WorkerControl = &ops.UpgradeWorkerControlStatus{ObservedAdmissionState: ops.UpgradeManagerAdmissionGranted, ObservedControlRevision: 7, EffectiveState: ops.UpgradeWorkerControlReady, UpdatedAt: now}
	annotations := map[string]string{
		devicecoordination.RetainLeaseAnnotation: "true",
		managedprotocol.AnnotationLeasePurpose:   managedprotocol.LeasePurposeDeviceMutation,
	}
	for key, value := range node.Annotations {
		annotations[key] = value
	}
	for key, value := range map[string]string{
		managedprotocol.AnnotationMaintenanceRequestVersion: managedprotocol.Version,
		managedprotocol.AnnotationMaintenanceSessionToken:   "00000000-0000-0000-0000-000000000001",
		managedprotocol.AnnotationMaintenanceRequestedAt:    now.Format(time.RFC3339),
		managedprotocol.AnnotationMaintenanceOperationNS:    leaf.Namespace, managedprotocol.AnnotationMaintenanceOperationName: leaf.Name,
		managedprotocol.AnnotationMaintenanceOperationUID: string(leaf.UID), managedprotocol.AnnotationMaintenanceControlRevision: "7",
	} {
		annotations[key] = value
	}
	lease := &coordv1.Lease{ObjectMeta: metav1.ObjectMeta{
		Namespace: "leases", Name: engine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily),
		UID: "lease-uid", Annotations: annotations,
		Labels: map[string]string{"cisco.vk/device": devicecoordination.DeviceKey(device.Namespace, device.Name), "cisco.vk/family": devicecoordination.MutationLeaseFamily},
	}, Spec: coordv1.LeaseSpec{HolderIdentity: ptr.To(mutationguard.UpgradeHolderIdentity(leaf)), AcquireTime: ptr.To(metav1.NewMicroTime(now.Time)), RenewTime: ptr.To(metav1.NewMicroTime(now.Time)), LeaseDurationSeconds: ptr.To[int32](3600), LeaseTransitions: ptr.To[int32](1)}}
	scheme := newTestScheme(t)
	if err := ops.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(device, leaf, node).WithObjects(device, node, leaf, lease).Build()
	r := &CiscoDeviceReconciler{Client: c, APIReader: c, Scheme: scheme, LeaseNamespace: lease.Namespace}
	return r, device, node, leaf, lease
}

func TestManagedMaintenancePersistsGuardAcknowledgementOnUnchangedTopology(t *testing.T) {
	r, device, node, leaf, _ := managerMaintenanceFixture(t)
	ctx := context.Background()
	if err := r.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(node), node); err != nil {
		t.Fatal(err)
	}
	// Seed the already-projected status first: only the maintenance session
	// changes on the next tick, so copying its baseline too late loses the ack.
	if err := r.patchManagedTopologyStatus(ctx, device, node, "projection", managedMaintenanceDecision{status: metav1.ConditionTrue, reason: "Idle", message: "idle"}); err != nil {
		t.Fatal(err)
	}
	decision := r.resolveManagedMaintenance(ctx, device, node)
	if decision.err != nil || !decision.guard || decision.session == nil {
		t.Fatalf("request not acknowledged: %+v", decision)
	}
	if err := r.reconcileManagedNodeMetadata(ctx, device, node, nil, &topologyrollout.ParsedAdminPolicy{}, "projection", decision.guard); err != nil {
		t.Fatal(err)
	}
	if err := r.patchManagedTopologyStatus(ctx, device, node, "projection", decision); err != nil {
		t.Fatal(err)
	}
	var persisted ciskov1.CiscoDevice
	if err := r.Get(ctx, client.ObjectKeyFromObject(device), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status.MaintenanceSession == nil || !meta.IsStatusConditionTrue(persisted.Status.Conditions, ciskov1.CiscoDeviceConditionMaintenanceReady) {
		t.Fatal("maintenance session/condition was not persisted")
	}
	c := &maintenance.Coordinator{Client: r.Client, Namespace: device.Namespace, DeviceName: device.Name, DeviceUID: string(device.UID), NodeName: node.Name, LeaseNamespace: r.LeaseNamespace, ManagedTopology: true}
	if err := c.BeforeSoftwareUpgradeMutation(ctx, leaf); err != nil {
		t.Fatalf("worker could not use manager's persisted guard: %v", err)
	}
	// A manager restart re-acknowledges the same token without changing its
	// original acknowledgement time or operation/Lease incarnation.
	decision = r.resolveManagedMaintenance(ctx, &persisted, node)
	if decision.err != nil || !decision.session.AcknowledgedAt.Equal(persisted.Status.MaintenanceSession.AcknowledgedAt) {
		t.Fatal("manager restart changed acknowledgement identity")
	}
}

func TestManagedMaintenanceRejectsInvalidGrantBindings(t *testing.T) {
	for name, mutate := range map[string]func(*ops.IOSXESoftwareUpgrade){
		"target":      func(l *ops.IOSXESoftwareUpgrade) { l.Spec.DeviceRef.Name = "other" },
		"leaf UID":    func(l *ops.IOSXESoftwareUpgrade) { l.UID = "replacement" },
		"device UID":  func(l *ops.IOSXESoftwareUpgrade) { l.Status.ManagerAdmission.DeviceUID = "replacement" },
		"node UID":    func(l *ops.IOSXESoftwareUpgrade) { l.Status.ManagerAdmission.NodeUID = "replacement" },
		"protocol":    func(l *ops.IOSXESoftwareUpgrade) { l.Status.ManagerAdmission.ProtocolVersion = "future" },
		"reservation": func(l *ops.IOSXESoftwareUpgrade) { l.Annotations[managedprotocol.AnnotationReservationID] = "other" },
		"campaign":    func(l *ops.IOSXESoftwareUpgrade) { l.Status.ManagerAdmission.CampaignUID = "other" },
		"pending": func(l *ops.IOSXESoftwareUpgrade) {
			l.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionPending
		},
		"revoked": func(l *ops.IOSXESoftwareUpgrade) {
			l.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionRevoked
		},
		"pause":           func(l *ops.IOSXESoftwareUpgrade) { l.Status.ManagerControl.Pause = true },
		"cancel":          func(l *ops.IOSXESoftwareUpgrade) { l.Status.ManagerControl.Cancel = true },
		"stale worker":    func(l *ops.IOSXESoftwareUpgrade) { l.Status.WorkerControl.ObservedControlRevision-- },
		"stale admission": func(l *ops.IOSXESoftwareUpgrade) { *l.Status.ManagerAdmission.ControlRevision++ },
		"unobserved admission": func(l *ops.IOSXESoftwareUpgrade) {
			l.Status.WorkerControl.ObservedAdmissionState = ops.UpgradeManagerAdmissionPending
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, device, node, leaf, _ := managerMaintenanceFixture(t)
			mutate(leaf)
			if err := validateMaintenanceLeafBinding(device, node, leaf, 7); err == nil {
				t.Fatal("invalid grant accepted")
			}
		})
	}
}

func TestManagedMaintenanceRetainsClaimedSessionAfterRevocation(t *testing.T) {
	r, device, node, leaf, lease := managerMaintenanceFixture(t)
	ctx := context.Background()
	if err := r.Get(ctx, client.ObjectKeyFromObject(leaf), leaf); err != nil {
		t.Fatal(err)
	}
	leaf.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionRevoked
	leaf.Status.ManagerControl.Cancel = true
	leaf.Status.WorkerControl.ObservedAdmissionState = ops.UpgradeManagerAdmissionRevoked
	leaf.Status.WorkerControl.EffectiveState = ops.UpgradeWorkerControlClaimed
	leaf.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{Stage: ops.UpgradeManagedMutationPrimaryInstall}}
	if err := r.Status().Update(ctx, leaf); err != nil {
		t.Fatal(err)
	}
	decision := r.resolveManagedMaintenance(ctx, device, node)
	if decision.err != nil || !decision.guard || decision.session.Phase != ciskov1.DeviceMaintenanceSessionActive {
		t.Fatalf("claimed mutation lost observation guard: %+v", decision)
	}
	device.Status.MaintenanceSession = decision.session
	if err := r.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "00000000-0000-0000-0000-000000000002"
	if err := r.Update(ctx, lease); err != nil {
		t.Fatal(err)
	}
	decision = r.resolveManagedMaintenance(ctx, device, node)
	if decision.err == nil || !decision.guard {
		t.Fatal("conflicting session replaced unresolved session")
	}
}

func TestManagedMaintenanceSettlesOnlyExactManagerSettledOutcome(t *testing.T) {
	for _, phase := range []ops.UpgradePhase{ops.UpgradePhaseSucceeded, ops.UpgradePhasePreflightFailed, ops.UpgradePhaseValidationFailed, ops.UpgradePhaseRebootTimeout, ops.UpgradePhaseCancelled} {
		t.Run(string(phase), func(t *testing.T) {
			r, device, node, leaf, lease := managerMaintenanceFixture(t)
			ctx := context.Background()
			decision := r.resolveManagedMaintenance(ctx, device, node)
			device.Status.MaintenanceSession = decision.session
			if err := r.Get(ctx, client.ObjectKeyFromObject(leaf), leaf); err != nil {
				t.Fatal(err)
			}
			leaf.Status.Phase = phase
			if err := r.Status().Update(ctx, leaf); err != nil {
				t.Fatal(err)
			}
			leaser := &engine.FamilyLeaser{Client: r.Client, Namespace: lease.Namespace}
			if err := leaser.Release(ctx, devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily, mutationguard.UpgradeHolderIdentity(leaf)); err != nil {
				t.Fatal(err)
			}
			if decision = r.resolveManagedMaintenance(ctx, device, node); !decision.guard {
				t.Fatal("terminal worker phase bypassed manager health settlement")
			}
			leaf.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionSettled
			leaf.Status.ManagerAdmission.DeviceUID = "replacement"
			if err := r.Status().Update(ctx, leaf); err != nil {
				t.Fatal(err)
			}
			if decision = r.resolveManagedMaintenance(ctx, device, node); !decision.guard {
				t.Fatal("foreign admission settled session")
			}
			leaf.Status.ManagerAdmission.DeviceUID = string(device.UID)
			if err := r.Status().Update(ctx, leaf); err != nil {
				t.Fatal(err)
			}
			decision = r.resolveManagedMaintenance(ctx, device, node)
			if decision.err != nil || decision.guard || decision.session.Phase != ciskov1.DeviceMaintenanceSessionSettled {
				t.Fatalf("exact settled outcome retained guard: %+v", decision)
			}
		})
	}
}

func TestManagedMaintenanceRejectsStaleLeaseAndControlRequest(t *testing.T) {
	for _, invalid := range []string{"expired", "node UID", "worker protocol", "control", "operation UID", "future time", "lease UID", "session token", "acquire time", "transitions", "excess duration"} {
		t.Run(invalid, func(t *testing.T) {
			r, device, node, _, lease := managerMaintenanceFixture(t)
			switch invalid {
			case "expired":
				lease.Spec.RenewTime = ptr.To(metav1.NewMicroTime(time.Now().Add(-2 * time.Hour)))
			case "node UID":
				lease.Annotations[managedprotocol.AnnotationNodeUID] = "replacement"
			case "worker protocol":
				lease.Annotations[managedprotocol.AnnotationWorkerProtocol] = "future"
			case "control":
				lease.Annotations[managedprotocol.AnnotationMaintenanceControlRevision] = strconv.Itoa(6)
			case "operation UID":
				lease.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = "replacement"
			case "future time":
				lease.Annotations[managedprotocol.AnnotationMaintenanceRequestedAt] = time.Now().Add(time.Hour).Format(time.RFC3339)
			case "lease UID":
				lease.UID = ""
			case "session token":
				lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "not-a-canonical-uuid"
			case "acquire time":
				lease.Spec.AcquireTime = nil
			case "transitions":
				lease.Spec.LeaseTransitions = ptr.To[int32](0)
			case "excess duration":
				lease.Spec.LeaseDurationSeconds = ptr.To(maxManagedMutationLeaseSeconds + 1)
			}
			if _, err := r.validateMaintenanceRequest(context.Background(), device, node, lease, *lease.Spec.HolderIdentity); err == nil {
				t.Fatal("invalid request acknowledged")
			}
		})
	}
}

func TestManagedMaintenanceOrphanRequestMetadataFailsClosed(t *testing.T) {
	for _, holder := range []string{"", "device-write/00000000-0000-4000-8000-000000000001"} {
		t.Run(holder, func(t *testing.T) {
			r, device, node, _, lease := managerMaintenanceFixture(t)
			ctx := context.Background()
			if err := r.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			if holder == "" {
				lease.Spec.HolderIdentity = nil
				lease.Spec.AcquireTime = nil
				lease.Spec.RenewTime = nil
				lease.Spec.LeaseDurationSeconds = nil
			} else {
				lease.Spec.HolderIdentity = ptr.To(holder)
			}
			if err := r.Update(ctx, lease); err != nil {
				t.Fatal(err)
			}
			decision := r.resolveManagedMaintenance(ctx, device, node)
			if !decision.guard || decision.err == nil {
				t.Fatalf("orphan maintenance metadata did not retain the Node guard: %+v", decision)
			}
		})
	}
}

func TestManagedMaintenanceCannotReuseSessionAcrossLeaseIncarnations(t *testing.T) {
	r, device, node, _, _ := managerMaintenanceFixture(t)
	decision := r.resolveManagedMaintenance(context.Background(), device, node)
	if decision.err != nil || decision.session == nil {
		t.Fatalf("initial request: %+v", decision)
	}
	device.Status.MaintenanceSession = decision.session
	device.Status.MaintenanceSession.Lease.UID = "old-lease-incarnation"
	decision = r.resolveManagedMaintenance(context.Background(), device, node)
	if decision.err == nil || !decision.guard || decision.session.Lease.UID != "old-lease-incarnation" {
		t.Fatal("recreated Lease inherited original session acknowledgement")
	}
}

type managedLeaseAdoptionConflictClient struct {
	client.Client
}

func (c managedLeaseAdoptionConflictClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if lease, ok := obj.(*coordv1.Lease); ok {
		var current coordv1.Lease
		if err := c.Client.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
			return err
		}
		current.Spec.HolderIdentity = ptr.To("legacy-operation")
		if err := c.Client.Update(ctx, &current); err != nil {
			return err
		}
		return apierrors.NewConflict(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, lease.Name, errors.New("holder acquired during adoption"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestManagedMutationLeaseAdoptionPreservesActiveOrForeignObjects(t *testing.T) {
	for _, state := range []string{"already managed", "empty legacy", "active legacy", "expired legacy", "timed empty legacy", "request metadata legacy", "owned legacy", "foreign binding", "foreign labels", "adoption conflict"} {
		t.Run(state, func(t *testing.T) {
			r, device, node, _, lease := managerMaintenanceFixture(t)
			ctx := context.Background()
			if err := r.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "already managed":
			case "foreign binding":
				lease.Annotations[managedprotocol.AnnotationDeviceUID] = "replacement"
			default:
				lease.Annotations = map[string]string{"operator.example/keep": "true"}
				if state == "foreign labels" {
					lease.Spec = coordv1.LeaseSpec{}
					lease.Labels["cisco.vk/device"] = "other/device"
				}
				if state == "empty legacy" || state == "adoption conflict" {
					lease.Spec = coordv1.LeaseSpec{}
				}
				if state == "expired legacy" {
					lease.Spec.RenewTime = ptr.To(metav1.NewMicroTime(time.Now().Add(-2 * time.Hour)))
				}
				if state == "timed empty legacy" {
					lease.Spec.HolderIdentity = nil
				}
				if state == "request metadata legacy" {
					lease.Spec = coordv1.LeaseSpec{}
					lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken] = "leftover-request-token"
				}
				if state == "owned legacy" {
					lease.Spec = coordv1.LeaseSpec{}
					lease.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "foreign", UID: "foreign-uid"}}
				}
			}
			if err := r.Update(ctx, lease); err != nil {
				t.Fatal(err)
			}
			before := lease.DeepCopy()
			if state == "adoption conflict" {
				r.Client = managedLeaseAdoptionConflictClient{Client: r.Client}
			}
			err := r.ensureManagedMutationLease(ctx, device, node)
			allowed := state == "already managed" || state == "empty legacy"
			if (err == nil) != allowed {
				t.Fatalf("state=%s error=%v", state, err)
			}
			if err := r.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			if lease.UID != before.UID {
				t.Fatal("manager replaced canonical Lease")
			}
			if state == "empty legacy" {
				if lease.Annotations[managedprotocol.AnnotationManaged] != "true" || lease.Annotations["operator.example/keep"] != "true" {
					t.Fatal("empty legacy adoption lost binding/unrelated metadata")
				}
			} else if state == "adoption conflict" {
				if lease.Annotations[managedprotocol.AnnotationManaged] == "true" || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "legacy-operation" {
					t.Fatal("conflicting adoption overwrote active legacy Lease")
				}
			} else if lease.ResourceVersion != before.ResourceVersion {
				t.Fatal("existing/rejected canonical Lease changed")
			}
		})
	}
}
