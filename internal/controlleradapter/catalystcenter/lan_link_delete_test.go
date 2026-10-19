// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

// Native TLS fixtures plus explicit fake actor grants; no installed execution
// or successful deconfiguration/settlement is asserted.
func TestLANLinkDeleteDispatchUsesSharedOneUsePermit(t *testing.T) {
	for _, mode := range []string{"accepted", "missing-permit", "denied", "lost", "revoked-after-acceptance", "changed-source", "active", "replaced-peer", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := ownedLANDeleteFixture(t, true)
			prepared, err := a.prepareLANLinkDelete(ctx, config, source, want.name)
			if err != nil {
				t.Fatal(err)
			}
			members := []configurationDispatchFixtureMember{}
			for _, name := range []string{"edge0", "edge1"} {
				var d cvk.CiscoDevice
				var n core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: name}, &d); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
					t.Fatal(err)
				}
				members = append(members, configurationDispatchFixtureMember{device: &d, node: &n})
			}
			// The predecessor fixture simulates actor settlement, not lease release.
			// Remove only its fake leases before installing a new fake grant.
			var leases coordination.LeaseList
			if err := a.statusReader.List(ctx, &leases); err != nil {
				t.Fatal(err)
			}
			for i := range leases.Items {
				if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
					t.Fatal(err)
				}
			}
			permit, revoke := configurationGroupDispatchPermitFixture(t, a, members)
			switch mode {
			case "missing-permit":
				permit = nil
			case "denied":
				revoke()
			case "lost":
				f.mode = "lost"
			case "revoked-after-acceptance":
				f.afterPost = revoke
			case "changed-source":
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			case "active":
				f.mode = mode
			case "replaced-peer":
				f.inventory[1].Serial = "FOC99999999"
			case "pruned-group":
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				current.Status.Configuration.Operation.PhysicalGroup = nil
				if err := a.statusWriter.Status().Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			task, err := a.dispatchLANLinkDelete(ctx, config, source, want.name, permit)
			if (err == nil) != (mode == "accepted") {
				t.Fatal("unexpected dispatch outcome", err)
			}
			op := journalState(t, a).Operation
			expectedPosts := 1
			if mode == "accepted" || mode == "lost" || mode == "revoked-after-acceptance" {
				expectedPosts = 2
			}
			if f.posts != expectedPosts {
				t.Fatal("unexpected native submission count", f.posts)
			}
			if mode == "accepted" {
				if op.Phase != "Accepted" || op.TaskID != "lan-task" || task.ID != "lan-task" {
					t.Fatal("native acknowledgement not retained")
				}
			} else if expectedPosts == 2 {
				if op.Phase != "Submitting" || op.TaskID != "" || op.SubmittedAt == nil {
					t.Fatal("ambiguous native acceptance lost its fence")
				}
			} else if op.Phase != "Prepared" || op.SubmittedAt != nil {
				t.Fatal("refused dispatch advanced")
			}
			if op.ID != prepared.ID {
				t.Fatal("retained operation replaced")
			}
			restarted := *a
			if _, err := restarted.dispatchLANLinkDelete(ctx, config, source, want.name, permit); err == nil || f.posts != expectedPosts {
				t.Fatal("restart replayed native update")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if len(reservations.Items) != 2 {
				t.Fatal("endpoint reservation missing")
			}
			for _, member := range reservations.Items {
				if member.Status.Claim == nil || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Grant == nil || member.Status.Grant.Phase == "Released" {
					t.Fatal("dispatch completed/released an endpoint")
				}
			}
		})
	}
}

func TestLANLinkDeletePreparationRequiresCompleteOwnedModel(t *testing.T) {
	for _, mode := range []string{"owned", "retained-predecessor", "missing-runtime", "unknown-link", "unowned-second-link", "mixed-actions"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, f := ownedLANDeleteFixture(t, mode != "retained-predecessor")
			name := want.name
			switch mode {
			case "missing-runtime":
				a.configurationAdmission = nil
			case "unknown-link":
				name = "undeclared"
			case "unowned-second-link", "mixed-actions":
				first := lanLinkFixture()
				first["action"], first["ip_pool_name"] = "DELETE_LINK", want.pool
				first["primary_device_management_ip_address"], first["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
				second := lanLinkFixture()
				second["name"], second["action"], second["ip_pool_name"] = "unowned-second", "DELETE_LINK", want.pool
				if mode == "mixed-actions" {
					second["action"] = "ADD_LINK"
				}
				second["primary_device_management_ip_address"], second["secondary_device_management_ip_address"] = want.primaryIP, want.secondaryIP
				second["primary_device_interface_name"], second["secondary_device_interface_name"] = "GigabitEthernet1/0/10", "GigabitEthernet1/0/11"
				config.Spec.Source.Inline.Raw = lanLinkModel(t, first, second)
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				var err error
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
			}
			op, err := a.prepareLANLinkDelete(ctx, config, source, name)
			if (err == nil) != (mode == "owned") {
				t.Fatal("unexpected preparation", err)
			}
			if mode == "owned" && (op.Phase != "Prepared" || len(op.PhysicalGroup.Targets) != 2) {
				t.Fatal("incomplete admitted group")
			}
			if mode != "owned" && journalState(t, a).Operation != nil {
				t.Fatal("partial model prepared")
			}
			if f.posts != 1 {
				t.Fatal("planning dispatched deletion")
			}
		})
	}
}
