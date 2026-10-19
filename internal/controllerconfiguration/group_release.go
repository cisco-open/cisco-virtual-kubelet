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

// MarkGroupMemberReleased records one device worker's release only after every
// group completion matches the verified receipt and its canonical Lease no
// longer carries this reservation's holder. Lease expiry is insufficient. It
// never deletes a Lease or removes a taint. Authorize must verify installed group
// admission/recovery and the bound device-worker actor; readers must be uncached.
// Group runtime reconciliation remains disabled pending the full lifecycle.
func MarkGroupMemberReleased(ctx context.Context, reader client.Reader, writer client.StatusClient, controller *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, target *ops.ControllerConfigurationReservation, podUID, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group member lacks verified release authority")
	if reader == nil || writer == nil || controller == nil || controller.UID == "" || target == nil || target.UID == "" || target.ResourceVersion == "" || podUID == "" || namespace == "" || authorize == nil || now.IsZero() {
		return reject
	}
	read := func() (*ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, controller, members)
		if err != nil {
			return nil, err
		}
		if err := ValidateGroupCompletionReceipts(nc, current, now); err != nil {
			return nil, err
		}
		for _, r := range current {
			if r.UID != target.UID {
				continue
			}
			if r.Name != target.Name || r.Namespace != target.Namespace || r.ResourceVersion != target.ResourceVersion || !reflect.DeepEqual(r.Spec, target.Spec) || r.Spec.DeviceWorkerPod.UID != podUID || r.Status.Grant.Lease.UID == "" {
				return nil, reject
			}
			return r, nil
		}
		return nil, reject
	}
	r, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, r.DeepCopy()); err != nil {
		return err
	}
	r, err = read()
	if err != nil {
		return err
	}
	if err := readReleasedConfigurationLease(ctx, reader, r, namespace); err != nil {
		return err
	}
	if r.Status.Grant.Phase == "Released" {
		return nil
	}
	r.Status.Grant.Phase = "Released"
	intended := r.DeepCopy()
	if err := writer.Status().Update(ctx, r); err != nil {
		return err
	}
	var persisted ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(intended), &persisted); err != nil {
		return err
	}
	if persisted.UID != intended.UID || !reflect.DeepEqual(persisted.Spec, intended.Spec) || !equality.Semantic.DeepEqual(persisted.Status, intended.Status) {
		return reject
	}
	return nil
}

// Receipt audit tolerates later controller versions but never replacement UIDs.
// Member versions may advance during independent release; each writer separately
// binds its own target version before a CAS. Full immutable group identity and
// receipt validation must follow these reads.
func readGroupSettlementObjects(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation) (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group settlement objects changed or are unavailable")
	if reader == nil || expected == nil || expected.UID == "" || len(members) == 0 || len(members) > 1000 {
		return nil, nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, nil, err
	}
	if nc.UID != expected.UID {
		return nil, nil, reject
	}
	current := make([]*ops.ControllerConfigurationReservation, 0, len(members))
	for _, old := range members {
		if old == nil || old.UID == "" {
			return nil, nil, reject
		}
		var r ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(old), &r); err != nil {
			return nil, nil, err
		}
		if r.UID != old.UID || !reflect.DeepEqual(r.Spec, old.Spec) {
			return nil, nil, reject
		}
		current = append(current, &r)
	}
	return &nc, current, nil
}
