// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGroupSubmissionProductBinding(t *testing.T) {
	for _, mode := range []string{"valid", "controller", "namespace", "operation", "group", "missing"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			permit, err := ClaimGroupForController(ctx, c, c, nc, members, "leases", func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, now)
			if err != nil {
				t.Fatal(err)
			}
			expected := nc.DeepCopy()
			switch mode {
			case "controller":
				expected.UID = "foreign"
			case "namespace":
				expected.Namespace = "foreign"
			case "operation":
				expected.Status.Configuration.Operation.ID = "foreign"
			case "group":
				expected.Status.Configuration.Operation.PhysicalGroup = nil
			case "missing":
				expected.Status.Configuration = nil
			}
			dispatch, err := permit.BeginSubmissionFor(ctx, expected, now)
			if (err == nil) != (mode == "valid") || (dispatch != nil) != (mode == "valid") {
				t.Fatal("binding result", err)
			}
			var stored cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &stored); err != nil {
				t.Fatal(err)
			}
			phase := "Prepared"
			if mode == "valid" {
				phase = "Submitting"
			}
			if stored.Status.Configuration.Operation.Phase != phase {
				t.Fatal("incorrect journal transition")
			}
		})
	}
}
