// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFinishConfigurationGroup(t *testing.T) {
	for _, mode := range []string{"reconcile-cancel-invalid", "reconcile-cancel-deleted", "reconcile-cancel-paused", "reconcile-cancel-generation", "reconcile-cancel-foreign-worker", "reconcile-verified-invalid", "cancel-valid", "cancel-missing-member", "cancel-denied", "cancel-lost-response", "public-settled", "public-publish", "public-denied", "public-foreign-worker", "valid", "paused", "new-generation", "no-admission", "denied", "missing-member", "unsettled-member", "unreleased-member", "missing-receipt", "late-member-change", "late-journal-change", "wrong-operation", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			reconcile := strings.HasPrefix(mode, "reconcile-")
			cancellation := strings.HasPrefix(mode, "cancel-") || strings.HasPrefix(mode, "reconcile-cancel-")
			reconcileSuccess := reconcile && mode != "reconcile-cancel-foreign-worker"
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab/Building","pub_sub_enabled":true,"authentication_template":{"name":"Closed Authentication"}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			nc, err := a.configurationRecoveryController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			targets := []contract.GroupTargetIdentity{}
			for _, name := range []string{"a", "b"} {
				targets = append(targets, contract.GroupTargetIdentity{InventoryID: "inventory-" + name, PhysicalIdentity: "serial-" + name, Device: ops.ConfigurationReservationReference{Name: name, UID: "device-" + name}, Node: ops.ConfigurationReservationReference{Name: name, UID: "node-" + name}})
			}
			op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "fabric", Resource: "fabric-pending-event:receipt", TargetID: "event-id", ParentID: "fabric-id", Action: "Update", Phase: "Prepared"}, targets)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			submitted, verified, settled := metav1.NewTime(now.Add(-30*time.Second)), metav1.NewTime(now.Add(-10*time.Second)), metav1.NewTime(now.Add(-time.Second))
			op.Phase, op.TaskID, op.SubmittedAt = "Verified", "task-id", &submitted
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op, Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: op.Resource, Operation: *op.DeepCopy(), VerifiedAt: &verified}}}
			members := []*ops.ControllerConfigurationReservation{}
			for _, target := range targets {
				raw, _ := json.Marshal([]string{contract.GroupReservationProtocol, string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity})
				sum := sha256.Sum256(raw)
				name := fmt.Sprintf("config-group-%x", sum[:16])
				row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: name, UID: types.UID(name + "-uid"), Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.GroupReservationProtocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, Config: ops.ConfigurationReservationReference{Name: config.Name, UID: string(config.UID)}, ControllerGeneration: nc.Generation, ConfigGeneration: config.Generation, OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, PhysicalIdentity: target.PhysicalIdentity, InventoryID: target.InventoryID, Device: target.Device, Node: target.Node, ControllerWorkerPod: ops.ConfigurationReservationReference{Name: "connector", UID: "connector-uid"}, ControllerUsername: "system:serviceaccount:lab:connector"}}
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
				nc.Status.Configuration.ActionApplications = nil
			}
			for i, row := range members {
				row.Status.Completion = completions[row.UID]
				if cancellation {
					row.Status.Completion = nil
					row.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, RequestedAt: verified, AcknowledgedAt: verified}
				}
				if mode == "public-publish" {
					row.Status.Completion = nil
					row.Status.Grant.Phase = "Held"
					row.Status.ManagerSettledAt = nil
				}
				if i == 1 && (mode == "missing-member" || mode == "cancel-missing-member") {
					continue
				}
				if i == 1 && mode == "unsettled-member" {
					row.Status.ManagerSettledAt = nil
				}
				if i == 1 && mode == "unreleased-member" {
					row.Status.Grant.Phase = "Held"
				}
				if err := a.statusWriter.Create(ctx, row); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-receipt" {
				nc.Status.Configuration.ActionApplications = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "paused" || mode == "reconcile-cancel-paused" {
				nc.Spec.Paused = true
			}
			if mode == "new-generation" || mode == "reconcile-cancel-generation" {
				nc.Generation++
			}
			if err := a.statusWriter.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			retained := nc.Status.Configuration.DeepCopy()
			retained.Operation = nil
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			calls := 0
			err = a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
				calls++
				if mode == "denied" || mode == "public-denied" || mode == "cancel-denied" {
					return errors.New("group admission not installed")
				}
				if calls == 1 && mode == "late-member-change" {
					members[1].Status.ManagerSettledAt = nil
					return a.statusWriter.Status().Update(ctx, members[1])
				}
				if calls == 1 && mode == "late-journal-change" {
					nc.Status.Configuration.Operation.Phase = "Unknown"
					return a.statusWriter.Status().Update(ctx, nc)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "no-admission" {
				a.configurationAdmission = nil
			}
			if mode == "lost-response" || mode == "cancel-lost-response" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			id := op.ID
			if mode == "wrong-operation" {
				id = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			}
			if mode == "public-foreign-worker" || mode == "reconcile-cancel-foreign-worker" {
				a.workerPodUID = "replacement"
			}
			public := (cancellation && mode != "cancel-lost-response") || mode == "public-settled" || mode == "public-publish" || mode == "public-denied" || mode == "public-foreign-worker"
			if reconcile {
				for _, obj := range []ctrlclient.Object{nc} {
					requests := a.configsForTerminalRecovery(ctx, obj)
					if len(requests) != 1 || requests[0].Name != config.Name || requests[0].Namespace != config.Namespace {
						t.Fatal("historical recovery watch lost owner")
					}
					foreign := obj.DeepCopyObject().(ctrlclient.Object)
					foreign.SetNamespace("other")
					if len(a.configsForTerminalRecovery(ctx, foreign)) != 0 {
						t.Fatal("foreign namespace woke recovery")
					}
				}
				if mode == "reconcile-cancel-deleted" {
					if err := a.statusWriter.Delete(ctx, config); err != nil {
						t.Fatal(err)
					}
				} else {
					config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"unimplemented":true}}`)
					config.Generation++
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
				_, err = (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
			} else if public {
				status := &configapi.NetworkControllerConfigStatus{}
				_, pending, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
				want := "GroupSettlementPending"
				if cancellation {
					want = "GroupCancellationPending"
				}
				if mode == "public-settled" || mode == "cancel-valid" {
					want = "ApplyProgressing"
				}
				if !pending || reason != want {
					t.Fatalf("public recovery: pending=%v reason=%s", pending, reason)
				}
				err = errors.New("pending")
				if mode == "public-settled" || mode == "cancel-valid" {
					err = nil
				}
				if mode == "public-publish" {
					for _, row := range members {
						if e := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), row); e != nil {
							t.Fatal(e)
						}
						if row.Status.Completion == nil || row.Status.Grant.Phase != "Held" || row.Status.ManagerSettledAt != nil {
							t.Fatal("publication changed settlement evidence")
						}
					}
				}
			} else {
				err = a.finishConfiguration(ctx, id)
			}
			valid := reconcileSuccess || mode == "cancel-valid" || mode == "public-settled" || mode == "valid" || mode == "paused" || mode == "new-generation"
			if (err == nil) != valid {
				t.Fatalf("%s: %v", mode, err)
			}
			state := journalState(t, a)
			cleared := valid || mode == "lost-response" || mode == "cancel-lost-response"
			if (state.Operation == nil) != cleared {
				t.Fatal("incorrect journal cleanup")
			}
			if cleared && cancellation {
				latest, err := a.configurationRecoveryController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := contract.ValidateRetainedGroupCancellation(latest, op.ID, members, time.Now()); err != nil {
					t.Fatal(err)
				}
				retained.GroupCancellations = state.DeepCopy().GroupCancellations
			}
			if cleared && !equality.Semantic.DeepEqual(state, retained) {
				t.Fatal("lost retained receipts or owner")
			}
			if valid && !public && !reconcile && calls != len(members) {
				t.Fatal("did not authorize every member")
			}
			if cleared && a.finishConfiguration(ctx, id) == nil {
				t.Fatal("stale cleanup accepted")
			}
		})
	}
}
