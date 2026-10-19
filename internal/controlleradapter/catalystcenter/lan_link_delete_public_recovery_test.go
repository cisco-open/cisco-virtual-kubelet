// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func TestLANLinkDeletePublicAcknowledgedRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "running", "failed", "future-task", "changed-source", "changed-pool", "replaced-port", "replaced-peer", "revoked", "lost", "pruned-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _, f, members := dispatchedLANLinkDeleteFixture(t, mode == "lost")
			for i := range f.ports {
				f.ports[i].IPv4, f.ports[i].Mask = "", ""
			}
			a.configurationAdmission = nil
			installFabricGroupAuthorityFixture(t, a)
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			original := current.Status.Configuration.Operation.DeepCopy()
			switch mode {
			case "unknown":
				current.Status.Configuration.Operation.Phase = "Unknown"
			case "pruned-group":
				current.Status.Configuration.Operation.PhysicalGroup = nil
			case "changed-source":
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			case "running", "failed":
				f.taskMode = mode
			case "future-task":
				f.taskEnd = time.Now().Add(time.Minute)
			case "changed-pool":
				f.poolChanged = true
			case "replaced-port":
				f.ports[1].ID = "replacement-port"
			case "replaced-peer":
				f.inventory[1].Serial = "FOC99999999"
			case "revoked":
				member := members[0]
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
					t.Fatal(err)
				}
				member.Status.Grant.Phase = "Revoked"
				if err := a.statusWriter.Status().Update(ctx, member); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
			}
			state := journalState(t, a)
			success := mode == "accepted" || mode == "unknown" || mode == "changed-pool"
			expected := original.Phase
			if success {
				expected = "Verifying"
			}
			if state.Operation == nil || state.Operation.ID != original.ID || state.Operation.Phase != expected || f.posts != 2 || len(state.ActionApplications) != 1 {
				t.Fatal("recovery replayed, replaced or prematurely completed work", mode)
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
				t.Fatal("retained recovery was relabelled unsupported/ready", published.Status)
			}
			if success {
				found := false
				for _, condition := range published.Status.Conditions {
					found = found || condition.Reason == "GroupDeviceVerificationPending"
				}
				if !found {
					t.Fatal("independent proof gap was not reported")
				}
			}
			assertLANClaimsRetained(t, a, false)
		})
	}
}
