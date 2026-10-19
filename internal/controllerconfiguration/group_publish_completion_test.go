// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPublishGroupCompletions(t *testing.T) {
	for _, mode := range []string{"valid", "lost-first-response", "conflict-second", "existing", "changed-existing", "denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc, reservations, now := groupCompletionFixture(t)
			if mode != "existing" && mode != "changed-existing" {
				for _, r := range reservations {
					r.Status.Completion = nil
				}
			}
			if mode == "changed-existing" {
				reservations[1].Status.Completion.TaskID = "different"
			}
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			objects := []client.Object{nc}
			for _, r := range reservations {
				objects = append(objects, r)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, &ops.ControllerConfigurationReservation{}).WithObjects(objects...).Build()
			refresh := func() {
				t.Helper()
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
				for _, r := range reservations {
					if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
						t.Fatal(err)
					}
				}
			}
			refresh()
			writer := &groupClaimTestWriter{Client: c, mode: mode, controller: nc}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			err := PublishGroupCompletions(ctx, c, writer, nc, reservations, authorize, now)
			success := mode == "valid" || mode == "existing"
			if (err == nil) != success {
				t.Fatal(mode, err)
			}
			if (mode == "existing" || mode == "changed-existing" || mode == "denied") && writer.calls != 0 {
				t.Fatal("unexpected publication", writer.calls)
			}
			refresh()
			if mode == "lost-first-response" || mode == "conflict-second" {
				if reservations[0].Status.Completion == nil || reservations[1].Status.Completion != nil {
					t.Fatal("partial completion evidence lost")
				}
				writer.mode = "valid"
				if err := PublishGroupCompletions(ctx, c, writer, nc, reservations, authorize, now); err != nil {
					t.Fatal("partial publication did not recover", err)
				}
				refresh()
				if err := ValidateGroupCompletionReceipts(nc, reservations, now); err != nil {
					t.Fatal(err)
				}
			}
			if success {
				calls := writer.calls
				if err := PublishGroupCompletions(ctx, c, writer, nc, reservations, authorize, now); err != nil || writer.calls != calls {
					t.Fatal("completion replayed", err)
				}
			}
		})
	}
}
