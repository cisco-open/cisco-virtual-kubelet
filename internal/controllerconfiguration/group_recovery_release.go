// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RecoverGroupMemberRelease releases only the calling worker's member after
// corroborating every completion receipt and current recovery authorization.
// Release must use the canonical reservation holder and must not acquire a
// lease. Missing/uncertain evidence leaves the worker's existing fence intact.
// No current desired intent or new controller task is inferred during recovery.
func RecoverGroupMemberRelease(ctx context.Context, reader client.Reader, writer client.StatusClient, target *ops.ControllerConfigurationReservation, podUID, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, release func(context.Context) error, now time.Time) error {
	reject := errors.New("group recovery release authority is incomplete")
	if reader == nil || writer == nil || target == nil || podUID == "" || namespace == "" || authorize == nil || release == nil || now.IsZero() || target.Spec.DeviceWorkerPod.UID != podUID || target.Status.Grant == nil {
		return reject
	}
	g := target.Status.Grant
	holder, err := Holder(target.Namespace, target)
	canonical := engine.LeaseName(devicecoordination.DeviceKey(target.Namespace, target.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	if err != nil || g.LeaseNamespace != namespace || g.Lease.Name != canonical || g.Holder != holder {
		return reject
	}
	if _, _, err := readGroupMemberCompletionSet(ctx, reader, target, now); err != nil {
		return err
	}
	if err := authorize(ctx, target.DeepCopy()); err != nil {
		return err
	}
	controller, members, err := readGroupMemberCompletionSet(ctx, reader, target, now)
	if err != nil {
		return err
	}
	if g.Phase != "Released" {
		if err := release(ctx); err != nil {
			return err
		}
	}
	// Recheck receipts and authorization after release, then publish with a
	// versioned status write. A lost response is recovered without remote work.
	return MarkGroupMemberReleased(ctx, reader, writer, controller, members, target, podUID, namespace, authorize, now)
}
