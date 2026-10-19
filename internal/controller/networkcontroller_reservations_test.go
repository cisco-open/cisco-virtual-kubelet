// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"strings"
	"testing"
	"time"
)

func TestNetworkControllerReservationRetirementAndDeletionFence(t *testing.T) {
	for _, scenario := range []string{"settled", "held", "same journal", "deleting held", "foreign controller", "disabled", "other adapter", "no reader"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			nc := validNetworkController("cc", "catalyst-center")
			nc.UID = "controller-uid"
			nc.Finalizers = []string{networkControllerFinalizer}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
			if scenario == "other adapter" {
				nc.Spec.Type = "nexus-dashboard"
			}
			r, c := newNetworkControllerReconciler(t, nc)
			if err := ops.AddToScheme(c.Scheme()); err != nil {
				t.Fatal(err)
			}
			r.APIReader = c
			r.ManageConfigurationReservations = true
			if scenario == "disabled" {
				r.ManageConfigurationReservations = false
			}
			if scenario == "no reader" {
				r.APIReader = nil
			}
			at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: nc.Namespace, Name: "reservation", UID: "reservation-uid", Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, OperationID: strings.Repeat("a", 32)}, Status: ops.ControllerConfigurationReservationStatus{Grant: &ops.ConfigurationReservationGrant{Phase: "Released", GrantedAt: at}, ManagerSettledAt: &at}}
			if scenario == "held" || scenario == "deleting held" {
				row.Status.Grant.Phase = "Held"
				row.Status.ManagerSettledAt = nil
			}
			if scenario == "foreign controller" {
				row.Spec.Controller.UID = "old-controller"
			}
			if err := c.Create(ctx, row); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if scenario == "same journal" {
				nc.Status.Configuration.Operation = &cvk.NetworkControllerConfigurationOperation{ID: row.Spec.OperationID}
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "deleting held" {
				if err := c.Delete(ctx, nc); err != nil {
					t.Fatal(err)
				}
				result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(nc)})
				if err != nil || result.RequeueAfter == 0 {
					t.Fatal("deletion not fenced", result, err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal("controller deleted with retained reservation", err)
				}
				return
			}
			pending, err := r.reconcileConfigurationReservationRetirement(ctx, nc)
			if scenario == "no reader" {
				if err == nil {
					t.Fatal("cached retirement allowed")
				}
				return
			}
			wantPending := scenario == "held" || scenario == "same journal"
			if err != nil || pending != wantPending {
				t.Fatal("retirement decision", pending, err)
			}
			var retained ops.ControllerConfigurationReservation
			err = c.Get(ctx, client.ObjectKeyFromObject(row), &retained)
			if scenario == "settled" {
				if !apierrors.IsNotFound(err) {
					t.Fatal("settled row not removed", err)
				}
			} else if err != nil {
				t.Fatal("evidence lost", err)
			}
		})
	}
}

func TestNetworkControllerReservationCreationScope(t *testing.T) {
	for _, scenario := range []string{"retirement only", "disabled", "other adapter", "paused", "nonphysical", "accepted", "prepared missing binding", "existing evidence", "no reader", "group missing binding", "group cancelled missing binding", "group invalid digest"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			nc := validNetworkController("cc", "catalyst-center")
			nc.UID = "controller-uid"
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), ConfigName: "intent", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: nc.Generation, Section: "inventory", Resource: "assignments/switch", Action: "Update", Phase: "Prepared", TargetID: "inventory-id", TargetSerial: "serial123"}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op}
			switch scenario {
			case "other adapter":
				nc.Spec.Type = "nexus-dashboard"
			case "paused":
				nc.Spec.Paused = true
			case "nonphysical":
				op.TargetSerial = ""
			case "accepted":
				op.Phase = "Accepted"
			case "group missing binding", "group cancelled missing binding", "group invalid digest":
				nc.Generation, op.ControllerGeneration = 1, 1
				op.TargetSerial = ""
				bound, err := contract.WithGroupIdentity(op, []contract.GroupTargetIdentity{{InventoryID: op.TargetID, PhysicalIdentity: "serial123", Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device"}, Node: ops.ConfigurationReservationReference{Name: "switch", UID: "node"}}})
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation = bound
				if scenario == "group cancelled missing binding" {
					at := metav1.Now()
					bound.Phase = "Cancelling"
					bound.CancellationRequestedAt = &at
				}
				if scenario == "group invalid digest" {
					bound.PhysicalGroup.Digest = "invalid"
				}
			}
			r, c := newNetworkControllerReconciler(t, nc)
			_ = ops.AddToScheme(c.Scheme())
			r.APIReader = c
			r.ManageConfigurationReservations = scenario != "disabled"
			calls := 0
			if scenario != "retirement only" {
				r.AuthorizeConfigurationReservation = func(context.Context, *ops.ControllerConfigurationReservation) error { calls++; return nil }
			}
			if scenario == "no reader" {
				r.APIReader = nil
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if scenario == "existing evidence" {
				digest, err := contract.OperationDigest(op)
				if err != nil {
					t.Fatal(err)
				}
				name, err := contract.ReservationName(string(nc.UID), op.ID)
				if err != nil {
					t.Fatal(err)
				}
				row := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: nc.Namespace, UID: "reservation-uid", Finalizers: []string{contract.Finalizer}}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, Controller: ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, ControllerGeneration: nc.Generation, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, ConfigGeneration: op.ConfigGeneration, OperationID: op.ID, OperationDigest: digest, InventoryID: op.TargetID, PhysicalIdentity: op.TargetSerial}}
				if err := c.Create(ctx, row); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := r.reconcileConfigurationReservationCreation(ctx, nc)
			wantErr := scenario == "prepared missing binding" || scenario == "no reader" || strings.HasPrefix(scenario, "group ")
			wantPending := scenario == "existing evidence" || scenario == "prepared missing binding" || strings.HasPrefix(scenario, "group ")
			if (err != nil) != wantErr || pending != wantPending {
				t.Fatalf("pending=%v err=%v", pending, err)
			}
			if calls != 0 {
				t.Fatal("authorized without complete candidate, or reauthorized existing evidence")
			}
		})
	}
}

func TestConfigurationReservationControllerPredicate(t *testing.T) {
	pred := configurationReservationControllerPredicate()
	nc := validNetworkController("cc", "catalyst-center")
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
	prepared := nc.DeepCopy()
	prepared.Status.Configuration.Operation = &cvk.NetworkControllerConfigurationOperation{ID: "operation", Phase: "Prepared", TargetSerial: "serial"}
	if !pred.Update(event.UpdateEvent{ObjectOld: nc, ObjectNew: prepared}) {
		t.Fatal("prepared journal did not wake manager")
	}
	if !pred.Update(event.UpdateEvent{ObjectOld: prepared, ObjectNew: nc}) {
		t.Fatal("cleared journal did not wake retirement")
	}
	unchanged := prepared.DeepCopy()
	unchanged.ResourceVersion = "new"
	unchanged.Status.Worker = &cvk.NetworkControllerWorkerStatus{ReadyReplicas: 1}
	if pred.Update(event.UpdateEvent{ObjectOld: prepared, ObjectNew: unchanged}) {
		t.Fatal("availability status caused self-trigger")
	}
	nd := nc.DeepCopy()
	nd.Spec.Type = "nexus-dashboard"
	ndChanged := prepared.DeepCopy()
	ndChanged.Spec.Type = "nexus-dashboard"
	if pred.Update(event.UpdateEvent{ObjectOld: nd, ObjectNew: ndChanged}) {
		t.Fatal("ND status predicate changed")
	}
	changedSpec := nc.DeepCopy()
	changedSpec.Generation++
	if !pred.Update(event.UpdateEvent{ObjectOld: nc, ObjectNew: changedSpec}) {
		t.Fatal("spec change lost")
	}
}
