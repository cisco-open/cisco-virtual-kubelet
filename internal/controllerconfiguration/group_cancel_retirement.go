// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"slices"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Cleanup of a historically closed cancellation is distinct from cancelling a
// live or partially created group. The complete retained membership list must
// bind this member's UID even after peers have already been retired.
func validateCancelledGroupMemberReceipt(nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation, now time.Time) error {
	reject := errors.New("cancelled group member lacks complete retained cancellation")
	if nc == nil || r == nil || nc.Status.Configuration == nil || string(nc.UID) != r.Spec.Controller.UID || nc.Name != r.Spec.Controller.Name || nc.Namespace != r.Namespace || now.IsZero() {
		return reject
	}
	if active := nc.Status.Configuration.Operation; active != nil && active.ID == r.Spec.OperationID {
		return reject
	}
	var receipt *cvk.NetworkControllerConfigurationGroupCancellation
	for i := range nc.Status.Configuration.GroupCancellations {
		candidate := &nc.Status.Configuration.GroupCancellations[i]
		if candidate.OperationID != r.Spec.OperationID {
			continue
		}
		if receipt != nil {
			return reject
		}
		receipt = candidate
	}
	if receipt == nil || receipt.Operation.ID != receipt.OperationID || receipt.SettledAt.IsZero() || receipt.SettledAt.After(now) {
		return reject
	}
	audit := nc.DeepCopy()
	audit.Status.Configuration.Operation = receipt.Operation.DeepCopy()
	if err := ValidateGroupCancellationFence(audit, now); err != nil {
		return err
	}
	targets, err := CanonicalGroupTargets(receipt.Operation.PhysicalGroup.Targets)
	if err != nil {
		return err
	}
	if len(receipt.Members) != len(targets) {
		return reject
	}
	names := map[string]bool{}
	for _, target := range targets {
		names[groupMemberReservationName(string(nc.UID), receipt.OperationID, receipt.Operation.PhysicalGroup.Digest, target.PhysicalIdentity)] = true
	}
	refs, uids := map[string]string{}, map[string]bool{}
	for _, ref := range receipt.Members {
		if !names[ref.Name] || !validGroupIdentityToken(ref.UID) || refs[ref.Name] != "" || uids[ref.UID] {
			return reject
		}
		refs[ref.Name] = ref.UID
		uids[ref.UID] = true
	}
	if refs[r.Name] != string(r.UID) {
		return reject
	}
	row := r.DeepCopy()
	row.DeletionTimestamp = nil
	if !slices.Contains(row.Finalizers, Finalizer) {
		row.Finalizers = append(row.Finalizers, Finalizer)
	}
	if err := ValidateCancelledGroupMemberSettlement(audit, row, now); err != nil {
		return err
	}
	if receipt.SettledAt.Before(row.Status.ManagerSettledAt) {
		return reject
	}
	return nil
}

// RetireCancelledGroupReservation deletes one settled member after the group
// cancellation was durably closed. It preserves the cancellation receipt/fence,
// ownership, other members and leases. Authorize must enforce group protocol and
// manager identity; no runtime caller is enabled by this staged helper.
func RetireCancelledGroupReservation(ctx context.Context, reader client.Reader, writer client.Writer, expected *ops.ControllerConfigurationReservation, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	return retireGroupReservation(ctx, reader, writer, expected, authorize, validateCancelledGroupMemberReceipt, now)
}
