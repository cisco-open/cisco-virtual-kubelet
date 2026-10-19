// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricDevicesBulkDispatchUsesSharedPermit(t *testing.T) {
	for _, mode := range []string{"accepted", "partial", "lost", "denied", "missing-permit", "changed-source", "replaced-peer", "revoked-after-acceptance"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, wants, f := fabricDevicesBulkPreparationFixture(t, "prepare")
			if _, err := a.prepareFabricDevicesBulk(ctx, config, source, wants); err != nil {
				t.Fatal(err)
			}
			members := []configurationDispatchFixtureMember{}
			for _, name := range []string{"edge0", "edge1", "peer"} {
				var device cvk.CiscoDevice
				var node core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: name}, &device); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &node); err != nil {
					t.Fatal(err)
				}
				members = append(members, configurationDispatchFixtureMember{device: &device, node: &node})
			}
			if err := ops.AddToScheme(a.statusWriter.Scheme()); err != nil {
				t.Fatal(err)
			}
			permit, revoke := configurationGroupDispatchPermitFixture(t, a, members)
			if mode == "revoked-after-acceptance" {
				f.afterMutation = revoke
			}
			if mode == "denied" {
				revoke()
			}
			if mode == "missing-permit" {
				permit = nil
			}
			if mode == "changed-source" {
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "replaced-peer" {
				f.inventory[2].Serial = "FOC99999999"
			}
			// Native response knobs use the same fixture callback as native tests.
			f.partial = mode == "partial"
			f.lost = mode == "lost"
			task, err := a.dispatchFabricDevicesBulk(ctx, config, source, wants, permit)
			accepted := mode == "accepted" || mode == "partial"
			if (err == nil) != accepted {
				t.Fatal("shared dispatch outcome", err)
			}
			op := journalState(t, a).Operation
			expected := 0
			if accepted || mode == "lost" || mode == "revoked-after-acceptance" {
				expected = 1
			}
			if f.posts != expected {
				t.Fatal("native request count", f.posts)
			}
			if accepted {
				if op.Phase != "Accepted" || op.TaskID != "task" || task.ID != "task" {
					t.Fatal("acknowledgement not recorded")
				}
			} else if mode == "lost" || mode == "revoked-after-acceptance" {
				if op.Phase != "Submitting" || op.TaskID != "" || op.SubmittedAt == nil {
					t.Fatal("uncertain acceptance fence lost")
				}
			} else if op.Phase != "Prepared" || op.SubmittedAt != nil {
				t.Fatal("refused dispatch advanced")
			}
			if mode == "revoked-after-acceptance" && task.ID != "task" {
				t.Fatal("known task lost on receipt refusal")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if len(reservations.Items) != 3 {
				t.Fatal("peer omitted from retained group")
			}
			for _, member := range reservations.Items {
				if member.Status.Claim == nil || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Grant == nil || member.Status.Grant.Phase == "Released" {
					t.Fatal("dispatch released/completed physical work")
				}
			}
			restarted := *a
			if _, err := restarted.dispatchFabricDevicesBulk(ctx, config, source, wants, permit); err == nil || f.posts != expected {
				t.Fatal("restart replayed native request")
			}
		})
	}
}
