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
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PublishGroupCompletions publishes verified receipt evidence, never lease
// release. Authorize must validate the bound controller actor and installed
// group admission/recovery contract. Reader must be uncached. Existing matching
// completions are preserved so partial publication can recover without replaying
// a controller request. Conflicts/unknown writes are not retried in this call.
// Runtime group reconciliation remains disabled until its full lifecycle exists.
func PublishGroupCompletions(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group completion publication requires current verified actor-bound evidence")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || authorize == nil || now.IsZero() {
		return reject
	}
	read := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		var nc cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
			return nil, nil, err
		}
		if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion {
			return nil, nil, reject
		}
		current := make([]*ops.ControllerConfigurationReservation, 0, len(reservations))
		for _, old := range reservations {
			if old == nil || old.UID == "" || old.ResourceVersion == "" {
				return nil, nil, reject
			}
			var r ops.ControllerConfigurationReservation
			if err := reader.Get(ctx, client.ObjectKeyFromObject(old), &r); err != nil {
				return nil, nil, err
			}
			if r.UID != old.UID || r.ResourceVersion != old.ResourceVersion || !reflect.DeepEqual(r.Spec, old.Spec) {
				return nil, nil, reject
			}
			current = append(current, &r)
		}
		return &nc, current, nil
	}
	nc, current, err := read()
	if err != nil {
		return err
	}
	completions, err := GroupCompletionsForReceipt(nc, current, now)
	if err != nil {
		return err
	}
	if err := authorize(ctx, copyGroupReservations(current)); err != nil {
		return err
	}
	nc, current, err = read()
	if err != nil {
		return err
	}
	confirmed, err := GroupCompletionsForReceipt(nc, current, now)
	if err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(confirmed, completions) {
		return reject
	}
	// Preflight every member before publishing any new completion.
	for _, r := range current {
		completion := completions[r.UID]
		if r.Status.Completion != nil {
			if !equality.Semantic.DeepEqual(r.Status.Completion, completion) {
				return reject
			}
		} else if r.Status.Grant.Phase != "Held" {
			return reject
		}
	}
	for _, r := range current {
		if r.Status.Completion != nil {
			continue
		}
		// The controller receipt can change during a multi-member scan. A changed
		// version stops publication; earlier completions remain retained evidence.
		var nc cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
			return err
		}
		if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion {
			return reject
		}
		completion := completions[r.UID]
		r.Status.Completion = completion.DeepCopy()
		uid, spec := r.UID, r.Spec
		if err := writer.Status().Update(ctx, r); err != nil {
			return err
		}
		var persisted ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(r), &persisted); err != nil {
			return err
		}
		if persisted.UID != uid || !reflect.DeepEqual(persisted.Spec, spec) || !equality.Semantic.DeepEqual(persisted.Status.Completion, completion) {
			return reject
		}
	}
	var finalController cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &finalController); err != nil {
		return err
	}
	if finalController.UID != expected.UID || finalController.ResourceVersion != expected.ResourceVersion {
		return reject
	}
	final := make([]*ops.ControllerConfigurationReservation, 0, len(current))
	for _, r := range current {
		var fresh ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(r), &fresh); err != nil {
			return err
		}
		if fresh.UID != r.UID || !reflect.DeepEqual(fresh.Spec, r.Spec) {
			return reject
		}
		final = append(final, &fresh)
	}
	return ValidateGroupCompletionReceipts(&finalController, final, now)
}
