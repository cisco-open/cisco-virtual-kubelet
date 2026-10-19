// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Add receipt to the same in-memory status update that advances Verified.
// Resource-specific authoritative verification must precede this bookkeeping.
// Wireless provisioning keeps its specialized baseline-derived stable key.
func (a *adapter) retainPhysicalActionReceipt(ctx context.Context, nc *cvk.NetworkController) error {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || strings.HasPrefix(nc.Status.Configuration.Operation.Resource, wirelessProvisionRecordPrefix) {
		return errConfigurationTransition
	}
	return a.retainConfigurationActionReceipt(ctx, nc, nc.Status.Configuration.Operation.Resource)
}

func (a *adapter) retainConfigurationActionReceipt(ctx context.Context, nc *cvk.NetworkController, resource string) error {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	state, op := nc.Status.Configuration, nc.Status.Configuration.Operation
	created := validPnPRegistrationReceiptOperation(op) && resource == "pnp-record:"+op.CreatedResourceID
	pnpRemoval := validPnPRemovalOperation(op) && resource == pnpRemovedKey(op.Resource)
	physical := op.CreatedResourceID == "" && op.TargetSerial != "" && validAPIID(op.TargetID) && validAPIID(op.TaskID)
	switchingCreator := validSwitchingCreateOperation(op) && strings.HasPrefix(resource, switchingCreatedKey(op, ""))
	if switchingCreator {
		if _, err := switchingCreatedTarget(cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: resource, Operation: *op.DeepCopy(), VerifiedAt: &metav1.Time{Time: time.Now()}}); err != nil {
			return err
		}
		if err := switchingCreatorCapacity(state, op); err != nil {
			return err
		}
	}
	logicalCommit := validTemplateCommitReceipt(op) && resource == op.Resource
	if op.Phase != "Verified" || (!created && !physical && !logicalCommit && !pnpRemoval && !switchingCreator) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || len(op.CredentialVersions) != 0 || resource == "" || len(resource) > 512 {
		return errConfigurationTransition
	}
	owned := false
	for _, owner := range state.Owners {
		if owner.Section == op.Section && owner.ConfigName == op.ConfigName && owner.ConfigUID == op.ConfigUID {
			owned = true
		}
	}
	if !owned {
		return errConfigurationOwned
	}
	if pnpRemoval {
		if err := pnpRemovalCapacity(state, op); err != nil {
			return err
		}
	}
	now := time.Now()
	if err := validatePrivateTemplateReceiptPublication(state, op, now); err != nil {
		return err
	}
	index := -1
	for i, entry := range state.ActionApplications {
		if entry.Section != op.Section || entry.Resource != resource {
			continue
		}
		if index != -1 || entry.Operation.ConfigUID != op.ConfigUID {
			return errConfigurationOwned
		}
		index = i
		if entry.Operation.ID != op.ID {
			if created || switchingCreator {
				return errConfigurationOwned
			}
			// An older retained reservation still needs its independent receipt
			// for retirement. Do not replace it even after its device is released.
			name, err := contract.ReservationName(string(nc.UID), entry.Operation.ID)
			if err != nil {
				return err
			}
			var retained ops.ControllerConfigurationReservation
			err = a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: nc.Namespace, Name: name}, &retained)
			if err == nil {
				return errConfigurationBusy
			}
			if !apierrors.IsNotFound(err) {
				return err
			}
		} else if !reflect.DeepEqual(entry.Operation, *op) {
			return errConfigurationStale
		}
	}
	if index == -1 && len(state.ActionApplications) >= 256 {
		return errConfigurationTransition
	}
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: resource, Operation: *op.DeepCopy()}
	receipt.VerifiedAt = &metav1.Time{Time: now.UTC().Truncate(time.Second)}
	if index >= 0 && state.ActionApplications[index].Operation.ID == op.ID && state.ActionApplications[index].VerifiedAt != nil {
		previous := state.ActionApplications[index].VerifiedAt
		if previous.IsZero() || previous.After(time.Now()) || previous.Before(op.SubmittedAt) {
			return errConfigurationStale
		}
		receipt.VerifiedAt = previous.DeepCopy()
	}

	if index == -1 {
		state.ActionApplications = append(state.ActionApplications, receipt)
	} else {
		state.ActionApplications[index] = receipt
	}
	return nil
}

// Persist verification and receipt atomically, then ensure the API retained both.
// A lost response is returned for reconciliation; it never retries a device call.
func (a *adapter) persistConfigurationActionReceipt(ctx context.Context, nc *cvk.NetworkController) error {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	var expected *cvk.NetworkControllerConfigurationActionApplication
	for _, entry := range nc.Status.Configuration.ActionApplications {
		if entry.Operation.ID == op.ID {
			if expected != nil || !reflect.DeepEqual(entry.Operation, *op) {
				return errConfigurationStale
			}
			expected = entry.DeepCopy()
		}
	}
	if expected == nil {
		return errConfigurationStale
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if stored.Status.Configuration == nil || !reflect.DeepEqual(stored.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	count := 0
	for _, entry := range stored.Status.Configuration.ActionApplications {
		if entry.Operation.ID == op.ID {
			if !apiequality.Semantic.DeepEqual(entry, *expected) {
				return errConfigurationStale
			}
			count++
		}
	}
	if count != 1 {
		return errConfigurationStale
	}
	return nil
}
