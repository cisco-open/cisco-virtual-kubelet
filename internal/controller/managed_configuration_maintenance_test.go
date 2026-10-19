// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
	"time"
)

func configurationMaintenanceFixture(t *testing.T) (*CiscoDeviceReconciler, *cvk.CiscoDevice, *core.Node, *ops.ControllerConfigurationReservation, *coordination.Lease) {
	t.Helper()
	r, d, n, _, l := managerMaintenanceFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	reservation := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: d.Namespace, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Controller: ops.ConfigurationReservationReference{Name: "cc", UID: "controller-uid"}, Device: ops.ConfigurationReservationReference{Name: d.Name, UID: string(d.UID)}, DeviceGeneration: d.Generation, Node: ops.ConfigurationReservationReference{Name: n.Name, UID: string(n.UID)}, ConfigGeneration: 1, DeviceWorkerUsername: managedNetworkWorkerUsername(n), DeviceWorkerPod: ops.ConfigurationReservationReference{Name: n.Annotations[managedprotocol.AnnotationNetworkWorkerPodName], UID: n.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID]}, OperationID: strings.Repeat("a", 32), OperationDigest: strings.Repeat("b", 64)}}
	reservation.Name, _ = contract.ReservationName(reservation.Spec.Controller.UID, reservation.Spec.OperationID)
	holder, _ := contract.Holder(d.Namespace, reservation)
	l.Spec.HolderIdentity = &holder
	token := "11111111-1111-4111-8111-111111111111"
	for k, v := range map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: managedprotocol.ConfigurationProtocolVersion, managedprotocol.AnnotationMaintenancePurpose: managedprotocol.MaintenancePurposeConfigurationMutation, managedprotocol.AnnotationMaintenanceSessionToken: token, managedprotocol.AnnotationMaintenanceRequestedAt: now.Format(time.RFC3339), managedprotocol.AnnotationMaintenanceOperationName: reservation.Name, managedprotocol.AnnotationMaintenanceOperationUID: string(reservation.UID), managedprotocol.AnnotationMaintenanceControlRevision: "1"} {
		l.Annotations[k] = v
	}
	reservation.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: l.Namespace, Lease: ops.ConfigurationReservationReference{Name: l.Name, UID: string(l.UID)}, Holder: holder, LeaseTransitions: *l.Spec.LeaseTransitions, GrantedAt: metav1.NewTime(now)}
	reservation.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: reservation.Spec.OperationID, OperationDigest: reservation.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now)}
	if err := r.Client.Create(context.Background(), reservation); err != nil {
		t.Fatal(err)
	}
	d.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionAcknowledged, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: token, DeviceUID: string(d.UID), NodeName: n.Name, NodeUID: string(n.UID), ControlRevision: 1, RequestedAt: metav1.NewTime(now), AcknowledgedAt: &metav1.Time{Time: now}, Operation: cvk.DeviceMaintenanceObjectReference{Namespace: reservation.Namespace, Name: reservation.Name, UID: string(reservation.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: l.Namespace, Name: l.Name, UID: string(l.UID)}, Holder: holder}}
	return r, d, n, reservation, l
}
func TestConfigurationMaintenanceKeepsAcknowledgedClaim(t *testing.T) {
	r, d, n, _, l := configurationMaintenanceFixture(t)
	session, err := r.validateMaintenanceRequest(context.Background(), d, n, l, *l.Spec.HolderIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if session.Phase != cvk.DeviceMaintenanceSessionActive || session.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || session.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation {
		t.Fatal("configuration claim not retained as active")
	}
}
func TestConfigurationMaintenanceRejectsUnboundClaim(t *testing.T) {
	for _, kind := range []string{"missing acknowledgement", "different session", "different node", "different claim", "missing intent", "wrong purpose"} {
		t.Run(kind, func(t *testing.T) {
			r, d, n, reservation, l := configurationMaintenanceFixture(t)
			switch kind {
			case "missing acknowledgement":
				d.Status.MaintenanceSession = nil
			case "different session":
				d.Status.MaintenanceSession.SessionToken = "22222222-2222-4222-8222-222222222222"
			case "different node":
				n.UID = "replacement"
			case "different claim":
				reservation.Status.Claim.OperationDigest = strings.Repeat("c", 64)
			case "missing intent":
				reservation.Status.Claim = nil
				reservation.Status.Grant = nil
			case "wrong purpose":
				l.Annotations[managedprotocol.AnnotationMaintenancePurpose] = managedprotocol.MaintenancePurposeSoftwareMutation
			}
			if kind == "different claim" || kind == "missing intent" {
				if err := r.Client.Update(context.Background(), reservation); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.validateMaintenanceRequest(context.Background(), d, n, l, *l.Spec.HolderIdentity); err == nil {
				t.Fatal("unbound request acknowledged")
			}
		})
	}
}

func TestConfigurationMaintenanceSettlement(t *testing.T) {
	for _, scenario := range []string{"verified", "cancelled", "preparation cancelled", "cancelled busy lease", "cancelled future", "cancelled claimed", "cancelled before acknowledgement", "held", "missing completion", "missing receipt", "different receipt", "replacement lease", "missing lease", "busy lease", "wrong holder", "wrong purpose", "replacement reservation", "claimless completion", "claim before acknowledgement"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, d, n, reservation, lease := configurationMaintenanceFixture(t)
			now := time.Now().UTC().Truncate(time.Second)
			op := cvk.NetworkControllerConfigurationOperation{ID: reservation.Spec.OperationID, ConfigName: "wireless", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "wireless-provision", TargetID: "inventory-id", TargetSerial: "serial-switch", Action: "Update", Phase: "Verified", TaskID: "task-id", SubmittedAt: &metav1.Time{Time: now}}
			digest, err := contract.OperationDigest(&op)
			if err != nil {
				t.Fatal(err)
			}
			reservation.Spec.Config = ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}
			reservation.Spec.ControllerGeneration = op.ControllerGeneration
			reservation.Spec.InventoryID = op.TargetID
			reservation.Spec.OperationDigest = digest
			reservation.Status.Claim.OperationDigest = digest
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: reservation.Namespace, Name: reservation.Spec.Controller.Name, UID: "controller-uid"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: op.Resource, Operation: op}}}}}
			reservation.Status.Completion, err = contract.CompletionForReceipt(reservation, nc, now)
			if err != nil {
				t.Fatal(err)
			}
			reservation.Status.Grant.Phase = "Released"
			empty := ""
			lease.Spec.HolderIdentity = &empty
			switch scenario {
			case "cancelled":
				reservation.Status.Claim = nil
				reservation.Status.Completion = nil
			case "preparation cancelled", "cancelled busy lease", "cancelled future", "cancelled claimed", "cancelled before acknowledgement":
				reservation.Status.Grant = nil
				reservation.Status.Claim = nil
				reservation.Status.Completion = nil
				reservation.Status.PreparationCancelledAt = &metav1.Time{Time: now}
				switch scenario {
				case "cancelled busy lease":
					foreign := "other-holder"
					lease.Spec.HolderIdentity = &foreign
				case "cancelled future":
					reservation.Status.PreparationCancelledAt = &metav1.Time{Time: now.Add(time.Hour)}
				case "cancelled claimed":
					reservation.Status.Claim = &ops.ConfigurationReservationClaim{ClaimedAt: metav1.NewTime(now)}
				case "cancelled before acknowledgement":
					reservation.Status.PreparationCancelledAt = &metav1.Time{Time: now.Add(-time.Hour)}
				}
			case "held":
				reservation.Status.Grant.Phase = "Held"
			case "missing completion":
				reservation.Status.Completion = nil
			case "missing receipt":
				nc.Status.Configuration.ActionApplications = nil
			case "different receipt":
				nc.Status.Configuration.ActionApplications[0].Operation.TaskID = "other"
			case "replacement lease":
				lease.UID = "replacement"
			case "missing lease":
				lease = nil
			case "busy lease":
				holder := "device-write/other"
				lease.Spec.HolderIdentity = &holder
			case "wrong holder":
				reservation.Status.Grant.Holder = "other"
			case "wrong purpose":
				d.Status.MaintenanceSession.Purpose = cvk.DeviceMaintenancePurposeSoftwareMutation
			case "replacement reservation":
				d.Status.MaintenanceSession.Operation.UID = "other"
			case "claimless completion":
				reservation.Status.Claim = nil
			case "claim before acknowledgement":
				d.Status.MaintenanceSession.AcknowledgedAt = &metav1.Time{Time: now.Add(time.Second)}
			}
			if err := r.Client.Update(ctx, reservation); err != nil {
				t.Fatal(err)
			}
			if err := r.Client.Create(ctx, nc); err != nil {
				t.Fatal(err)
			}
			decision := r.retainOrSettleMaintenance(ctx, d, n, lease)
			success := scenario == "verified" || scenario == "cancelled" || scenario == "preparation cancelled"
			if success {
				if decision.err != nil || decision.guard || decision.session.Phase != cvk.DeviceMaintenanceSessionSettled {
					t.Fatalf("not settled: %+v", decision)
				}
			} else if !decision.guard || decision.session.Phase == cvk.DeviceMaintenanceSessionSettled {
				t.Fatalf("unsafe settlement: %+v", decision)
			}
			if d.Status.MaintenanceSession.Phase == cvk.DeviceMaintenanceSessionSettled {
				t.Fatal("mutated input session")
			}
		})
	}
}

func TestConfigurationMaintenanceInitialAcknowledgement(t *testing.T) {
	t.Run("single", func(t *testing.T) { testConfigurationMaintenanceInitialAcknowledgement(t, false) })
	t.Run("group", func(t *testing.T) { testConfigurationMaintenanceInitialAcknowledgement(t, true) })
}

func testConfigurationMaintenanceInitialAcknowledgement(t *testing.T, group bool) {
	ctx := context.Background()
	r, d, n, reservation, lease := configurationMaintenanceFixture(t)
	d.Status.MaintenanceSession = nil
	n.Status.NodeInfo.MachineID = "serial-switch"
	if err := r.Client.Status().Update(ctx, n); err != nil {
		t.Fatal(err)
	}
	reservation.Status = ops.ControllerConfigurationReservationStatus{}
	reservation.Spec.Config = ops.ConfigurationReservationReference{Name: "intent", UID: "config-uid"}
	reservation.Spec.ControllerWorkerPod = ops.ConfigurationReservationReference{Name: "cc-pod", UID: "cc-pod-uid"}
	reservation.Spec.ControllerUsername = "system:serviceaccount:edge:connector"
	reservation.Spec.ControllerGeneration = 1
	reservation.Spec.ManagementAddress = "192.0.2.1"
	reservation.Spec.PhysicalIdentity = "serial-switch"
	reservation.Spec.InventoryID = "inventory-id"
	reservation.Spec.DeviceWorkerRevision = "revision"
	op := &cvk.NetworkControllerConfigurationOperation{ID: reservation.Spec.OperationID, ConfigName: "intent", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "resource", Action: "Update", Phase: "Prepared", TargetID: "inventory-id", TargetSerial: "serial-switch"}
	reservation.Spec.OperationDigest, _ = contract.OperationDigest(op)
	if err := r.Client.Update(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	started, ready := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(-time.Second))
	d.Spec.Address, d.Spec.Driver = "192.0.2.1", cvk.DeviceDriverXE
	if err := r.Client.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.Status.MaintenanceSession = nil
	d.Status.NetworkWorkerRevision = &cvk.DeviceNetworkWorkerRevisionStatus{DesiredRevision: "revision", ObservedRevision: "revision", PodUID: reservation.Spec.DeviceWorkerPod.UID, PodStartTime: &started, PodReadyTime: &ready}
	if err := r.Client.Status().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: reservation.Spec.DeviceWorkerPod.Name, Namespace: "edge", UID: "network-pod-uid", Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: "revision"}}, Spec: core.PodSpec{ServiceAccountName: "cisco-vk-network-management"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: "edge", UID: "controller-uid", Generation: 1}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op}}}
	nc.Spec.Type = "catalyst-center"
	nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "intent", ConfigUID: "config-uid"}}
	intent := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "intent", Namespace: "edge", UID: "config-uid", Generation: 1}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: "cc"}, Scope: "global", ManagedSections: []string{"inventory"}, Mode: config.NetworkControllerApplyModeApply, ModelSource: config.NetworkControllerNetAsCodeModelSource{Resolved: true, ModelVersion: "0.5.0"}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{}`)}}}}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: "connector", DeploymentName: "connector", ReadyReplicas: 1}
	replicas, controlled := int32(1), true
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"worker": "connector"}, Annotations: map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Args: []string{"controller-worker", "--controller-namespace=edge", "--controller-name=cc", "--controller-uid=controller-uid", "--controller-generation=1", "--controller-type=catalyst-center"}}}}}
	deployment := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "edge", UID: "deployment-uid", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": "cc", "cisco.vk/network-controller-uid": "controller-uid"}}, Spec: apps.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: *template.DeepCopy()}, Status: apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "replica", Namespace: "edge", UID: "replica-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "connector", UID: "deployment-uid", Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: *template.DeepCopy()}}
	connector := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "cc-pod", Namespace: "edge", UID: "cc-pod-uid", Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "replica", UID: "replica-uid", Controller: &controlled}}}, Spec: *template.Spec.DeepCopy(), Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
	for _, obj := range []client.Object{pod, nc, intent, deployment, rs, connector} {
		if err := r.Client.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	validate := func() (*cvk.DeviceMaintenanceSessionStatus, error) {
		return r.validateMaintenanceRequest(ctx, d, n, lease, *lease.Spec.HolderIdentity)
	}
	if group {
		op.TargetSerial = ""
		var err error
		op, err = contract.WithGroupIdentity(op, []contract.GroupTargetIdentity{{InventoryID: "inventory-id", PhysicalIdentity: "serial-switch", Device: reservation.Spec.Device, Node: reservation.Spec.Node}})
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.Operation = op
		if err := r.Client.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		d.Spec.PhysicalIdentity = "serial-switch"
		d.Generation = 1
		if err := r.Client.Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(d.UID), NodeName: n.Name, NodeUID: string(n.UID), PhysicalIdentity: "serial-switch"}
		if err := r.Client.Status().Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		candidates, err := contract.GroupReservationCandidates(nc, []contract.GroupMemberSnapshot{{Device: d, Node: n, Worker: pod}}, connector, now)
		if err != nil {
			t.Fatal(err)
		}
		reservation = candidates[0]
		reservation.UID = "group-reservation-uid"
		if err := r.Client.Create(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		holder, err := contract.Holder(d.Namespace, reservation)
		if err != nil {
			t.Fatal(err)
		}
		lease.Spec.HolderIdentity = &holder
		lease.Annotations[managedprotocol.AnnotationMaintenanceOperationName] = reservation.Name
		lease.Annotations[managedprotocol.AnnotationMaintenanceOperationUID] = string(reservation.UID)
		at, err := time.Parse(time.RFC3339Nano, lease.Annotations[managedprotocol.AnnotationMaintenanceRequestedAt])
		if err != nil {
			t.Fatal(err)
		}
		operation := cvk.DeviceMaintenanceObjectReference{Namespace: reservation.Namespace, Name: reservation.Name, UID: string(reservation.UID)}
		validate = func() (*cvk.DeviceMaintenanceSessionStatus, error) {
			return r.validateGroupConfigurationMaintenanceRequest(ctx, d, n, lease, holder, lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken], at, 1, operation, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil })
		}
		if _, err := r.validateMaintenanceRequest(ctx, d, n, lease, holder); err == nil {
			t.Fatal("public entry accepted group without authorization")
		}
		if _, err := r.validateGroupConfigurationMaintenanceRequest(ctx, d, n, lease, holder, lease.Annotations[managedprotocol.AnnotationMaintenanceSessionToken], at, 1, operation, nil); err == nil {
			t.Fatal("missing group admission accepted")
		}
		r.APIReader = r.Client
		r.AuthorizeConfigurationReservation = func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
		validate = func() (*cvk.DeviceMaintenanceSessionStatus, error) {
			return r.validateMaintenanceRequest(ctx, d, n, lease, holder)
		}

	}
	session, err := validate()
	if err != nil {
		t.Fatal(err)
	}
	if session.Phase != cvk.DeviceMaintenanceSessionAcknowledged || session.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || session.Operation.UID != string(reservation.UID) || session.Lease.UID != string(lease.UID) {
		t.Fatalf("wrong acknowledgement: %+v", session)
	}
	// A changed source invalidates a new acknowledgement even with the same
	// reservation and exact worker ownership chain.
	intent.Generation++
	if err := r.Client.Update(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if _, err := validate(); err == nil {
		t.Fatal("acknowledged changed desired intent")
	}
	if group {
		// Once claimed, preserve an existing acknowledgement despite the changed
		// desired source. Round-trip status as Kubernetes does for metav1.Time.
		d.Status.MaintenanceSession = session
		if err := r.Client.Status().Update(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
			t.Fatal(err)
		}
		at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		reservation.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: lease.Namespace, Lease: ops.ConfigurationReservationReference{Name: lease.Name, UID: string(lease.UID)}, Holder: *lease.Spec.HolderIdentity, LeaseTransitions: *lease.Spec.LeaseTransitions, GrantedAt: at}
		// A Held but unclaimed grant must preserve its original acknowledgement.
		if err := r.Client.Update(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		if err := r.Client.Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
		if _, err := validate(); err == nil {
			t.Fatal("held grant accepted changed intent")
		}
		intent.Generation--
		if err := r.Client.Update(ctx, intent); err != nil {
			t.Fatal(err)
		}
		acknowledged := d.Status.MaintenanceSession.DeepCopy()
		for range 2 {
			held, err := validate()
			if err != nil {
				t.Fatal("held session retention", err)
			}
			if held.Phase != cvk.DeviceMaintenanceSessionAcknowledged || !held.AcknowledgedAt.Equal(acknowledged.AcknowledgedAt) {
				t.Fatal("held session acknowledgement changed")
			}
		}
		d.Status.MaintenanceSession = nil
		if _, err := validate(); err == nil {
			t.Fatal("held grant manufactured a session")
		}
		d.Status.MaintenanceSession = acknowledged.DeepCopy()
		d.Status.MaintenanceSession.SessionToken = "foreign"
		if _, err := validate(); err == nil {
			t.Fatal("held grant accepted a foreign session")
		}
		d.Status.MaintenanceSession = acknowledged.DeepCopy()
		savedAuthorization := r.AuthorizeConfigurationReservation
		r.AuthorizeConfigurationReservation = nil
		if _, err := validate(); err == nil {
			t.Fatal("held session bypassed authorization")
		}
		r.AuthorizeConfigurationReservation = func(context.Context, *ops.ControllerConfigurationReservation) error {
			intent.Generation++
			return r.Client.Update(ctx, intent)
		}
		if _, err := validate(); err == nil {
			t.Fatal("held session ignored source change during authorization")
		}
		r.AuthorizeConfigurationReservation = savedAuthorization
		reservation.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: at}
		if err := r.Client.Update(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		if err := r.Client.Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
		old := d.Status.MaintenanceSession.DeepCopy()
		for range 2 {
			current, err := r.validateGroupConfigurationMaintenanceRequest(ctx, d, n, lease, *lease.Spec.HolderIdentity, old.SessionToken, old.RequestedAt.Time, 1, old.Operation, nil)
			if err != nil {
				t.Fatal("claimed group retention", err)
			}
			if current.Phase != cvk.DeviceMaintenanceSessionActive || !current.AcknowledgedAt.Equal(old.AcknowledgedAt) {
				t.Fatal("claim retention rewrote acknowledgement")
			}
			d.Status.MaintenanceSession = current
		}
		d.Status.MaintenanceSession = nil
		if _, err := r.validateGroupConfigurationMaintenanceRequest(ctx, d, n, lease, *lease.Spec.HolderIdentity, old.SessionToken, old.RequestedAt.Time, 1, old.Operation, nil); err == nil {
			t.Fatal("claimed group manufactured initial acknowledgement")
		}
		op.Phase, op.TaskID = "Verified", "group-task"
		op.SubmittedAt = &at
		nc.Status.Configuration.Operation = op
		nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: op.Resource, Operation: *op.DeepCopy(), VerifiedAt: &at}}
		if err := r.Client.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		completion, err := contract.GroupCompletionsForReceipt(nc, []*ops.ControllerConfigurationReservation{reservation}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		reservation.Status.Completion = completion[reservation.UID]
		reservation.Status.Grant.Phase = "Released"
		if err := r.Client.Update(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		empty := ""
		lease.Spec.HolderIdentity = &empty
		if err := r.Client.Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
		decision := managedMaintenanceDecision{session: old.DeepCopy(), guard: true}
		denied := r.settleGroupConfigurationMaintenance(ctx, d, n, lease, decision, nil)
		if denied.err == nil || !denied.guard {
			t.Fatal("group settled without manager authorization")
		}
		changed := r.settleGroupConfigurationMaintenance(ctx, d, n, lease, decision, func(context.Context, *ops.ControllerConfigurationReservation) error {
			copy := lease.DeepCopy()
			other := "new-holder"
			copy.Spec.HolderIdentity = &other
			return r.Client.Update(ctx, copy)
		})
		if changed.err == nil || !changed.guard {
			t.Fatal("settled a newly acquired lease")
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
			t.Fatal(err)
		}
		lease.Spec.HolderIdentity = &empty
		if err := r.Client.Update(ctx, lease); err != nil {
			t.Fatal(err)
		}
		settled := r.settleConfigurationMaintenance(ctx, d, n, lease, decision)
		if settled.err != nil || settled.guard || settled.session.Phase != cvk.DeviceMaintenanceSessionSettled {
			t.Fatal("verified group settlement", settled.err)
		}
		// A missing member completion cannot be replaced by the verified task.
		reservation.Status.Completion = nil
		if err := r.Client.Update(ctx, reservation); err != nil {
			t.Fatal(err)
		}
		decision.session = old.DeepCopy()
		refused := r.settleGroupConfigurationMaintenance(ctx, d, n, lease, decision, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil })
		if refused.err == nil || !refused.guard {
			t.Fatal("settled missing group completion")
		}
		// Reconstruct only persisted recovery evidence in a fresh client, with
		// reservation status semantics enabled for the retention publisher.
		reservation.Status.Completion = completion[reservation.UID]
		d.Status.MaintenanceSession = settled.session.DeepCopy()
		recovered := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(d, reservation, nc).WithObjects(d, n, lease, reservation, nc).Build()
		r.Client, r.APIReader = recovered, recovered
		authorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
		if err := r.retainConfigurationSettlement(ctx, d); err != nil {
			t.Fatal("retain settled group", err)
		}
		var retained ops.ControllerConfigurationReservation
		if err := recovered.Get(ctx, client.ObjectKeyFromObject(reservation), &retained); err != nil {
			t.Fatal(err)
		}
		if retained.Status.ManagerSettledAt == nil {
			t.Fatal("missing retained manager settlement")
		}
		stamp := retained.Status.ManagerSettledAt.DeepCopy()
		if err := r.retainConfigurationSettlement(ctx, d); err != nil {
			t.Fatal("idempotent group settlement", err)
		}
		if err := recovered.Get(ctx, client.ObjectKeyFromObject(reservation), &retained); err != nil {
			t.Fatal(err)
		}
		if !retained.Status.ManagerSettledAt.Equal(stamp) {
			t.Fatal("manager settlement timestamp changed")
		}
		for _, mode := range []string{"granted", "ungranted", "held", "missing-ack", "submitted", "denied"} {
			t.Run("cancel-"+mode, func(t *testing.T) {
				controller, row, device, idle := nc.DeepCopy(), retained.DeepCopy(), d.DeepCopy(), lease.DeepCopy()
				cancelAt := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				controller.Status.Configuration.Operation.Phase = "Cancelling"
				controller.Status.Configuration.Operation.TaskID = ""
				controller.Status.Configuration.Operation.SubmittedAt = nil
				controller.Status.Configuration.Operation.CancellationRequestedAt = &cancelAt
				controller.Status.Configuration.ActionApplications = nil
				row.Status.Completion, row.Status.ManagerSettledAt = nil, nil
				row.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: row.Spec.OperationID, OperationDigest: row.Spec.OperationDigest, RequestedAt: cancelAt, AcknowledgedAt: cancelAt}
				if mode == "ungranted" {
					row.Status.Grant, row.Status.Claim = nil, nil
				}
				if mode == "held" {
					row.Status.Grant.Phase = "Held"
				}
				if mode == "missing-ack" {
					row.Status.GroupCancellation = nil
				}
				if mode == "submitted" {
					controller.Status.Configuration.Operation.SubmittedAt = &cancelAt
				}
				device.Status.MaintenanceSession = old.DeepCopy()
				store := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(device, row, controller).WithObjects(controller, row, device, n, idle).Build()
				copy := *r
				copy.Client, copy.APIReader = store, store
				allow := authorize
				if mode == "denied" {
					allow = nil
				}
				copy.AuthorizeConfigurationReservation = allow
				out := copy.settleConfigurationMaintenance(ctx, device, n, idle, managedMaintenanceDecision{session: old.DeepCopy(), guard: true})
				valid := mode == "granted" || mode == "ungranted"
				if (out.err == nil) != valid || out.guard == valid {
					t.Fatal("cancel settlement", mode, out.err)
				}
				if !valid {
					return
				}
				device.Status.MaintenanceSession = out.session
				if err := store.Status().Update(ctx, device); err != nil {
					t.Fatal(err)
				}
				if err := copy.retainConfigurationSettlement(ctx, device); err != nil {
					t.Fatal("cancel retention", err)
				}
				if err := store.Get(ctx, client.ObjectKeyFromObject(row), row); err != nil {
					t.Fatal(err)
				}
				if row.Status.ManagerSettledAt == nil || row.Status.Completion != nil {
					t.Fatal("cancellation settlement evidence missing or fabricated")
				}
			})
		}
	}
}

func TestConfigurationSettlementRetainedBeforeNextSession(t *testing.T) {
	ctx := context.Background()
	r, d, _, reservation, _ := configurationMaintenanceFixture(t)
	reservation.Status.Claim = nil
	reservation.Status.Grant.Phase = "Released"
	d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(d, reservation).WithObjects(d, reservation).Build()
	r.Client, r.APIReader = c, c
	if err := r.retainConfigurationSettlement(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.ManagerSettledAt == nil {
		t.Fatal("manager settlement was not retained")
	}
	stamp := reservation.Status.ManagerSettledAt.DeepCopy()
	if err := r.retainConfigurationSettlement(ctx, d); err != nil {
		t.Fatal("retry", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	if !reservation.Status.ManagerSettledAt.Equal(stamp) {
		t.Fatal("manager rewrote settlement timestamp")
	}
	d.Status.MaintenanceSession.Operation.UID = "replacement"
	if err := r.retainConfigurationSettlement(ctx, d); err == nil {
		t.Fatal("replacement reservation was accepted")
	}
}

func TestCancelledConfigurationSettlementRetained(t *testing.T) {
	ctx := context.Background()
	r, d, _, reservation, _ := configurationMaintenanceFixture(t)
	reservation.Status.Grant = nil
	reservation.Status.Claim = nil
	reservation.Status.PreparationCancelledAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}
	d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithStatusSubresource(d, reservation).WithObjects(d, reservation).Build()
	r.Client, r.APIReader = c, c
	if err := r.retainConfigurationSettlement(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	if err := contract.ValidateManagerSettlement(reservation, time.Now()); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.Grant != nil {
		t.Fatal("cancellation fabricated a grant")
	}
}
