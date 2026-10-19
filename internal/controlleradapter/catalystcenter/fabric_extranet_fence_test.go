// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func TestExtranetPublicRetainedGroupFence(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Unknown", "Verifying", "Verified", "Cancelling"} {
		for _, pruned := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/complete", true: "/pruned"}[pruned], func(t *testing.T) {
				ctx := context.Background()
				a, config, _, writes, _ := extranetManagedFixture(t, "valid")
				config.Spec.Source.Inline.Raw = []byte(extranetModel)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				op, err := a.prepareExtranetPolicy(ctx, config, source, "Shared")
				if err != nil || op == nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation.Phase = phase
				if phase != "Prepared" {
					nc.Status.Configuration.Operation.SubmittedAt = &metav1.Time{Time: metav1.Now().Time}
				}
				if phase == "Accepted" || phase == "Verifying" || phase == "Verified" {
					nc.Status.Configuration.Operation.TaskID = "task"
				}
				if phase == "Cancelling" {
					nc.Status.Configuration.Operation.CancellationRequestedAt = &metav1.Time{Time: metav1.Now().Time}
				}
				if pruned {
					nc.Status.Configuration.Operation.PhysicalGroup = nil
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				before := nc.Status.Configuration.Operation.DeepCopy()
				restarted := *a
				status := &configapi.NetworkControllerConfigStatus{}
				_, pending, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, status)
				if !pending || reason != "GroupReservationRecoveryRequired" || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("retained extranet escaped group fence", pending, reason, status.Phase)
				}
				for _, err := range []error{a.startConfiguration(ctx, config, source, before.ID), a.advanceConfiguration(ctx, before.ID, "Unknown", ""), a.finishConfiguration(ctx, before.ID)} {
					if !errors.Is(err, errConfigurationDeviceAdmission) {
						t.Fatal("legacy bypass", err)
					}
				}
				if writes.Load() != 0 || !reflect.DeepEqual(before, journalState(t, a).Operation) {
					t.Fatal("unimplemented public recovery wrote or discarded evidence")
				}
			})
		}
	}
}
