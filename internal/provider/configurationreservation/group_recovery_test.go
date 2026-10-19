// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package configurationreservation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestGroupRecoveryRetainsClaimFence(t *testing.T) {
	for _, mode := range []string{"claimed", "incomplete-completion", "unclaimed", "foreign-worker", "unknown-protocol"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r, reservation, lease := recoveryFixture(t)
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
				t.Fatal(err)
			}
			reservation.Spec.Protocol = contract.GroupReservationProtocol
			switch mode {
			case "incomplete-completion":
				reservation.Status.Completion = &ops.ConfigurationReservationCompletion{OperationID: reservation.Spec.OperationID}
			case "unclaimed":
				reservation.Status.Claim = nil
			case "foreign-worker":
				reservation.Spec.DeviceWorkerPod.UID = "foreign"
			case "unknown-protocol":
				reservation.Spec.Protocol = "unknown"
			}
			status := reservation.Status.DeepCopy()
			if err := r.Client.Update(ctx, reservation); err != nil {
				t.Fatal(err)
			}
			reservation.Status = *status
			if err := r.Client.Status().Update(ctx, reservation); err != nil {
				t.Fatal(err)
			}
			before := reservation.DeepCopy()
			var old coordination.Lease
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), &old); err != nil {
				t.Fatal(err)
			}
			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(reservation)})
			if mode == "foreign-worker" {
				if err != nil || result.RequeueAfter != 0 {
					t.Fatal("foreign worker acted")
				}
			} else if err == nil || result.RequeueAfter == 0 {
				t.Fatal("unsupported group lifecycle accepted")
			}
			var current coordination.Lease
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(lease), &current); err != nil {
				t.Fatal(err)
			}
			if mode == "claimed" || mode == "incomplete-completion" {
				if current.Spec.HolderIdentity == nil || *current.Spec.HolderIdentity != reservation.Status.Grant.Holder || current.Spec.RenewTime.Time.Before(time.Now().Add(-time.Minute)) {
					t.Fatal("group claim fence was not renewed")
				}
			} else if !reflect.DeepEqual(old, current) {
				t.Fatal("unclaimed/foreign reservation changed lease")
			}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(reservation), reservation); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Status, reservation.Status) {
				t.Fatal("recovery wrote grant/completion/release")
			}
		})
	}
}

func TestGroupPreparationWorkloadBoundary(t *testing.T) {
	for _, mode := range []string{"preparation-error", "native-app", "scan-error", "late-kubernetes-pod"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			r, member, _ := recoveryFixture(t)
			member.Spec.Protocol = contract.GroupReservationProtocol
			member.Spec.Node.Name = "node-a"
			scans := 0
			r.Prepare = func(context.Context, *ops.ControllerConfigurationReservation) error {
				if mode == "preparation-error" {
					return errors.New("preparation unavailable")
				}
				return nil
			}
			r.StrictDevicePodLister = func(context.Context) ([]*core.Pod, error) {
				scans++
				switch mode {
				case "scan-error":
					return nil, errors.New("inventory unavailable")
				case "native-app":
					return []*core.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "foreign-app"}}}, nil
				case "late-kubernetes-pod":
					return nil, r.Client.Create(ctx, &core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "late"}, Spec: core.PodSpec{NodeName: "node-a"}, Status: core.PodStatus{Phase: core.PodRunning}})
				}
				return nil, nil
			}
			var stored ops.ControllerConfigurationReservation
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(member), &stored); err != nil {
				t.Fatal(err)
			}
			before := stored.Status.DeepCopy()
			if err := r.prepareAndPublish(ctx, member, true); err == nil {
				t.Fatal("unsafe preparation accepted")
			}
			wantScans := 1
			if mode == "preparation-error" {
				wantScans = 0
			}
			if scans != wantScans {
				t.Fatalf("unexpected scans: %d", scans)
			}
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(member), member); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, &member.Status) {
				t.Fatal("failed preparation changed reservation authority")
			}
		})
	}
}

func TestGroupWorkerVerifiedRelease(t *testing.T) {
	testGroupWorkerVerifiedRelease(t, "")
}

func TestGroupWorkerCancellationAcknowledgement(t *testing.T) {
	for _, mode := range []string{"acknowledge", "ungranted", "denied", "submitted", "foreign-controller"} {
		t.Run(mode, func(t *testing.T) { testGroupWorkerVerifiedRelease(t, mode) })
	}
}

func testGroupWorkerVerifiedRelease(t *testing.T, cancelMode string) {
	ctx := context.Background()
	r, member, lease := recoveryFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	r.Now = func() time.Time { return now }
	s := &member.Spec
	s.Protocol = contract.GroupReservationProtocol
	s.Config = ops.ConfigurationReservationReference{Name: "intent", UID: "intent-uid"}
	s.ConfigGeneration, s.ControllerGeneration = 1, 1
	s.PhysicalIdentity, s.InventoryID = "serial-a", "inventory-a"
	s.Node = ops.ConfigurationReservationReference{Name: "node-a", UID: "node-a-uid"}
	s.ControllerWorkerPod = ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}
	s.ControllerUsername = "system:serviceaccount:lab:connector"
	op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{
		ID: s.OperationID, ConfigName: s.Config.Name, ConfigUID: s.Config.UID,
		ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Resource: "fabric-pending-event:receipt",
		Action: "Update", TargetID: "fabric-id", Phase: "Prepared",
	}, []contract.GroupTargetIdentity{{PhysicalIdentity: s.PhysicalIdentity, InventoryID: s.InventoryID, Device: s.Device, Node: s.Node}})
	if err != nil {
		t.Fatal(err)
	}
	s.OperationDigest = op.PhysicalGroup.Digest
	raw, _ := json.Marshal([]string{contract.GroupReservationProtocol, s.Controller.UID, op.ID, s.OperationDigest, s.PhysicalIdentity})
	sum := sha256.Sum256(raw)
	member.Name = "config-group-" + hex.EncodeToString(sum[:16])
	member.Finalizers = []string{contract.Finalizer}
	holder, err := contract.Holder(member.Namespace, member)
	if err != nil {
		t.Fatal(err)
	}
	member.Status.Grant.Holder = holder
	member.Status.Claim.OperationDigest = s.OperationDigest
	lease.Spec.HolderIdentity = &holder
	submitted, verified := metav1.NewTime(now.Add(-30*time.Second)), metav1.NewTime(now.Add(-time.Second))
	op.Phase, op.TaskID, op.SubmittedAt = "Verified", "task-id", &submitted
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: member.Namespace, Name: s.Controller.Name, UID: "controller-uid"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{
		Operation: op, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: op.Resource, Operation: *op.DeepCopy(), VerifiedAt: &verified}},
	}}}
	completions, err := contract.GroupCompletionsForReceipt(nc, []*ops.ControllerConfigurationReservation{member}, now)
	if err != nil {
		t.Fatal(err)
	}
	member.Status.Completion = completions[member.UID]
	if cancelMode != "" {
		member.Status.Completion = nil
		op.Phase, op.TaskID, op.SubmittedAt = "Cancelling", "", nil
		at := metav1.NewTime(now.Add(-time.Second))
		op.CancellationRequestedAt = &at
		nc.Status.Configuration.ActionApplications = nil
		if cancelMode == "ungranted" {
			member.Status.Grant = nil
			member.Status.Claim = nil
		}
		if cancelMode == "submitted" {
			op.SubmittedAt = &submitted
		}
		if cancelMode == "foreign-controller" {
			nc.UID = "replacement"
		}
	}

	c := fake.NewClientBuilder().WithScheme(r.Client.Scheme()).WithStatusSubresource(member, nc).WithObjects(member, nc, lease).WithIndex(&core.Pod{}, "spec.nodeName", func(o client.Object) []string { return []string{o.(*core.Pod).Spec.NodeName} }).Build()
	r.Client, r.Reader, r.Leaser.Client = c, c, c
	checks := 0
	r.Authorize = func(context.Context, *ops.ControllerConfigurationReservation) error {
		checks++
		if cancelMode == "denied" {
			return errors.New("denied")
		}
		return nil
	}
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(member)}
	result, err := r.Reconcile(ctx, request)
	if cancelMode != "" {
		accepted := cancelMode == "acknowledge" || cancelMode == "ungranted"
		if (err == nil) != accepted || result.RequeueAfter == 0 {
			t.Fatal("cancellation result", result, err)
		}
		if e := c.Get(ctx, client.ObjectKeyFromObject(member), member); e != nil {
			t.Fatal(e)
		}
		if (member.Status.GroupCancellation != nil) != accepted {
			t.Fatal("incorrect cancellation acknowledgement")
		}
		if cancelMode != "ungranted" && (member.Status.Grant.Phase != "Held" || member.Status.Claim == nil || member.Status.Completion != nil) {
			t.Fatal("acknowledgement changed mutation evidence")
		}
		if e := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); e != nil {
			t.Fatal(e)
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != holder {
			t.Fatal("acknowledgement released lease")
		}
		if accepted {
			before := member.Status.GroupCancellation.DeepCopy()
			if _, e := r.Reconcile(ctx, request); e != nil {
				t.Fatal(e)
			}
			if e := c.Get(ctx, client.ObjectKeyFromObject(member), member); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, member.Status.GroupCancellation) {
				t.Fatal("acknowledgement changed on retry")
			}
			if cancelMode != "ungranted" && (member.Status.Grant.Phase != "Released" || member.Status.Claim == nil || member.Status.Completion != nil) {
				t.Fatal("cancellation release lost evidence")
			}
			if cancelMode == "ungranted" && (member.Status.Grant != nil || member.Status.Claim != nil || member.Status.Completion != nil) {
				t.Fatal("ungranted cleanup fabricated execution evidence")
			}
			if e := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); e != nil {
				t.Fatal(e)
			}
			if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
				t.Fatal("cancelled lease still held")
			}
			released := lease.DeepCopy()
			if _, e := r.Reconcile(ctx, request); e != nil {
				t.Fatal("released cancellation retry", e)
			}
			if e := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(released, lease) {
				t.Fatal("cancelled release reacquired lease")
			}

		}
		return
	}

	if err != nil || result.RequeueAfter != 0 {
		t.Fatal(result, err)
	}
	if checks < 2 {
		t.Fatal("missing recovery authorization")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(member), member); err != nil {
		t.Fatal(err)
	}
	if member.Status.Grant.Phase != "Released" {
		t.Fatal("release not persisted")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
		t.Fatal("lease remains held")
	}
	before := lease.DeepCopy()
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatal("repeated recovery", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, lease) {
		t.Fatal("released group reacquired or rewrote lease")
	}
}
