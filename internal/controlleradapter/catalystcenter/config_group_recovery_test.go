// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConfigurationGroupRecoveryRetainsJournal(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Verifying", "Verified", "Unknown", "Cancelling"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			if _, err := a.prepareConfiguration(ctx, config, nil, "sites", "fabric-group", "Update"); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = phase
			// Even incomplete evidence must never fall through to the logical-resource
			// path, clear a terminal journal, or dispatch a task. No HTTP client exists.
			nc.Status.Configuration.Operation.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{Version: "controller-configuration-group-identity-v1"}
			if phase == "Cancelling" {
				// A partially pruned group must remain fenced too.
				nc.Status.Configuration.Operation.PhysicalGroup = nil
				at := metav1.Now()
				nc.Status.Configuration.Operation.CancellationRequestedAt = &at
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			before := journalState(t, a).Operation.DeepCopy()
			restarted := *a
			status := &configapi.NetworkControllerConfigStatus{}
			_, pending, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, status)
			if !pending || reason != "GroupReservationRecoveryRequired" || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
				t.Fatal("unsafe recovery", pending, reason, status.Phase)
			}
			// Legacy journal helpers must also refuse bypasses of the reconciler.
			for _, err := range []error{
				restarted.startConfiguration(ctx, config, nil, before.ID),
				restarted.advanceConfiguration(ctx, before.ID, "Unknown", ""),
				restarted.finishConfiguration(ctx, before.ID),
			} {
				if !errors.Is(err, errConfigurationDeviceAdmission) {
					t.Fatal("legacy group bypass", err)
				}
			}
			if !reflect.DeepEqual(before, journalState(t, a).Operation) {
				t.Fatal("group journal changed")
			}
		})
	}
}
