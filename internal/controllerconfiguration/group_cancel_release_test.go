// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMarkCancelledGroupMemberReleased(t *testing.T) {
	t.Run("status", func(t *testing.T) { testCancelledGroupRelease(t, false) })
	t.Run("recovery", func(t *testing.T) { testCancelledGroupRelease(t, true) })
}

func TestCancelledGroupReleaseQuarantine(t *testing.T) {
	for _, mode := range []string{"released", "claimed-released", "held", "ungranted", "ungranted-settled", "ungranted-future-settlement", "missing-ack", "wrong-operation", "wrong-holder", "wrong-name", "future-ack", "ack-before-request", "completion", "invalid-claim", "unknown-protocol"} {
		t.Run(mode, func(t *testing.T) {
			_, members, _, now := groupGrantFixture(t)
			r := members[0]
			r.Status.Grant.Phase = "Released"
			requested, ack := metav1.NewTime(now.Add(-time.Second)), metav1.NewTime(now)
			r.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, RequestedAt: requested, AcknowledgedAt: ack}
			switch mode {
			case "claimed-released":
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: requested}
			case "held":
				r.Status.Grant.Phase = "Held"
			case "ungranted":
				r.Status.Grant = nil
			case "ungranted-settled", "ungranted-future-settlement":
				r.Status.Grant = nil
				settled := metav1.NewTime(now)
				if mode == "ungranted-future-settlement" {
					settled.Time = now.Add(time.Second)
				}
				r.Status.ManagerSettledAt = &settled
			case "missing-ack":
				r.Status.GroupCancellation = nil
			case "wrong-operation":
				r.Status.GroupCancellation.OperationID = "other"
			case "wrong-holder":
				r.Status.Grant.Holder = "other"
			case "wrong-name":
				r.Name = "other"
			case "future-ack":
				r.Status.GroupCancellation.AcknowledgedAt.Time = now.Add(time.Second)
			case "ack-before-request":
				r.Status.GroupCancellation.AcknowledgedAt.Time = now.Add(-2 * time.Second)
			case "completion":
				r.Status.Completion = &ops.ConfigurationReservationCompletion{}
			case "invalid-claim":
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: "other"}
			case "unknown-protocol":
				r.Spec.Protocol = "unknown"
			}
			settled := mode == "released" || mode == "claimed-released" || mode == "ungranted-settled"
			if RequiresQuarantine(r, now) == settled {
				t.Fatal("incorrect cancellation quarantine", mode)
			}
		})
	}
}

func testCancelledGroupRelease(t *testing.T, recovery bool) {
	for _, mode := range []string{"valid", "missing-lease", "still-held", "expired-held", "replaced-held", "foreign-worker", "wrong-namespace", "ungranted", "no-ack", "wrong-ack", "claimed", "revoked", "not-cancelling", "settled-held", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc, members, _, now := groupGrantFixture(t)
			at := metav1.NewTime(now.Add(-time.Second))
			nc.Status.Configuration.Operation.Phase = "Cancelling"
			nc.Status.Configuration.Operation.CancellationRequestedAt = &at
			for _, r := range members {
				r.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, RequestedAt: at, AcknowledgedAt: metav1.NewTime(now)}
			}
			_, _, leases, _ := groupGrantFixture(t)
			target := members[0]
			podUID := target.Spec.DeviceWorkerPod.UID
			namespace := "leases"
			lease := leases[0]
			switch mode {
			case "still-held":
			case "expired-held":
				at := metav1.NewMicroTime(now.Add(-time.Hour))
				lease.Spec.RenewTime = &at
			case "replaced-held":
				lease.UID = "replacement"
			default:
				lease.Spec.HolderIdentity = nil
			}
			if mode == "foreign-worker" {
				podUID = "other"
			}
			if mode == "wrong-namespace" {
				namespace = "other"
			}
			switch mode {
			case "ungranted":
				target.Status.Grant = nil
			case "no-ack":
				target.Status.GroupCancellation = nil
			case "wrong-ack":
				target.Status.GroupCancellation.OperationID = "other"
			case "claimed":
				target.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: target.Spec.OperationID, OperationDigest: target.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now)}
			case "revoked":
				target.Status.Grant.Phase = "Revoked"
			case "not-cancelling":
				nc.Status.Configuration.Operation.Phase = "Prepared"
				nc.Status.Configuration.Operation.CancellationRequestedAt = nil
			case "settled-held":
				target.Status.ManagerSettledAt = &at
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			_ = coordination.AddToScheme(scheme)
			objects := []client.Object{nc}
			// Peers may not have been created before cancellation.
			objects = append(objects, target)
			if mode != "missing-lease" {
				objects = append(objects, lease)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, &ops.ControllerConfigurationReservation{}).WithObjects(objects...).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				t.Fatal(err)
			}
			original := target.DeepCopy()
			if mode != "missing-lease" {
				if err := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					t.Fatal(err)
				}
			}
			before := lease.DeepCopy()
			writer := client.StatusClient(c)
			if mode == "lost-response" {
				writer = ambiguousStatus{Client: c}
			}
			authorize := func(context.Context, *ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			var err error
			if recovery {
				err = RecoverCancelledGroupMemberRelease(ctx, c, writer, nc, target, podUID, namespace, authorize, func(context.Context) error { return nil }, now)
			} else {
				err = MarkCancelledGroupMemberReleased(ctx, c, writer, nc, target, podUID, namespace, authorize, now)
			}
			valid := mode == "valid" || mode == "missing-lease" || mode == "claimed" || mode == "revoked" || (recovery && mode == "ungranted")
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				t.Fatal(err)
			}
			released := (valid || mode == "lost-response") && target.Status.Grant != nil
			if (target.Status.Grant != nil && target.Status.Grant.Phase == "Released") != released {
				t.Fatal("release status inconsistent")
			}
			if valid || mode == "lost-response" {
				rv := target.ResourceVersion
				var retryErr error
				if recovery && mode == "ungranted" {
					retryErr = RecoverCancelledGroupMemberRelease(ctx, c, c, nc, target, podUID, namespace, authorize, func(context.Context) error { return nil }, now)
				} else {
					retryErr = MarkCancelledGroupMemberReleased(ctx, c, c, nc, target, podUID, namespace, authorize, now)
				}
				if err := retryErr; err != nil {
					t.Fatal("release recovery failed", err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
					t.Fatal(err)
				}
				if target.ResourceVersion != rv {
					t.Fatal("repeated release rewrote evidence")
				}
			}
			if released {
				original.Status.Grant.Phase = "Released"
			}
			if !reflect.DeepEqual(original.Status, target.Status) {
				t.Fatal("release altered other evidence")
			}
			if mode != "missing-lease" {
				if err := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, lease) {
					t.Fatal("evidence helper modified lease")
				}
			}
		})
	}
}
