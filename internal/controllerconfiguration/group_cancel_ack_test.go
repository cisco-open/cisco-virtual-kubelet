// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAcknowledgeGroupCancellation(t *testing.T) {
	for _, mode := range []string{"ungranted", "held", "claimed", "peer-not-created", "foreign-worker", "not-cancelling", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc, members, _, now := groupGrantFixture(t)
			at := metav1.NewTime(now.Add(-time.Second))
			nc.Status.Configuration.Operation.Phase = "Cancelling"
			nc.Status.Configuration.Operation.CancellationRequestedAt = &at
			r := members[0]
			if mode == "ungranted" {
				r.Status.Grant = nil
			}
			if mode == "claimed" {
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now)}
			}
			if mode == "not-cancelling" {
				nc.Status.Configuration.Operation.Phase = "Prepared"
				nc.Status.Configuration.Operation.CancellationRequestedAt = nil
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			objects := []client.Object{nc, r}
			if mode != "peer-not-created" {
				objects = append(objects, members[1])
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, r).WithObjects(objects...).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			before := r.DeepCopy()
			podUID := r.Spec.DeviceWorkerPod.UID
			if mode == "foreign-worker" {
				podUID = "other"
			}
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
			err := AcknowledgeGroupCancellation(ctx, c, writer, nc, r, podUID, authorize, now)
			valid := mode == "ungranted" || mode == "held" || mode == "claimed" || mode == "peer-not-created"
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			if (r.Status.GroupCancellation != nil) != (valid || mode == "lost-response") {
				t.Fatal("acknowledgement persistence mismatch")
			}
			if (r.Status.Claim == nil) != (before.Status.Claim == nil) || (r.Status.Grant == nil) != (before.Status.Grant == nil) {
				t.Fatal("cancellation erased claim/grant evidence")
			}
			if valid || mode == "lost-response" {
				rv := r.ResourceVersion
				if err := AcknowledgeGroupCancellation(ctx, c, c, nc, r, podUID, authorize, now); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
				if r.ResourceVersion != rv {
					t.Fatal("acknowledgement replay changed evidence")
				}
			}
		})
	}
}

func TestGroupCancellationAcknowledgementLosesRacingClaim(t *testing.T) {
	ctx := context.Background()
	c, nc, members, now := groupAuthorityFixture(t)
	at := metav1.NewTime(now)
	nc.Status.Configuration.Operation.Phase = "Cancelling"
	nc.Status.Configuration.Operation.CancellationRequestedAt = &at
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	r := members[0]
	writer := groupAckRaceWriter{Client: c, before: func() error {
		var fresh ops.ControllerConfigurationReservation
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), &fresh); err != nil {
			return err
		}
		fresh.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now)}
		return c.Status().Update(ctx, &fresh)
	}}
	authorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
	if err := AcknowledgeGroupCancellation(ctx, c, writer, nc, r, r.Spec.DeviceWorkerPod.UID, authorize, now); err == nil {
		t.Fatal("acknowledgement overwrote racing claim")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Claim == nil || r.Status.GroupCancellation != nil {
		t.Fatal("racing claim evidence lost")
	}
	if err := AcknowledgeGroupCancellation(ctx, c, c, nc, r, r.Spec.DeviceWorkerPod.UID, authorize, now); err != nil {
		t.Fatal("fresh acknowledgement did not retain late claim", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Claim == nil || r.Status.GroupCancellation == nil {
		t.Fatal("late claim was erased during cancellation")
	}
}

type groupAckRaceWriter struct {
	client.Client
	before func() error
}

func (w groupAckRaceWriter) Status() client.SubResourceWriter {
	return groupAckRaceStatus{SubResourceWriter: w.Client.Status(), before: w.before}
}

type groupAckRaceStatus struct {
	client.SubResourceWriter
	before func() error
}

func (w groupAckRaceStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if err := w.before(); err != nil {
		return err
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
