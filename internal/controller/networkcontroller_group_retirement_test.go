// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNetworkControllerGroupRetirement(t *testing.T) {
	for _, mode := range []string{"cancelled-unstarted", "cancelled-unstarted-denied", "cancelled", "cancelled-missing-receipt", "settled", "paused", "new-generation", "same-journal", "unsettled", "held", "missing-receipt", "denied", "no-authorizer", "late-receipt-change", "foreign-finalizer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			unstarted := strings.HasPrefix(mode, "cancelled-unstarted")
			cancellation := unstarted || mode == "cancelled" || mode == "cancelled-missing-receipt"
			nc := validNetworkController("cc", "catalyst-center")
			nc.UID, nc.Generation = "controller-uid", 1
			r, c := newNetworkControllerReconciler(t, nc)
			if err := ops.AddToScheme(c.Scheme()); err != nil {
				t.Fatal(err)
			}
			c = fake.NewClientBuilder().WithScheme(c.Scheme()).WithStatusSubresource(&cvk.NetworkController{}, &ops.ControllerConfigurationReservation{}).WithObjects(nc).Build()
			r.Client = c
			r.APIReader, r.ManageConfigurationReservations = c, true
			targets := []contract.GroupTargetIdentity{}
			for _, name := range []string{"a", "b"} {
				targets = append(targets, contract.GroupTargetIdentity{InventoryID: "inventory-" + name, PhysicalIdentity: "serial-" + name, Device: ops.ConfigurationReservationReference{Name: name, UID: "device-" + name}, Node: ops.ConfigurationReservationReference{Name: name, UID: "node-" + name}})
			}
			op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ConfigName: "intent", ConfigUID: "intent-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Resource: "fabric-pending-event:receipt", TargetID: "event", ParentID: "fabric", Action: "Update", Phase: "Prepared"}, targets)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			submitted, verified, settled := metav1.NewTime(now.Add(-30*time.Second)), metav1.NewTime(now.Add(-10*time.Second)), metav1.NewTime(now.Add(-time.Second))
			op.Phase, op.TaskID, op.SubmittedAt = "Verified", "task-id", &submitted
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: op.Resource, Operation: *op.DeepCopy(), VerifiedAt: &verified}}}
			members := []*ops.ControllerConfigurationReservation{}
			for _, target := range targets {
				raw, _ := json.Marshal([]string{contract.GroupReservationProtocol, string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity})
				sum := sha256.Sum256(raw)
				name := fmt.Sprintf("config-group-%x", sum[:16])
				row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: name, UID: types.UID(name + "-uid"), Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.GroupReservationProtocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, ControllerGeneration: 1, ConfigGeneration: 1, OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, PhysicalIdentity: target.PhysicalIdentity, InventoryID: target.InventoryID, Device: target.Device, Node: target.Node, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}, ControllerUsername: "system:serviceaccount:lab:connector"}}
				holder, err := contract.Holder(row.Namespace, row)
				if err != nil {
					t.Fatal(err)
				}
				row.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Released", Holder: holder, GrantedAt: metav1.NewTime(now.Add(-time.Minute))}
				row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-40 * time.Second))}
				row.Status.ManagerSettledAt = &settled
				members = append(members, row)
			}
			completions, err := contract.GroupCompletionsForReceipt(nc, members, now)
			if err != nil {
				t.Fatal(err)
			}
			if cancellation {
				op.Phase, op.TaskID, op.SubmittedAt = "Cancelling", "", nil
				op.CancellationRequestedAt = &verified
				nc.Status.Configuration.Operation = op
				nc.Status.Configuration.ActionApplications = nil
			}
			for _, row := range members {
				row.Status.Completion = completions[row.UID]
				if cancellation {
					row.Status.Completion = nil
					row.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, RequestedAt: verified, AcknowledgedAt: verified}
				}

				if unstarted {
					row.Status.Grant, row.Status.Claim, row.Status.ManagerSettledAt = nil, nil, nil
					d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: row.Spec.Device.Name, Namespace: row.Namespace, UID: types.UID(row.Spec.Device.UID)}}
					d.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: row.Spec.Device.UID, NodeName: row.Spec.Node.Name, NodeUID: row.Spec.Node.UID, PhysicalIdentity: row.Spec.PhysicalIdentity}
					n := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: row.Spec.Node.Name, UID: types.UID(row.Spec.Node.UID), Annotations: map[string]string{
						managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationDeviceNamespace: row.Namespace,
						managedprotocol.AnnotationDeviceName: row.Spec.Device.Name, managedprotocol.AnnotationDeviceUID: row.Spec.Device.UID, managedprotocol.AnnotationNodeUID: row.Spec.Node.UID,
					}}}
					if err := c.Create(ctx, d); err != nil {
						t.Fatal(err)
					}
					if err := c.Create(ctx, n); err != nil {
						t.Fatal(err)
					}
					r.ConfigurationAdmissionBinding = &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "apps", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
				}
				if mode == "unsettled" {
					row.Status.ManagerSettledAt = nil
				}
				if mode == "held" {
					row.Status.Grant.Phase = "Held"
				}
				if mode == "foreign-finalizer" {
					row.Finalizers = append(row.Finalizers, "example.com/retained")
				}
				if err := c.Create(ctx, row); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "same-journal" {
				nc.Status.Configuration.Operation = op
			}
			if mode == "missing-receipt" {
				nc.Status.Configuration.ActionApplications = nil
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "paused" {
				nc.Spec.Paused = true
			}
			if mode == "new-generation" {
				nc.Generation++
			}
			if err := c.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if cancellation && !unstarted {
				if err := contract.FinishCancelledGroup(ctx, c, c, nc, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
				if mode == "cancelled-missing-receipt" {
					nc.Status.Configuration.GroupCancellations = nil
					if err := c.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := nc.Status.Configuration.DeepCopy()
			calls := 0
			r.AuthorizeConfigurationReservation = func(context.Context, *ops.ControllerConfigurationReservation) error {
				calls++
				if mode == "denied" || mode == "cancelled-unstarted-denied" {
					return errors.New("denied")
				}
				if mode == "late-receipt-change" {
					nc.Status.Configuration.ActionApplications = nil
					return c.Status().Update(ctx, nc)
				}
				return nil
			}
			if mode == "no-authorizer" {
				r.AuthorizeConfigurationReservation = nil
			}
			pending, err := r.reconcileConfigurationReservationRetirement(ctx, nc)
			removed := mode == "cancelled" || mode == "settled" || mode == "paused" || mode == "new-generation"
			wantError := mode == "cancelled-unstarted-denied" || mode == "cancelled-missing-receipt" || mode == "held" || mode == "missing-receipt" || mode == "denied" || mode == "no-authorizer" || mode == "late-receipt-change"
			if (err != nil) != wantError || pending == removed {
				t.Fatalf("pending=%v error=%v", pending, err)
			}
			for _, row := range members {
				var actual ops.ControllerConfigurationReservation
				err := c.Get(ctx, client.ObjectKeyFromObject(row), &actual)
				if removed {
					if !apierrors.IsNotFound(err) {
						t.Fatal("settled member retained", err)
					}
					continue
				}
				if err != nil {
					t.Fatal("evidence lost", err)
				}
				if unstarted && (actual.Status.ManagerSettledAt != nil) != (mode == "cancelled-unstarted") {
					t.Fatal("incorrect unstarted settlement")
				}
				if mode == "foreign-finalizer" && (actual.DeletionTimestamp == nil || len(actual.Finalizers) != 1 || actual.Finalizers[0] != "example.com/retained") {
					t.Fatal("foreign finalizer not preserved")
				}
			}
			if removed && calls != 2 {
				t.Fatal("both members must be authorized")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if mode != "late-receipt-change" && !equality.Semantic.DeepEqual(before, nc.Status.Configuration) {
				t.Fatal("retirement altered retained journal receipts")
			}
		})
	}
}
