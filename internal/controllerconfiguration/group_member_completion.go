// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadGroupMemberCompletion corroborates the complete retained receipt set for
// a manager's exact member. Lease/session cleanup and actor authorization remain
// separate; this read cannot by itself restore scheduling or clear a journal.
func ReadGroupMemberCompletion(ctx context.Context, reader client.Reader, member *ops.ControllerConfigurationReservation, now time.Time) error {
	_, _, err := readGroupMemberCompletionSet(ctx, reader, member, now)
	return err
}

func readGroupMemberCompletionSet(ctx context.Context, reader client.Reader, member *ops.ControllerConfigurationReservation, now time.Time) (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group member lacks complete current verified receipts")
	if reader == nil || member == nil || member.UID == "" || member.ResourceVersion == "" || member.Spec.Protocol != GroupReservationProtocol {
		return nil, nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Controller.Name}, &nc); err != nil {
		return nil, nil, err
	}
	if string(nc.UID) != member.Spec.Controller.UID {
		return nil, nil, reject
	}
	receipt, err := verifiedGroupReceipt(&nc, member.Spec.OperationID, now)
	if err != nil {
		return nil, nil, err
	}
	targets, err := CanonicalGroupTargets(receipt.Operation.PhysicalGroup.Targets)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]*ops.ControllerConfigurationReservation, len(targets))
	found := false
	for i, target := range targets {
		var row ops.ControllerConfigurationReservation
		key := client.ObjectKey{Namespace: nc.Namespace, Name: groupMemberReservationName(string(nc.UID), receipt.Operation.ID, receipt.Operation.PhysicalGroup.Digest, target.PhysicalIdentity)}
		if err := reader.Get(ctx, key, &row); err != nil {
			return nil, nil, err
		}
		if row.UID == member.UID && row.Name == member.Name {
			if row.ResourceVersion != member.ResourceVersion || !reflect.DeepEqual(row.Spec, member.Spec) || !reflect.DeepEqual(row.Status, member.Status) {
				return nil, nil, reject
			}
			found = true
		}
		rows[i] = &row
	}
	if !found {
		return nil, nil, reject
	}
	if err := ValidateGroupCompletionReceipts(&nc, rows, now); err != nil {
		return nil, nil, err
	}
	objects := []client.Object{&nc}
	for _, row := range rows {
		objects = append(objects, row)
	}
	for _, old := range objects {
		fresh := old.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(old), fresh); err != nil {
			return nil, nil, err
		}
		if fresh.GetUID() != old.GetUID() || fresh.GetResourceVersion() != old.GetResourceVersion() {
			return nil, nil, reject
		}
	}
	return &nc, rows, nil
}
