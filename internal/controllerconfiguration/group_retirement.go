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
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Retirement is per settled member: peers may already have been deleted. This
// historical audit cannot be used to publish completion or release a live grant,
// which continue to require full-group evidence. The shared receipt is retained.
func validateSettledGroupMemberReceipt(nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation, now time.Time) error {
	reject := errors.New("settled group reservation does not match retained completion")
	if nc == nil || r == nil || nc.Status.Configuration == nil || string(nc.UID) != r.Spec.Controller.UID || nc.Name != r.Spec.Controller.Name || nc.Namespace != r.Namespace {
		return reject
	}
	if active := nc.Status.Configuration.Operation; active != nil && active.ID == r.Spec.OperationID {
		return reject
	}
	if err := validateGroupMemberSettlement(r, now); err != nil {
		return err
	}
	receipt, err := verifiedGroupReceipt(nc, r.Spec.OperationID, now)
	if err != nil {
		return err
	}
	audit := nc.DeepCopy()
	audit.Status.Configuration.Operation = receipt.Operation.DeepCopy()
	row := r.DeepCopy()
	row.DeletionTimestamp = nil
	// An ambiguous finalizer removal can be resumed from the same UID. The
	// historical identity audit must not confuse that with incomplete settlement.
	if !slices.Contains(row.Finalizers, Finalizer) {
		row.Finalizers = append(row.Finalizers, Finalizer)
	}
	targets, err := CanonicalGroupTargets(receipt.Operation.PhysicalGroup.Targets)
	if err != nil {
		return err
	}
	found := false
	for _, target := range targets {
		if target.PhysicalIdentity != r.Spec.PhysicalIdentity {
			continue
		}
		if found || validateGroupReservationMember(audit, row, target) != nil {
			return reject
		}
		found = true
	}
	if !found {
		return reject
	}
	completion, err := completionForGroupMember(receipt, r)
	if err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(completion, r.Status.Completion) {
		return reject
	}
	return nil
}

// RetireSettledGroupReservation removes only this exact member after its old
// journal has cleared. authorize must enforce the installed group protocol and
// manager actor. It preserves controller receipts, peers, devices and leases.
// A lost write response is returned; a later call must start from a fresh object.
func RetireSettledGroupReservation(ctx context.Context, reader client.Reader, writer client.Writer, expected *ops.ControllerConfigurationReservation, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	return retireGroupReservation(ctx, reader, writer, expected, authorize, validateSettledGroupMemberReceipt, now)
}

// Shared CAS/delete mechanics; lifecycle-specific receipt validation stays in
// its own validator and cannot accidentally treat cancellation as completion.
func retireGroupReservation(ctx context.Context, reader client.Reader, writer client.Writer, expected *ops.ControllerConfigurationReservation, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, validate func(*cvk.NetworkController, *ops.ControllerConfigurationReservation, time.Time) error, now time.Time) error {
	reject := errors.New("group reservation is not ready for retirement")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || authorize == nil || validate == nil || now.IsZero() {
		return reject
	}
	expected = expected.DeepCopy()
	read := func() (*ops.ControllerConfigurationReservation, *cvk.NetworkController, error) {
		var r ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &r); err != nil {
			return nil, nil, err
		}
		if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !equality.Semantic.DeepEqual(r.Spec, expected.Spec) || len(r.OwnerReferences) != 0 {
			return nil, nil, reject
		}
		var nc cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
			return nil, nil, err
		}
		if err := validate(&nc, &r, now); err != nil {
			return nil, nil, err
		}
		return &r, &nc, nil
	}
	r, nc, err := read()
	if apierrors.IsNotFound(err) {
		// Only an absent reservation is already retired; a missing controller cannot
		// erase the receipt requirement for a reservation which still exists.
		var existing ops.ControllerConfigurationReservation
		if check := reader.Get(ctx, client.ObjectKeyFromObject(expected), &existing); apierrors.IsNotFound(check) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	if err := authorize(ctx, r.DeepCopy()); err != nil {
		return err
	}
	r, fresh, err := read()
	if err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return reject
	}
	if slices.Contains(r.Finalizers, Finalizer) {
		r.Finalizers = slices.DeleteFunc(r.Finalizers, func(v string) bool { return v == Finalizer })
		if err := writer.Update(ctx, r); err != nil {
			return err
		}
	}
	err = writer.Delete(ctx, r, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &r.UID, ResourceVersion: &r.ResourceVersion}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
