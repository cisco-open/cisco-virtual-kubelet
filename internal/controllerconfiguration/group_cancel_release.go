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
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RecoverCancelledGroupMemberRelease executes only the caller's canonical
// release after acknowledgement, then uses the existing release status writer.
// No completion is fabricated. Ungranted cancellation settlement is separate.
func RecoverCancelledGroupMemberRelease(ctx context.Context, reader client.Reader, writer client.StatusClient, controller *cvk.NetworkController, target *ops.ControllerConfigurationReservation, podUID, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, release func(context.Context) error, now time.Time) error {
	reject := errors.New("cancelled group release authority is incomplete")
	if reader == nil || writer == nil || target == nil || controller == nil || authorize == nil || release == nil || namespace == "" || podUID == "" || target.Spec.DeviceWorkerPod.UID != podUID || now.IsZero() {
		return reject
	}
	holder, err := Holder(target.Namespace, target)
	g := target.Status.Grant
	canonical := engine.LeaseName(devicecoordination.DeviceKey(target.Namespace, target.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	if err != nil || (g != nil && (g.Holder != holder || g.LeaseNamespace != namespace || g.Lease.Name != canonical || g.Lease.UID == "")) {
		return reject
	}
	read := func() error {
		nc, rows, err := readGroupSettlementObjects(ctx, reader, controller, []*ops.ControllerConfigurationReservation{target})
		if err != nil {
			return err
		}
		if rows[0].ResourceVersion != target.ResourceVersion || !equality.Semantic.DeepEqual(rows[0].Status, target.Status) {
			return reject
		}
		return ValidateGroupCancellationAcknowledgement(nc, rows[0], now)
	}
	if err := read(); err != nil {
		return err
	}
	if err := authorize(ctx, target.DeepCopy()); err != nil {
		return err
	}
	if err := read(); err != nil {
		return err
	}
	if g == nil || g.Phase != "Released" {
		if err := release(ctx); err != nil {
			return err
		}
	}
	if g == nil {
		// No grant exists to mark Released. Keep the acknowledgement and require
		// observed absence of this holder; manager settlement records cleanup.
		if err := read(); err != nil {
			return err
		}
		if err := authorize(ctx, target.DeepCopy()); err != nil {
			return err
		}
		if err := read(); err != nil {
			return err
		}
		return readConfigurationLeaseWithoutHolder(ctx, reader, namespace, canonical, holder)
	}
	return MarkCancelledGroupMemberReleased(ctx, reader, writer, controller, target, podUID, namespace, authorize, now)
}

// MarkCancelledGroupMemberReleased records actual release after the durable
// cancellation fence and member acknowledgement. Unlike verified completion,
// cancellation may interrupt reservation creation, so this checks one member
// against the entire journal identity without requiring every peer to exist.
// It preserves claims and never fabricates a completion receipt, releases a
// Lease, removes a taint, or settles a session. Ungranted members have no grant
// to release and must follow the separate manager cancellation settlement path.
// Authorize must verify installed group admission and the bound device-worker
// actor. The reader must be uncached. Group runtime remains disabled.
func MarkCancelledGroupMemberReleased(ctx context.Context, reader client.Reader, writer client.StatusClient, controller *cvk.NetworkController, expected *ops.ControllerConfigurationReservation, podUID, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("cancelled group member lacks release authority")
	if reader == nil || writer == nil || controller == nil || controller.UID == "" || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || podUID == "" || namespace == "" || authorize == nil || now.IsZero() {
		return reject
	}
	read := func() (*ops.ControllerConfigurationReservation, error) {
		nc, members, err := readGroupSettlementObjects(ctx, reader, controller, []*ops.ControllerConfigurationReservation{expected})
		if err != nil {
			return nil, err
		}
		r := members[0]
		if r.ResourceVersion != expected.ResourceVersion || r.Spec.DeviceWorkerPod.UID != podUID || r.Status.Grant == nil || r.Status.Grant.Lease.UID == "" {
			return nil, reject
		}
		if err := ValidateGroupCancellationAcknowledgement(nc, r, now); err != nil {
			return nil, err
		}
		return r, nil
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
	if r.Status.ManagerSettledAt != nil {
		return reject
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
