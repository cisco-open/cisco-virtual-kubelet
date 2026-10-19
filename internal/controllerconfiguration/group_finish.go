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

// FinishVerifiedControllerGroup resolves the journal's complete canonical member
// set before applying the existing settlement/CAS contract. Missing members are
// never interpreted as retired: retirement is permitted only after this journal
// has cleared. Runtime authorization remains mandatory for every member.
func FinishVerifiedControllerGroup(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	members, err := ReadVerifiedGroupReservations(ctx, reader, expected, now)
	if err != nil {
		return err
	}
	if authorize == nil {
		return errors.New("group journal completion authority is unavailable")
	}
	return FinishVerifiedGroup(ctx, reader, writer, expected, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		for _, member := range current {
			if err := authorize(ctx, member); err != nil {
				return err
			}
		}
		return nil
	}, now)
}

// ReadVerifiedGroupReservations resolves every member of a Verified journal and
// corroborates its retained receipt and claims. Completion publication and actor
// authorization remain separate; this reader cannot clear or release anything.
func ReadVerifiedGroupReservations(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	members, err := readJournalGroupReservations(ctx, reader, expected)
	if err != nil {
		return nil, err
	}
	if expected.Status.Configuration.Operation.Phase != "Verified" {
		return nil, errors.New("group journal is not verified")
	}
	if _, err := GroupCompletionsForReceipt(expected, members, now); err != nil {
		return nil, err
	}
	return members, nil
}

func readJournalGroupReservations(ctx context.Context, reader client.Reader, expected *cvk.NetworkController) ([]*ops.ControllerConfigurationReservation, error) {
	if reader == nil || expected == nil || expected.Status.Configuration == nil || expected.Status.Configuration.Operation == nil {
		return nil, errors.New("group journal completion evidence is unavailable")
	}
	op := expected.Status.Configuration.Operation
	if ValidateGroupJournalIdentity(op) != nil {
		return nil, errors.New("group journal is not verified")
	}
	targets, err := CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	members := make([]*ops.ControllerConfigurationReservation, len(targets))
	for i, target := range targets {
		row := &ops.ControllerConfigurationReservation{}
		key := client.ObjectKey{Namespace: expected.Namespace, Name: groupMemberReservationName(string(expected.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity)}
		if err := reader.Get(ctx, key, row); err != nil {
			return nil, err
		}
		members[i] = row
	}
	return members, nil
}

// FinishVerifiedGroup clears only the exact Verified journal after complete
// receipt, device release and manager-settlement evidence. Action receipts and
// section ownership remain retained. Authorize must validate the current writer
// and installed group lifecycle contract. There is no CAS retry and no remote
// request; uncertain journal writes are resolved by subsequent observation.
func FinishVerifiedGroup(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group journal cannot clear before exact complete settlement")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || expected.Status.Configuration == nil || expected.Status.Configuration.Operation == nil || authorize == nil || now.IsZero() {
		return reject
	}
	op := expected.Status.Configuration.Operation
	if op.Phase != "Verified" || ValidateGroupJournalIdentity(op) != nil || len(members) == 0 || members[0] == nil || members[0].Spec.OperationID != op.ID {
		return reject
	}
	read := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, expected, members)
		if err != nil {
			return nil, nil, err
		}
		if nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || !equality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) {
			return nil, nil, reject
		}
		if err := ValidateGroupManagerSettlement(nc, current, now); err != nil {
			return nil, nil, err
		}
		return nc, current, nil
	}
	_, current, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, copyGroupReservations(current)); err != nil {
		return err
	}
	nc, _, err := read()
	if err != nil {
		return err
	}
	nc.Status.Configuration.Operation = nil
	retained := nc.Status.Configuration.DeepCopy()
	if err := writer.Status().Update(ctx, nc); err != nil {
		return err
	}
	var persisted cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &persisted); err != nil {
		return err
	}
	if persisted.UID != expected.UID || !equality.Semantic.DeepEqual(persisted.Status.Configuration, retained) {
		return reject
	}
	return nil
}
