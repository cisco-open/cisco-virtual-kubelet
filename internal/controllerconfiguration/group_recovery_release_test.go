// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRecoverGroupMemberRelease(t *testing.T) {
	for _, mode := range []string{"valid", "released", "missing-peer-completion", "missing-authorizer", "denied", "changed-after-authorization", "wrong-worker", "wrong-namespace", "release-error", "release-noop", "lost-status-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc, members, now := groupCompletionFixture(t)
			_, _, leases, _ := groupGrantFixture(t)
			target, lease := members[0], leases[0]
			pod, namespace := target.Spec.DeviceWorkerPod.UID, "leases"
			if mode == "missing-peer-completion" {
				members[1].Status.Completion = nil
			}
			if mode == "released" {
				target.Status.Grant.Phase = "Released"
				lease.Spec.HolderIdentity = nil
			}
			if mode == "wrong-worker" {
				pod = "foreign"
			}
			if mode == "wrong-namespace" {
				namespace = "foreign"
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			_ = coordination.AddToScheme(scheme)
			objects := []client.Object{nc}
			for _, r := range members {
				objects = append(objects, r)
			}
			for _, l := range leases {
				objects = append(objects, l)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, target).WithObjects(objects...).Build()
			for _, r := range members {
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
			}
			peer := members[1].DeepCopy()
			authCalls, releases := 0, 0
			authorize := func(context.Context, *ops.ControllerConfigurationReservation) error {
				authCalls++
				if mode == "denied" {
					return errors.New("denied")
				}
				if mode == "changed-after-authorization" {
					members[1].Status.Completion = nil
					return c.Status().Update(ctx, members[1])
				}
				return nil
			}
			if mode == "missing-authorizer" {
				authorize = nil
			}
			release := func(context.Context) error {
				releases++
				if mode == "release-error" {
					return errors.New("release unavailable")
				}
				if mode == "release-noop" {
					return nil
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
					return err
				}
				lease.Spec.HolderIdentity = nil
				return c.Update(ctx, lease)
			}
			writer := client.StatusClient(c)
			if mode == "lost-status-response" {
				writer = ambiguousStatus{Client: c}
			}
			err := RecoverGroupMemberRelease(ctx, c, writer, target, pod, namespace, authorize, release, now)
			valid := mode == "valid" || mode == "released"
			if (err == nil) != valid {
				t.Fatalf("%s: %v", mode, err)
			}
			wantRelease := mode == "valid" || mode == "release-error" || mode == "release-noop" || mode == "lost-status-response"
			if (releases == 1) != wantRelease {
				t.Fatalf("unexpected release calls: %d", releases)
			}
			if mode == "missing-peer-completion" && authCalls != 0 {
				t.Fatal("authorized incomplete receipts")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				t.Fatal(err)
			}
			settled := valid || mode == "lost-status-response"
			if (target.Status.Grant.Phase == "Released") != settled {
				t.Fatal("incorrect release marker")
			}
			if RequiresQuarantine(target, now) == settled {
				t.Fatal("incorrect quarantine after release")
			}
			if settled {
				before := releases
				if err := RecoverGroupMemberRelease(ctx, c, c, target, pod, namespace, authorize, release, now); err != nil {
					t.Fatal("repeat recovery", err)
				}
				if releases != before {
					t.Fatal("repeated lease release")
				}
			}
			if mode != "changed-after-authorization" {
				if err := c.Get(ctx, client.ObjectKeyFromObject(peer), members[1]); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(peer, members[1]) {
					t.Fatal("release changed another member")
				}
			}
		})
	}
}

func TestGroupReleasedQuarantineEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-name", "missing-claim", "missing-completion", "wrong-digest", "future-verification", "unknown-protocol"} {
		t.Run(mode, func(t *testing.T) {
			_, members, now := groupCompletionFixture(t)
			r := members[0]
			r.Status.Grant.Phase = "Released"
			switch mode {
			case "wrong-name":
				r.Name = "other"
			case "missing-claim":
				r.Status.Claim = nil
			case "missing-completion":
				r.Status.Completion = nil
			case "wrong-digest":
				r.Spec.OperationDigest = "other"
			case "future-verification":
				r.Status.Completion.VerifiedAt.Time = now.AddDate(0, 0, 1)
			case "unknown-protocol":
				r.Spec.Protocol = "unknown"
			}
			if RequiresQuarantine(r, now) != (mode != "valid") {
				t.Fatal("incorrect quarantine decision")
			}
		})
	}
}
