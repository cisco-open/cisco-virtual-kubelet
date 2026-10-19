// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricDevicesBulkPublicAcknowledgedRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "declared-peer", "running", "failed", "partial", "revoked", "changed-source", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, wants, f := fabricDevicesBulkPreparationFixture(t, "prepare")
			if mode == "declared-peer" {
				var root map[string]any
				if json.Unmarshal(source.raw, &root) != nil {
					t.Fatal("fixture source")
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				inventory["devices"] = append(inventory["devices"].([]any), map[string]any{"name": "peer", "serial_number": "FOC12345680", "site": "Global/Lab/Building", "state": "PROVISION", "fabric_site": "Global/Lab", "fabric_roles": []string{"EDGE_NODE"}})
				raw, _ := json.Marshal(root)
				config.Spec.Source.Inline.Raw = raw
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				source = &configSourceSnapshot{raw: raw}
			}
			prepared, err := a.prepareFabricDevicesBulk(ctx, config, source, wants)
			if err != nil {
				t.Fatal(err)
			}
			members := []configurationDispatchFixtureMember{}
			for _, name := range []string{"edge0", "edge1", "peer"} {
				var d cvk.CiscoDevice
				var n core.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: "lab", Name: name}, &d); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
					t.Fatal(err)
				}
				members = append(members, configurationDispatchFixtureMember{device: &d, node: &n})
			}
			permit, revoke := configurationGroupDispatchPermitFixture(t, a, members)
			f.partial = mode == "partial"
			if _, err := a.dispatchFabricDevicesBulk(ctx, config, source, wants, permit); err != nil {
				t.Fatal(err)
			}
			// Persisted worker/session/lease installation is simulated in this test.
			a.configurationAdmission = nil
			installFabricGroupAuthorityFixture(t, a)
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				current.Status.Configuration.Operation.Phase = "Unknown"
			}
			if mode == "pruned-group" {
				current.Status.Configuration.Operation.PhysicalGroup = nil
			}
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			if mode == "running" || mode == "failed" {
				f.taskMode = mode
			}
			if mode == "revoked" {
				revoke()
			}
			if mode == "changed-source" {
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			key := ctrlclient.ObjectKeyFromObject(config)
			for range 3 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
			}
			state := journalState(t, a)
			success := mode == "accepted" || mode == "unknown" || mode == "declared-peer"
			expected := "Accepted"
			if mode == "unknown" {
				expected = "Unknown"
			}
			if success {
				expected = "Verifying"
			}
			if state.Operation == nil || state.Operation.ID != prepared.ID || state.Operation.Phase != expected || state.Operation.TaskID != "task" || len(state.ActionApplications) != 0 || f.posts != 1 {
				t.Fatal("recovery lost fence or replayed", mode)
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, key, &published); err != nil {
				t.Fatal(err)
			}
			if success {
				found := false
				for _, condition := range published.Status.Conditions {
					if condition.Reason == "GroupDeviceEvidencePending" {
						found = true
					}
				}
				if !found || published.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("missing convergence gap status")
				}
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if len(reservations.Items) != 3 {
				t.Fatal("peer claim lost")
			}
			for _, member := range reservations.Items {
				if member.Status.Claim == nil || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Grant == nil || member.Status.Grant.Phase == "Released" {
					t.Fatal("recovery released or completed a member")
				}
			}
		})
	}
}
