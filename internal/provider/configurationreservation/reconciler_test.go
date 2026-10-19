// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package configurationreservation

import (
	"context"
	"errors"
	"fmt"
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strings"
	"testing"
	"time"
)

func recoveryFixture(t *testing.T) (*Reconciler, *ops.ControllerConfigurationReservation, *coordination.Lease) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	reservation := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "reservation", Namespace: "lab", UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device-uid"}, DeviceWorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "pod-uid"}, OperationID: strings.Repeat("a", 32), OperationDigest: strings.Repeat("b", 64), Controller: ops.ConfigurationReservationReference{Name: "cc", UID: "controller-uid"}}}
	reservation.Name, _ = contract.ReservationName(reservation.Spec.Controller.UID, reservation.Spec.OperationID)
	holder, _ := contract.Holder("lab", reservation)
	key := devicecoordination.DeviceKey("lab", "switch")
	duration := int32(60)
	transitions := int32(1)
	renewed := metav1.NewMicroTime(now.Add(-time.Hour))
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: engine.LeaseName(key, devicecoordination.MutationLeaseFamily), Namespace: "leases", UID: "lease-uid", Annotations: map[string]string{devicecoordination.RetainLeaseAnnotation: "true"}}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseTransitions: &transitions, LeaseDurationSeconds: &duration, RenewTime: &renewed, AcquireTime: &renewed}}
	reservation.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: lease.Name, UID: string(lease.UID)}, Holder: holder, GrantedAt: metav1.NewTime(now.Add(-time.Hour))}
	reservation.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: reservation.Spec.OperationID, OperationDigest: reservation.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-time.Minute))}
	scheme := runtime.NewScheme()
	_ = ops.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	_ = config.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	_ = rbac.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(reservation).WithObjects(reservation, lease).WithIndex(&core.Pod{}, "spec.nodeName", func(o client.Object) []string { return []string{o.(*core.Pod).Spec.NodeName} }).Build()
	r := &Reconciler{Reader: c, Client: c, Leaser: &engine.FamilyLeaser{Client: c, Namespace: "leases", TTL: time.Minute, RequireExisting: true}, Namespace: "lab", DeviceName: "switch", DeviceUID: "device-uid", PodUID: "pod-uid", ControllerType: "catalyst-center", Authorize: func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("recovery tried to authorize new work")
		return nil
	}, Prepare: func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("recovery ran preparation")
		return nil
	}}
	return r, reservation, lease
}
func TestClaimedRecoveryRenewsFence(t *testing.T) {
	ctx := context.Background()
	r, reservation, lease := recoveryFixture(t)
	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)})
	if err != nil || result.RequeueAfter == 0 {
		t.Fatal(result, err)
	}
	if err = r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != reservation.Status.Grant.Holder || lease.Spec.RenewTime.Before(&metav1.MicroTime{Time: time.Now().Add(-time.Minute)}) {
		t.Fatal("claim fence not renewed")
	}
}
func TestIncompleteCompletionStillRenewsFence(t *testing.T) {
	ctx := context.Background()
	r, reservation, lease := recoveryFixture(t)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	reservation.Status.Completion = &ops.ConfigurationReservationCompletion{OperationID: reservation.Spec.OperationID}
	if err := r.Client.Status().Update(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}); err == nil {
		t.Fatal("invalid completion accepted")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.RenewTime.Time.Before(time.Now().Add(-time.Minute)) {
		t.Fatal("failed completion read stopped renewal")
	}
}
func TestRevokedPreparationReleasesRetainedLease(t *testing.T) {
	ctx := context.Background()
	r, reservation, lease := recoveryFixture(t)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	reservation.Status.Claim = nil
	reservation.Status.Grant.Phase = "Revoked"
	if err := r.Client.Status().Update(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity != nil {
		t.Fatal("preparation lease retained holder")
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.Grant.Phase != "Released" {
		t.Fatal("release not recorded")
	}
}
func TestReplacementPodCannotReconcile(t *testing.T) {
	ctx := context.Background()
	r, reservation, lease := recoveryFixture(t)
	r.PodUID = "replacement"
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}); err != nil {
		t.Fatal(err)
	}
	var actual coordination.Lease
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), &actual); err != nil {
		t.Fatal(err)
	}
	if !actual.Spec.RenewTime.Equal(lease.Spec.RenewTime) {
		t.Fatal("replacement worker used old reservation")
	}
}

func TestInitialGrantUsesWorkerAndJournalEvidence(t *testing.T) {
	for _, drift := range []bool{false, true} {
		name := "settlement"
		if drift {
			name = "workload before dispatch"
		}
		t.Run(name, func(t *testing.T) { testInitialGrantUsesWorkerAndJournalEvidence(t, drift, false) })
	}
}

func testInitialGrantUsesWorkerAndJournalEvidence(t *testing.T, workloadBeforeDispatch, group bool) {
	ctx := context.Background()
	r, reservation, _ := recoveryFixture(t)
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	reservation.Spec.Config = ops.ConfigurationReservationReference{Name: "intent", UID: "config-uid"}
	reservation.Spec.ControllerWorkerPod = ops.ConfigurationReservationReference{Name: "cc-pod", UID: "cc-pod-uid"}
	reservation.Spec.ControllerUsername = "system:serviceaccount:lab:connector"
	reservation.Spec.ConfigGeneration = 1
	reservation.Spec.ControllerGeneration = 1
	reservation.Spec.DeviceGeneration = 1
	reservation.Spec.Node = ops.ConfigurationReservationReference{Name: "switch", UID: "node-uid"}
	reservation.Spec.ManagementAddress = "192.0.2.1"
	reservation.Spec.PhysicalIdentity = "serial123"
	reservation.Spec.InventoryID = "inventory-id"
	reservation.Spec.DeviceWorkerUsername = "system:serviceaccount:lab:network"
	reservation.Spec.DeviceWorkerRevision = "revision"
	op := &cvk.NetworkControllerConfigurationOperation{ID: reservation.Spec.OperationID, ConfigName: "intent", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "resource", Action: "Update", Phase: "Prepared", TargetID: "inventory-id", TargetSerial: "serial123"}
	reservation.Spec.OperationDigest, _ = contract.OperationDigest(op)
	if err := r.Client.Update(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	reservation.Status = ops.ControllerConfigurationReservationStatus{}
	if err := r.Client.Status().Update(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	started := metav1.NewTime(now.Add(-time.Minute))
	ready := metav1.NewTime(now.Add(-time.Second))
	device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "switch", Namespace: "lab", UID: "device-uid", Generation: 1}, Spec: cvk.DeviceSpec{Address: "192.0.2.1", Driver: cvk.DeviceDriverXE, PhysicalIdentity: "serial123"}, Status: cvk.DeviceStatus{NodeIdentity: &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-uid", NodeName: "switch", NodeUID: "node-uid", PhysicalIdentity: "serial123"}, NetworkWorkerRevision: &cvk.DeviceNetworkWorkerRevisionStatus{DesiredRevision: "revision", ObservedRevision: "revision", PodUID: "pod-uid", PodStartTime: &started, PodReadyTime: &ready}}}
	node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node-uid", Annotations: map[string]string{managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: "lab", managedprotocol.AnnotationDeviceName: "switch", managedprotocol.AnnotationDeviceUID: "device-uid", managedprotocol.AnnotationNodeUID: "node-uid", managedprotocol.AnnotationWorkerProtocol: managedprotocol.Version, managedprotocol.AnnotationNetworkWorkerUsername: reservation.Spec.DeviceWorkerUsername}}, Status: core.NodeStatus{NodeInfo: core.NodeSystemInfo{MachineID: "serial123"}}}
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "lab", UID: "pod-uid", Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: "revision"}}, Spec: core.PodSpec{ServiceAccountName: "network"}, Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: "lab", UID: "controller-uid", Generation: 1}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op}}}
	nc.Spec.Type = "catalyst-center"
	nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "intent", ConfigUID: "config-uid"}}
	intent := &config.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "intent", Namespace: "lab", UID: "config-uid", Generation: 1}, Spec: config.NetworkControllerConfigSpec{ControllerRef: config.NetworkControllerRef{Name: "cc"}, Scope: "global", ManagedSections: []string{"inventory"}, Mode: config.NetworkControllerApplyModeApply, ModelSource: config.NetworkControllerNetAsCodeModelSource{Resolved: true, ModelVersion: "0.5.0"}, Source: config.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{}`)}}}}
	nc.Status.Worker = &cvk.NetworkControllerWorkerStatus{Name: "connector", DeploymentName: "connector", ReadyReplicas: 1}
	replicas := int32(1)
	controlled := true
	template := core.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"worker": "connector"}, Annotations: map[string]string{"cisco.vk/controller-contract-hash": strings.Repeat("a", 64)}}, Spec: core.PodSpec{ServiceAccountName: "connector", Containers: []core.Container{{Name: "controller-worker", Args: []string{"controller-worker", "--controller-namespace=lab", "--controller-name=cc", "--controller-uid=controller-uid", "--controller-generation=1", "--controller-type=catalyst-center"}}}}}
	deployment := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "lab", UID: "deployment-uid", Generation: 1, Annotations: map[string]string{"cisco.vk/network-controller-name": "cc", "cisco.vk/network-controller-uid": "controller-uid"}}, Spec: apps.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: template.Labels}, Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType}, Template: *template.DeepCopy()}, Status: apps.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1}}
	rs := &apps.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "replica", Namespace: "lab", UID: "replica-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "connector", UID: "deployment-uid", Controller: &controlled}}}, Spec: apps.ReplicaSetSpec{Template: *template.DeepCopy()}}
	connector := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "cc-pod", Namespace: "lab", UID: "cc-pod-uid", Labels: template.Labels, Annotations: template.Annotations, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "replica", UID: "replica-uid", Controller: &controlled}}}, Spec: *template.Spec.DeepCopy(), Status: core.PodStatus{Phase: core.PodRunning, Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue, LastTransitionTime: ready}}}}
	node.Annotations[managedprotocol.AnnotationNetworkWorkerPodName] = pod.Name
	node.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = string(pod.UID)
	for _, obj := range []client.Object{device, node, pod, nc, intent, deployment, rs, connector} {
		if err := r.Client.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	// The manager reader constructs the same immutable reservation from live
	// physical identity, source and complete worker ownership evidence.
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	if group {
		testGroupPreparationFromLiveFixture(t, r, reservation, nc, device, node)
		return
	}
	candidate, err := contract.ReadReservationCandidate(ctx, r.Reader, nc, now)
	if err != nil {
		t.Fatal("read candidate", err)
	}
	if !reflect.DeepEqual(candidate.Spec, reservation.Spec) || candidate.UID != "" || candidate.Status.Grant != nil {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}
	if contract.ReadIntentBinding(ctx, r.Reader, candidate, nc.Spec.Type) == nil {
		t.Fatal("UID-less candidate passed runtime intent gate")
	}
	duplicate := device.DeepCopy()
	duplicate.Namespace = "other-namespace"
	duplicate.Name = "duplicate"
	duplicate.UID = "duplicate-uid"
	duplicate.ResourceVersion = ""
	if err := r.Client.Create(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ReadReservationCandidate(ctx, r.Reader, nc, now); err == nil {
		t.Fatal("duplicate physical device selected")
	}
	if err := r.Client.Delete(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	originalMode := intent.Spec.Mode
	intent.Spec.Mode = config.NetworkControllerApplyModeReport
	if err := r.Client.Update(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ReadReservationCandidate(ctx, r.Reader, nc, now); err == nil {
		t.Fatal("report-only intent created candidate")
	}
	intent.Spec.Mode = originalMode
	if err := r.Client.Update(ctx, intent); err != nil {
		t.Fatal(err)
	}
	stale := nc.DeepCopy()
	stale.ResourceVersion = "0"
	if _, err := contract.ReadReservationCandidate(ctx, r.Reader, stale, now); err == nil {
		t.Fatal("stale controller created candidate")
	}
	if _, err := contract.ReadReservationCandidate(ctx, r.Reader, nc, now); err != nil {
		t.Fatal("restored candidate", err)
	}
	// Exercise manager creation against the same complete live evidence before
	// the device-side grant lifecycle. The fake writer supplies an API-assigned UID.
	if err := r.Client.Delete(ctx, reservation); err != nil {
		t.Fatal(err)
	}
	writer := &reservationAPIWriter{Writer: r.Client}
	if _, err := contract.EnsureReservation(ctx, r.Reader, writer, nc, nil, now); err == nil {
		t.Fatal("creation without rollout authorization")
	}
	if _, err := contract.EnsureReservation(ctx, r.Reader, writer, nc, func(context.Context, *ops.ControllerConfigurationReservation) error {
		return errors.New("rollout unavailable")
	}, now); err == nil {
		t.Fatal("creation ignored rollout rejection")
	}
	if writer.creates != 0 {
		t.Fatal("unauthorized creation reached API")
	}
	if _, err := contract.EnsureReservation(ctx, r.Reader, writer, nc, func(context.Context, *ops.ControllerConfigurationReservation) error {
		intent.Spec.Mode = config.NetworkControllerApplyModeReport
		return r.Client.Update(ctx, intent)
	}, now); err == nil {
		t.Fatal("source changed during authorization still created reservation")
	}
	if writer.creates != 0 {
		t.Fatal("changed source reached creation")
	}
	intent.Spec.Mode = originalMode
	if err := r.Client.Update(ctx, intent); err != nil {
		t.Fatal(err)
	}
	creationChecks := 0
	// Ready/read-write intent is not proof that the network worker actually
	// holds the current compiled role. No reservation may be created without it.
	if _, err := contract.EnsureReservation(ctx, r.Reader, writer, nc, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, now); err == nil {
		t.Fatal("created without network RBAC")
	}
	if writer.creates != 0 {
		t.Fatal("missing role reached create")
	}
	for _, object := range []client.Object{
		&rbac.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: managedprotocol.NetworkManagementReadWriteClusterRole, UID: "role-uid"}, Rules: managedprotocol.WorkerClusterRoleContracts()[managedprotocol.NetworkManagementReadWriteClusterRole]},
		&rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "network", Namespace: "lab", UID: "binding-uid"}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "ClusterRole", Name: managedprotocol.NetworkManagementReadWriteClusterRole}, Subjects: []rbac.Subject{{Kind: rbac.ServiceAccountKind, Namespace: "lab", Name: "network"}}},
	} {
		if err := r.Client.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	created, err := contract.EnsureReservation(ctx, r.Reader, writer, nc, func(_ context.Context, candidate *ops.ControllerConfigurationReservation) error {
		creationChecks++
		candidate.Spec.InventoryID = "callback mutation must not escape"
		return nil
	}, now)
	if err != nil {
		t.Fatal("manager create", err)
	}
	if created.UID == "" || !reflect.DeepEqual(created.Spec, reservation.Spec) || writer.creates != 1 || creationChecks != 1 {
		t.Fatal("creation lost immutable binding")
	}
	existing, err := contract.EnsureReservation(ctx, r.Reader, writer, nc, nil, now)
	if err != nil || existing.UID != created.UID || writer.creates != 1 {
		t.Fatal("retry did not preserve existing evidence", err)
	}
	reservation = created
	authorizations, preparations := 0, 0
	r.Authorize = func(context.Context, *ops.ControllerConfigurationReservation) error { authorizations++; return nil }
	r.Prepare = func(context.Context, *ops.ControllerConfigurationReservation) error { preparations++; return nil }
	scans := 0
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}
	for _, failScan := range []bool{false, true} {
		r.StrictDevicePodLister = func(context.Context) ([]*core.Pod, error) {
			if failScan {
				return nil, errors.New("incomplete inventory")
			}
			return []*core.Pod{nil}, nil
		}
		if _, err := r.Reconcile(ctx, req); err == nil {
			t.Fatal("native inventory uncertainty published grant")
		}
		var rejected ops.ControllerConfigurationReservation
		if err := r.Client.Get(ctx, req.NamespacedName, &rejected); err != nil {
			t.Fatal(err)
		}
		if rejected.Status.Grant != nil || rejected.Status.Claim != nil {
			t.Fatal("failed scan left mutation authority")
		}
	}
	authorizations, preparations = 0, 0
	r.StrictDevicePodLister = func(context.Context) ([]*core.Pod, error) { scans++; return nil, nil }
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.Grant == nil || reservation.Status.Grant.Phase != "Held" || authorizations != 2 || preparations != 1 || scans != 1 {
		t.Fatalf("grant path not executed: %#v %d %d", reservation.Status, authorizations, preparations)
	}
	// Reconciliation renews the existing grant, without repeating preparation.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if preparations != 1 {
		t.Fatal("renewal repeated preparation")
	}
	// Simulate the controller's one-shot claim and independently verified receipt.
	if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	actor := reservation.Spec.ControllerWorkerPod
	claimAuthorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", claimAuthorize, time.Now()); err == nil {
		t.Fatal("claim without manager acknowledgement")
	}
	g := reservation.Status.Grant
	token := "11111111-1111-4111-8111-111111111111"
	device.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionAcknowledged, ProtocolVersion: cvk.DeviceMaintenanceProtocolConfigurationV1, Purpose: cvk.DeviceMaintenancePurposeConfigurationMutation, SessionToken: token, DeviceUID: string(device.UID), NodeName: node.Name, NodeUID: string(node.UID), ControlRevision: reservation.Spec.ConfigGeneration, RequestedAt: g.GrantedAt, AcknowledgedAt: g.GrantedAt.DeepCopy(), Operation: cvk.DeviceMaintenanceObjectReference{Namespace: reservation.Namespace, Name: reservation.Name, UID: string(reservation.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: g.LeaseNamespace, Name: g.Lease.Name, UID: g.Lease.UID}, Holder: g.Holder}}
	if err := r.Client.Update(ctx, device); err != nil {
		t.Fatal(err)
	}
	var guardedLease coordination.Lease
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: g.LeaseNamespace, Name: g.Lease.Name}, &guardedLease); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{managedprotocol.AnnotationMaintenanceRequestVersion: contract.Protocol, managedprotocol.AnnotationMaintenancePurpose: managedprotocol.MaintenancePurposeConfigurationMutation, managedprotocol.AnnotationMaintenanceSessionToken: token, managedprotocol.AnnotationMaintenanceOperationNS: reservation.Namespace, managedprotocol.AnnotationMaintenanceOperationName: reservation.Name, managedprotocol.AnnotationMaintenanceOperationUID: string(reservation.UID), managedprotocol.AnnotationMaintenanceControlRevision: "1", managedprotocol.AnnotationMaintenanceRequestedAt: g.GrantedAt.Format(time.RFC3339)} {
		guardedLease.Annotations[k] = v
	}
	if err := r.Client.Update(ctx, &guardedLease); err != nil {
		t.Fatal(err)
	}
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", claimAuthorize, time.Now()); err == nil {
		t.Fatal("claim without Node guard")
	}
	node.Spec.Taints = []core.Taint{{Key: managedprotocol.MaintenanceTaintKey, Value: managedprotocol.MaintenanceTaintValue, Effect: core.TaintEffectNoSchedule}}
	if err := r.Client.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	replacement := actor
	replacement.UID = "replacement"
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, replacement, "leases", claimAuthorize, time.Now()); err == nil {
		t.Fatal("replacement connector claimed operation")
	}
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", nil, time.Now()); err == nil {
		t.Fatal("claim without rollout/workload authorizer")
	}
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		node.Spec.Taints = nil
		return r.Client.Update(ctx, node)
	}, time.Now()); err == nil {
		t.Fatal("claim ignored guard removal during authorization")
	}
	node.Spec.Taints = []core.Taint{{Key: managedprotocol.MaintenanceTaintKey, Value: managedprotocol.MaintenanceTaintValue, Effect: core.TaintEffectNoSchedule}}
	if err := r.Client.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	workload := &core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "late-workload", Namespace: "applications", UID: "late-workload-uid"}, Spec: core.PodSpec{NodeName: node.Name}, Status: core.PodStatus{Phase: core.PodPending}}
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		return r.Client.Create(ctx, workload)
	}, time.Now()); err == nil {
		t.Fatal("claim ignored new workload during authorization")
	}
	if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.Claim != nil {
		t.Fatal("blocked workload left a submission claim")
	}
	if err := r.Client.Delete(ctx, workload); err != nil {
		t.Fatal(err)
	}
	workload.ResourceVersion = ""
	permit, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", claimAuthorize, time.Now())
	if err != nil {
		t.Fatal("guarded claim", err)
	}
	if _, err := contract.ClaimForController(ctx, r.Reader, r.Client, nc, actor, "leases", claimAuthorize, time.Now()); err == nil {
		t.Fatal("replayed claim")
	}

	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	// Persist the existing journal's write-ahead transition before consuming
	// the process-local permit. A copied permit cannot authorize another submit.
	startedOp := op.DeepCopy()
	startedOp.Phase = "Submitting"
	startedAt := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	startedOp.SubmittedAt = &startedAt
	nc.Status.Configuration.Operation = startedOp
	if err := r.Client.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	copiedPermit := *permit
	if workloadBeforeDispatch {
		if err := r.Client.Create(ctx, workload); err != nil {
			t.Fatal(err)
		}
		if err := permit.Check(ctx, time.Now()); err == nil {
			t.Fatal("dispatch ignored newly bound workload")
		}
		if err := r.Client.Delete(ctx, workload); err != nil {
			t.Fatal(err)
		}
		if err := copiedPermit.Check(ctx, time.Now()); err == nil {
			t.Fatal("workload removal revived consumed permit")
		}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
			t.Fatal(err)
		}
		if reservation.Status.Grant.Phase != "Held" || reservation.Status.Claim == nil || reservation.Status.Completion != nil {
			t.Fatal("failed dispatch lost quarantine evidence")
		}
		return
	}
	if err := permit.Check(ctx, time.Now()); err != nil {
		t.Fatal("dispatch verification", err)
	}
	if err := copiedPermit.Check(ctx, time.Now()); err == nil {
		t.Fatal("copied permit authorized replay")
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	verified.TaskID = "task"
	submitted := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	verified.SubmittedAt = &submitted
	nc.Status.Configuration.Operation = verified
	nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: verified.Section, Resource: "stable-key", Operation: *verified.DeepCopy()}}
	if err := r.Client.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
		t.Fatal(err)
	}
	if err := contract.PublishCompletion(ctx, r.Reader, r.Client, reservation, time.Now().UTC().Truncate(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Status.Grant.Phase != "Released" || reservation.Status.Completion == nil {
		t.Fatal("verified flow did not release")
	}
	var finalLease coordination.Lease
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: "leases", Name: reservation.Status.Grant.Lease.Name}, &finalLease); err != nil {
		t.Fatal(err)
	}
	if finalLease.Spec.HolderIdentity != nil {
		t.Fatal("completed flow retained lease holder")
	}
}

// fake.Client does not assign Kubernetes UIDs on Create. Model only that API
// behavior; authorization and all reads use the ordinary fixture client.
type reservationAPIWriter struct {
	client.Writer
	creates int
}

func (w *reservationAPIWriter) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	w.creates++
	obj.SetUID("created-reservation-uid")
	return w.Writer.Create(ctx, obj, opts...)
}

// Startup intentionally supplies no new-work hooks. Recovery must still renew
// accepted work and revoke/release unused grants without invoking native APIs.
func TestRecoveryOnlyRuntime(t *testing.T) {
	for _, claimed := range []bool{true, false} {
		t.Run(fmt.Sprintf("claimed=%v", claimed), func(t *testing.T) {
			ctx := context.Background()
			r, reservation, lease := recoveryFixture(t)
			r.ControllerType = ""
			r.Authorize, r.Prepare, r.StrictDevicePodLister = nil, nil, nil
			if !claimed {
				reservation.Status.Claim = nil
				if err := r.Client.Status().Update(ctx, reservation); err != nil {
					t.Fatal(err)
				}
			}
			req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
				t.Fatal(err)
			}
			if claimed {
				if reservation.Status.Grant.Phase != "Held" || reservation.Status.Claim == nil {
					t.Fatal("accepted reservation changed")
				}
				if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					t.Fatal(err)
				}
				if lease.Spec.RenewTime.Time.Before(time.Now().Add(-time.Minute)) {
					t.Fatal("recovery without hooks failed to renew quarantine")
				}
			} else {
				if reservation.Status.Grant.Phase != "Revoked" {
					t.Fatal("unused grant was not revoked")
				}
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
				if err := r.Client.Get(ctx, req.NamespacedName, reservation); err != nil {
					t.Fatal(err)
				}
				if reservation.Status.Grant.Phase != "Released" {
					t.Fatal("revoked grant was not settled")
				}
				if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					t.Fatal(err)
				}
				if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
					t.Fatal("revoked lease remains owned")
				}
			}
		})
	}
}

func TestCancelledPreparationReleasesOnlyOwnLease(t *testing.T) {
	for _, scenario := range []string{"own", "foreign", "future", "conflicting grant"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, reservation, lease := recoveryFixture(t)
			reservation.Status.Claim = nil
			if scenario != "conflicting grant" {
				reservation.Status.Grant = nil
			}
			now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			reservation.Status.PreparationCancelledAt = &now
			if scenario == "future" {
				reservation.Status.PreparationCancelledAt = &metav1.Time{Time: time.Now().Add(time.Hour)}
			}
			if err := r.Client.Status().Update(ctx, reservation); err != nil {
				t.Fatal(err)
			}
			if scenario == "foreign" {
				holder := "another-operation"
				lease.Spec.HolderIdentity = &holder
				if err := r.Client.Update(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
			expectedHolder := *lease.Spec.HolderIdentity
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)})
			if (err != nil) != (scenario == "future" || scenario == "conflicting grant") {
				t.Fatalf("err=%v", err)
			}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			holder := ""
			if lease.Spec.HolderIdentity != nil {
				holder = *lease.Spec.HolderIdentity
			}
			if scenario == "own" && holder != "" {
				t.Fatal("cancelled preparation still owns lease")
			}
			if scenario != "own" && holder != expectedHolder {
				t.Fatal("foreign or inconsistent evidence released lease")
			}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
				t.Fatal(err)
			}
			if reservation.Status.ManagerSettledAt != nil || reservation.Status.Claim != nil {
				t.Fatal("device fabricated manager settlement or claim")
			}
		})
	}
}

func TestAutomaticPreparationCancellation(t *testing.T) {
	for _, scenario := range []string{"missing hooks", "lost binding", "foreign lease"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			r, row, lease := recoveryFixture(t)
			row.Status = ops.ControllerConfigurationReservationStatus{}
			if err := r.Client.Status().Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing hooks" {
				r.StrictDevicePodLister = func(context.Context) ([]*core.Pod, error) { t.Fatal("invalid binding scanned device"); return nil, nil }
			}
			foreign := "device-write/foreign"
			if scenario == "foreign lease" {
				lease.Spec.HolderIdentity = &foreign
				if err := r.Client.Update(ctx, lease); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			before := lease.DeepCopy()
			req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(row)}
			if _, err := r.Reconcile(ctx, req); err == nil {
				t.Fatal("lost authorization not reported")
			}
			if err := r.Reader.Get(ctx, req.NamespacedName, row); err != nil {
				t.Fatal(err)
			}
			if err := contract.ValidatePreparationCancellation(row, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Spec, lease.Spec) {
				t.Fatal("Lease changed on cancellation publication pass")
			}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
				t.Fatal(err)
			}
			if scenario == "foreign lease" {
				if !reflect.DeepEqual(before.Spec, lease.Spec) {
					t.Fatal("foreign lease modified")
				}
			} else if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
				t.Fatal("own lease not released")
			}
			if row.Status.ManagerSettledAt != nil {
				t.Fatal("worker manufactured manager settlement")
			}
		})
	}
}
