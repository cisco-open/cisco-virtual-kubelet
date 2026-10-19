// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"time"
)

// Retire only settled evidence. This does not create reservations or authorize
// new device work, and must remain available during managed topology retirement.
func (r *NetworkControllerReconciler) reconcileConfigurationReservationRetirement(ctx context.Context, nc *cvk.NetworkController) (bool, error) {
	metadataPending, err := r.reconcileMetadataSettlement(ctx, nc)
	if err != nil {
		return true, err
	}
	if !r.ManageConfigurationReservations || nc.Spec.Type != "catalyst-center" {
		return metadataPending, nil
	}
	if r.APIReader == nil {
		return true, errors.New("configuration retirement requires uncached reads")
	}
	var reservations ops.ControllerConfigurationReservationList
	if err := r.APIReader.List(ctx, &reservations, client.InNamespace(nc.Namespace)); err != nil {
		return true, err
	}
	pending := metadataPending
	for i := range reservations.Items {
		row := &reservations.Items[i]
		if row.Spec.Controller.Name != nc.Name || row.Spec.Controller.UID != string(nc.UID) {
			continue
		}
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.ID == row.Spec.OperationID {
			if row.Spec.Protocol == contract.GroupReservationProtocol && row.Status.GroupCancellation != nil && row.Status.ManagerSettledAt == nil && row.Status.Grant == nil && row.Status.Claim == nil {
				if err := r.settleUnstartedGroupCancellation(ctx, nc, row); err != nil {
					return true, err
				}
			}
			pending = true
			continue
		}
		if row.Spec.Protocol == contract.GroupReservationProtocol {
			// Historical retirement is per member: already-retired peers need not
			// be reconstructed. The retained full-group receipt remains mandatory.
			// Cancellation retirement is a separate lifecycle, never completion.
			if row.Status.ManagerSettledAt == nil {
				pending = true
				continue
			}
			retire := contract.RetireSettledGroupReservation
			if row.Status.GroupCancellation != nil {
				retire = contract.RetireCancelledGroupReservation
			}
			if err := retire(ctx, r.APIReader, r.Client, row, r.AuthorizeConfigurationReservation, time.Now()); err != nil {
				return true, err
			}
		} else {
			if contract.ValidateManagerSettlement(row, time.Now()) != nil {
				pending = true
				continue
			}
			if err := contract.RetireSettledReservation(ctx, r.APIReader, r.Client, row, time.Now()); err != nil {
				return true, err
			}
		}
		// DELETE can be acknowledged while another finalizer retains the row.
		var retained ops.ControllerConfigurationReservation
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(row), &retained); err == nil {
			pending = true
		} else if client.IgnoreNotFound(err) != nil {
			return true, err
		}
	}
	return pending, nil
}

func configurationReservationControllerRequests(_ context.Context, obj client.Object) []ctrl.Request {
	r, ok := obj.(*ops.ControllerConfigurationReservation)
	if !ok || r.Namespace == "" || r.Spec.Controller.Name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}}}
}

// Creation follows normal worker reconciliation, using a fresh controller
// read because availability status may have changed its resourceVersion. This
// never acquires a device Lease or submits a product API operation.
func (r *NetworkControllerReconciler) reconcileConfigurationReservationCreation(ctx context.Context, expected *cvk.NetworkController) (bool, error) {
	// Metadata custody has no CiscoDevice mutation reservation. Its explicit
	// installation hook must not depend on enabling physical read-write work.
	if r.MetadataHandoffProtocol == cvk.NetworkControllerMetadataHandoffProtocol && r.AuthorizeMetadataHandoff != nil && expected != nil && expected.Status.Configuration != nil && expected.Status.Configuration.Operation != nil && expected.Status.Configuration.Operation.Phase == "Prepared" && !expected.Spec.Paused && expected.DeletionTimestamp == nil {
		if scope := controlleradapter.ConfigurationMetadataScopeFor(string(expected.Spec.Type)); scope != nil && scope.Selected(expected) {
			return r.reconcileConfigurationMetadataScope(ctx, expected)
		}
	}

	if !r.ManageConfigurationReservations || r.AuthorizeConfigurationReservation == nil || expected == nil || expected.Spec.Type != "catalyst-center" || expected.Spec.Paused || expected.DeletionTimestamp != nil {
		return false, nil
	}
	if r.APIReader == nil {
		return false, errors.New("configuration reservation creation requires uncached reads")
	}
	var nc cvk.NetworkController
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return false, err
	}
	if nc.UID != expected.UID || nc.Generation != expected.Generation || nc.Spec.Type != expected.Spec.Type || nc.Spec.Paused || nc.DeletionTimestamp != nil {
		return false, errors.New("controller changed before reservation creation")
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return false, nil
	}
	op := nc.Status.Configuration.Operation
	if op.Phase != "Prepared" && !(op.Phase == "Cancelling" && op.PhysicalGroup != nil) {
		return false, nil
	}
	if op.PhysicalGroup != nil {
		// Every member passes live manager authorization, including exact group
		// installation bindings on both worker Pods. Existing single-device
		// installations cannot silently authorize group reservations.
		ensure := contract.EnsureGroupReservations
		if op.Phase == "Cancelling" {
			ensure = contract.EnsureCancelledGroupReservations
		}
		_, err := ensure(ctx, r.APIReader, r.Client, &nc, func(ctx context.Context, members []*ops.ControllerConfigurationReservation) error {
			for _, member := range members {
				if err := r.AuthorizeConfigurationReservation(ctx, member); err != nil {
					return err
				}
			}
			return nil
		}, time.Now())
		return true, err
	}
	if op.TargetSerial == "" {
		return r.reconcileConfigurationMetadataScope(ctx, &nc)
	}
	_, err := contract.EnsureReservation(ctx, r.APIReader, r.Client, &nc, r.AuthorizeConfigurationReservation, time.Now())
	return true, err
}

// Physical intent is journaled in status by the isolated connector. Wake its
// own manager reconciliation on journal changes without turning inventory and
// availability updates into a self-triggering reconcile loop or waking ND.
func configurationReservationControllerPredicate() predicate.Predicate {
	base := specOrDeletionChangedPredicate()
	return predicate.Funcs{
		CreateFunc: base.Create, DeleteFunc: base.Delete, GenericFunc: base.Generic,
		UpdateFunc: func(update event.UpdateEvent) bool {
			if base.Update(update) {
				return true
			}
			old, okOld := update.ObjectOld.(*cvk.NetworkController)
			current, okCurrent := update.ObjectNew.(*cvk.NetworkController)
			if !okOld || !okCurrent || current.Spec.Type != "catalyst-center" {
				return false
			}
			operation := func(nc *cvk.NetworkController) *cvk.NetworkControllerConfigurationOperation {
				if nc.Status.Configuration == nil {
					return nil
				}
				return nc.Status.Configuration.Operation
			}
			return !reflect.DeepEqual(operation(old), operation(current))
		},
	}
}

// A cancelled preparation can end before the manager ever acknowledges a
// maintenance session. Retain that member only after the shared publisher proves
// stable absence of a guard and lease holder. Existing sessions use the normal
// CiscoDevice maintenance reconciliation, including its scheduling restoration.
func (r *NetworkControllerReconciler) settleUnstartedGroupCancellation(ctx context.Context, nc *cvk.NetworkController, row *ops.ControllerConfigurationReservation) error {
	var device cvk.CiscoDevice
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: row.Namespace, Name: row.Spec.Device.Name}, &device); err != nil {
		return err
	}
	if device.Status.MaintenanceSession != nil {
		return nil
	}
	if r.ConfigurationAdmissionBinding == nil || r.ConfigurationAdmissionBinding.Validate() != nil || r.AuthorizeConfigurationReservation == nil {
		return errors.New("unstarted cancellation requires installed manager authority and lease namespace")
	}
	namespace := r.ConfigurationAdmissionBinding.LeaseNamespace
	if namespace == "" {
		namespace = row.Namespace
	}
	return contract.PublishCancelledGroupManagerSettlement(ctx, r.APIReader, r.Client, nc, row, namespace, r.AuthorizeConfigurationReservation, time.Now())
}
