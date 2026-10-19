// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"slices"
	"time"
)

func (a *adapter) configurationReservation(ctx context.Context, nc *cvk.NetworkController) (*ops.ControllerConfigurationReservation, error) {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return nil, errConfigurationBusy
	}
	op := nc.Status.Configuration.Operation
	name, err := contract.ReservationName(string(nc.UID), op.ID)
	if err != nil {
		return nil, err
	}
	var r ops.ControllerConfigurationReservation
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: nc.Namespace, Name: name}, &r); err != nil {
		return nil, err
	}
	if !slices.Contains(r.Finalizers, contract.Finalizer) || r.Spec.Controller.Name != nc.Name || r.Spec.Controller.UID != string(nc.UID) || contract.ValidateOperationBinding(&r, op) != nil {
		return nil, errConfigurationBusy
	}
	return &r, nil
}

// Publish only verified retained evidence. The device worker releases the Lease
// independently; this adapter never acquires, renews or releases device Leases.
func (a *adapter) completeConfigurationReservation(ctx context.Context, nc *cvk.NetworkController) error {
	r, err := a.configurationReservation(ctx, nc)
	if err != nil {
		return err
	}
	if r.Status.Grant != nil && r.Status.Grant.Phase == "Released" {
		return a.finishConfiguration(ctx, nc.Status.Configuration.Operation.ID)
	}
	if a.workerPodUID == "" || a.workerPodName == "" || r.Spec.ControllerWorkerPod.Name != a.workerPodName || r.Spec.ControllerWorkerPod.UID != a.workerPodUID {
		return errConfigurationDeviceAdmission
	}
	if err := contract.PublishCompletion(ctx, a.statusReader, a.statusWriter, r, time.Now()); err != nil {
		return err
	}
	return errConfigurationBusy
}

// Completion publication and settlement use the same retained group receipt.
// This route never submits a native task or changes a device lease.
func (a *adapter) completeConfigurationGroup(ctx context.Context, nc *cvk.NetworkController) error {
	if !a.configurationAdmissionReady() {
		return errConfigurationDeviceAdmission
	}
	members, err := contract.ReadVerifiedGroupReservations(ctx, a.statusReader, nc, time.Now())
	if err != nil {
		return err
	}
	if err := contract.PublishGroupCompletions(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		for _, member := range current {
			if err := a.authorizeConfigurationGroupMember(ctx, member); err != nil {
				return err
			}
		}
		return nil
	}, time.Now()); err != nil {
		return err
	}
	return a.finishConfiguration(ctx, nc.Status.Configuration.Operation.ID)
}

// A verified receipt alone cannot discard the operation while reservation or
// manager settlement remains outstanding. The receipt survives journal release
// and remains available for subsequent reservation retirement.
func (a *adapter) configurationReservationSettled(ctx context.Context, nc *cvk.NetworkController) error {
	r, err := a.configurationReservation(ctx, nc)
	if err != nil {
		return err
	}
	g := r.Status.Grant
	if r.Status.PreparationCancelledAt != nil {
		if nc.Status.Configuration.Operation.Phase != "Prepared" {
			return errConfigurationBusy
		}
		return contract.ValidateManagerSettlement(r, time.Now())
	}
	if g == nil || g.Phase != "Released" {
		return errConfigurationBusy
	}
	if nc.Status.Configuration.Operation.Phase == "Verified" {
		if err := contract.ValidateCompletionReceipt(r, nc, time.Now()); err != nil {
			return err
		}
	} else if r.Status.Claim != nil || r.Status.Completion != nil {
		return errConfigurationBusy
	}
	return contract.ValidateManagerSettlement(r, time.Now())
}

// Recovery only observes an already accepted task. It never claims again and
// deliberately does not apply the new-submission freshness window to that task.
func (a *adapter) validatePhysicalConfigurationRecovery(ctx context.Context, nc *cvk.NetworkController) error {
	r, err := a.configurationReservation(ctx, nc)
	if err != nil {
		return err
	}
	op := nc.Status.Configuration.Operation
	claim := r.Status.Claim
	if (op.Phase != "Accepted" && op.Phase != "Verifying") || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || r.Status.Grant == nil || r.Status.Grant.Phase != "Held" || claim == nil || claim.OperationID != op.ID || claim.OperationDigest != r.Spec.OperationDigest || claim.ClaimedAt.IsZero() || op.SubmittedAt.Before(&claim.ClaimedAt) || r.Status.Completion != nil || a.workerPodName == "" || a.workerPodUID == "" || r.Spec.ControllerWorkerPod.Name != a.workerPodName || r.Spec.ControllerWorkerPod.UID != a.workerPodUID {
		return errConfigurationDeviceAdmission
	}
	return nil
}
