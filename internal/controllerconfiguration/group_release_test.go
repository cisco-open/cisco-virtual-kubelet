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

func TestMarkGroupMemberReleased(t *testing.T) {
	for _, mode := range []string{"valid", "missing-lease", "still-held", "expired-held", "replaced-held", "foreign-worker", "wrong-namespace", "missing-peer-completion", "subset", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc, members, now := groupCompletionFixture(t)
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
			if mode == "missing-peer-completion" {
				members[1].Status.Completion = nil
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			_ = coordination.AddToScheme(scheme)
			objects := []client.Object{nc}
			for _, r := range members {
				objects = append(objects, r)
			}
			if mode != "missing-lease" {
				objects = append(objects, lease)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, &ops.ControllerConfigurationReservation{}).WithObjects(objects...).Build()
			for _, r := range members {
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
			}
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
			supplied := members
			if mode == "subset" {
				supplied = members[:1]
			}
			err := MarkGroupMemberReleased(ctx, c, writer, nc, supplied, target, podUID, namespace, authorize, now)
			valid := mode == "valid" || mode == "missing-lease"
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				t.Fatal(err)
			}
			released := valid || mode == "lost-response"
			if (target.Status.Grant.Phase == "Released") != released {
				t.Fatal("release status inconsistent", target.Status.Grant.Phase)
			}
			if mode == "lost-response" {
				if err := MarkGroupMemberReleased(ctx, c, c, nc, members, target, podUID, namespace, authorize, now); err != nil {
					t.Fatal("release recovery failed", err)
				}
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
