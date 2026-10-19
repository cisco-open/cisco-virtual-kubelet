// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PersistVerifiedGroupReceipt atomically advances the exact Verifying operation
// and retains independently verified resource evidence. The adapter must verify
// the remote task/readback before calling this helper. authorize validates the
// live writer and installed group protocol; it is mandatory even for recovery.
// No remote operation, grant release, journal clear or CAS retry occurs here.
func PersistVerifiedGroupReceipt(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, receipt *cvk.NetworkControllerConfigurationActionApplication, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	return persistVerifiedGroupReceipt(ctx, reader, writer, expected, members, receipt, authorize, now, nil)
}

// PersistReplacingVerifiedGroupReceipt requires the exact retained predecessor
// and independently checks that no prior group reservation remains.
func PersistReplacingVerifiedGroupReceipt(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, receipt *cvk.NetworkControllerConfigurationActionApplication, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time, previous *cvk.NetworkControllerConfigurationActionApplication) error {
	if previous == nil {
		return errors.New("replacement receipt requires predecessor")
	}
	return persistVerifiedGroupReceipt(ctx, reader, writer, expected, members, receipt, authorize, now, previous.DeepCopy())
}

func persistVerifiedGroupReceipt(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, receipt *cvk.NetworkControllerConfigurationActionApplication, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time, previous *cvk.NetworkControllerConfigurationActionApplication) error {
	reject := errors.New("group receipt cannot persist without exact verifying journal and complete claimed members")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || expected.Status.Configuration == nil || expected.Status.Configuration.Operation == nil || receipt == nil || authorize == nil || now.IsZero() {
		return reject
	}
	expected = expected.DeepCopy()
	receipt = receipt.DeepCopy()
	members = copyGroupReservations(members)
	op := expected.Status.Configuration.Operation
	if op.Phase != "Verifying" || ValidateGroupJournalIdentity(op) != nil || receipt.Section != op.Section || receipt.Resource == "" || len(receipt.Resource) > 512 {
		return reject
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	if !equality.Semantic.DeepEqual(verified, &receipt.Operation) {
		return reject
	}
	build := func(nc *cvk.NetworkController, current []*ops.ControllerConfigurationReservation) (*cvk.NetworkController, error) {
		if !nc.DeletionTimestamp.IsZero() || nc.Generation != op.ControllerGeneration || nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || !equality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) {
			return nil, reject
		}
		owners := 0
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.Section != op.Section {
				continue
			}
			if owner.ConfigName != op.ConfigName || owner.ConfigUID != op.ConfigUID {
				return nil, reject
			}
			owners++
		}
		if owners != 1 {
			return nil, reject
		}
		replacement := -1
		for i, old := range nc.Status.Configuration.ActionApplications {
			if old.Operation.ID == op.ID {
				return nil, reject
			}
			if old.Section != receipt.Section || old.Resource != receipt.Resource {
				continue
			}
			if previous == nil || replacement != -1 || !equality.Semantic.DeepEqual(old, *previous) {
				return nil, reject
			}
			if err := validateRetiredGroupReceipt(ctx, reader, nc, previous, receipt, now); err != nil {
				return nil, err
			}
			replacement = i
		}
		if previous != nil && replacement == -1 {
			return nil, reject
		}
		if replacement == -1 && len(nc.Status.Configuration.ActionApplications) >= 256 {
			return nil, reject
		}

		if _, err := ValidateGroupReservationSet(nc, current); err != nil {
			return nil, err
		}
		for _, r := range current {
			if !r.DeletionTimestamp.IsZero() || r.Status.Grant == nil || r.Status.Grant.Phase != "Held" || r.Status.Completion != nil || r.Status.ManagerSettledAt != nil {
				return nil, reject
			}
		}
		result := nc.DeepCopy()
		result.Status.Configuration.Operation = verified.DeepCopy()
		if replacement < 0 {
			result.Status.Configuration.ActionApplications = append(result.Status.Configuration.ActionApplications, *receipt.DeepCopy())
		} else {
			result.Status.Configuration.ActionApplications[replacement] = *receipt.DeepCopy()
		}
		// Shared completion validation binds claims, submission timing, full-group
		// identity and receipt timestamps; persistence does not duplicate that logic.
		if _, err := GroupCompletionsForReceipt(result, current, now); err != nil {
			return nil, err
		}
		return result, nil
	}
	read := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, expected, members)
		if err != nil {
			return nil, nil, err
		}
		next, err := build(nc, current)
		return next, current, err
	}
	_, current, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, copyGroupReservations(current)); err != nil {
		return err
	}
	next, fresh, err := read()
	if err != nil {
		return err
	}
	// Authorize was based on the first complete set. A changed member requires a
	// fresh authorization cycle, even when its new status also looks admissible.
	for i := range current {
		if current[i].ResourceVersion != fresh[i].ResourceVersion || !equality.Semantic.DeepEqual(current[i], fresh[i]) {
			return reject
		}
	}
	retained := next.Status.Configuration.DeepCopy()
	if err := writer.Status().Update(ctx, next); err != nil {
		return err
	}
	var stored cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &stored); err != nil {
		return err
	}
	if stored.UID != expected.UID || !equality.Semantic.DeepEqual(stored.Status.Configuration, retained) {
		return reject
	}
	return nil
}
