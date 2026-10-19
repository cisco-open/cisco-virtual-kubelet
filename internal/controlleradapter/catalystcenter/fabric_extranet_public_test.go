// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"testing"
)

func TestExtranetPublicWholeModelPreflight(t *testing.T) {
	for _, mode := range []string{"valid", "no-group-runtime", "later-unready", "later-unowned", "unowned-update", "unready-metadata", "unknown-section"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nativeMode := "valid"
			if mode == "later-unowned" || mode == "unowned-update" {
				nativeMode = "expansion"
			}
			a, config, _, writes, _ := extranetManagedFixture(t, nativeMode)
			var root map[string]any
			if err := json.Unmarshal([]byte(extranetModel), &root); err != nil {
				t.Fatal(err)
			}
			cc := root["catalyst_center"].(map[string]any)
			fabric := cc["fabric"].(map[string]any)
			switch mode {
			case "later-unready":
				fabric["extranet_policies"] = append(fabric["extranet_policies"].([]any), map[string]any{"name": "ZZ", "provider_virtual_network": "Services", "subscriber_virtual_networks": []string{"Corp"}, "fabric_sites": []string{"Global/Missing"}})
			case "later-unowned":
				fabric["extranet_policies"] = append([]any{map[string]any{"name": "First", "provider_virtual_network": "Services", "subscriber_virtual_networks": []string{"Corp", "Guest"}, "fabric_sites": []string{"Global/Lab"}}}, fabric["extranet_policies"].([]any)...)
			case "unready-metadata":
				fabric["l3_virtual_networks"] = []any{map[string]any{"name": "NotReady"}}
			case "unknown-section":
				cc["wireless"] = map[string]any{}
			}
			config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if mode != "no-group-runtime" {
				a.workerPodName, a.workerPodUID = "connector", "connector-uid"
				if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
					t.Fatal("preflight requested mutation authority")
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var status configapi.NetworkControllerConfigStatus
			_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, &status)
			state := journalState(t, a)
			if mode == "valid" {
				if reason != "GroupPreparationPending" || state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" {
					t.Fatal("valid public preparation failed", reason, state)
				}
			} else if state != nil && state.Operation != nil {
				t.Fatal("later invalid intent partially prepared", reason, state)
			}
			if writes.Load() != 0 {
				t.Fatal("preflight mutated appliance")
			}
		})
	}
}

func TestExtranetPublicRetainedNativeIdentity(t *testing.T) {
	for _, mode := range []string{"matching", "unowned-matching", "missing", "replaced", "invalid-receipt"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nativeMode := "existing"
			if mode == "missing" {
				nativeMode = "valid"
			}
			a, config, want, writes, _ := extranetManagedFixture(t, nativeMode)
			config.Spec.Source.Inline.Raw = []byte(extranetModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if mode != "unowned-matching" {
				retainExtranetCreationFixture(t, a, config, want)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replaced" {
				prefix, _ := extranetReceiptPrefix(want.policy.Name)
				nc.Status.Configuration.ActionApplications[0].Resource = prefix + "original"
			}
			if mode == "invalid-receipt" {
				nc.Status.Configuration.ActionApplications[0].Operation.TaskID = ""
			}
			if mode == "replaced" || mode == "invalid-receipt" {
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
				t.Fatal("no-op or stale receipt requested authority")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			var status configapi.NetworkControllerConfigStatus
			_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, &status)
			allowed := mode == "matching" || mode == "unowned-matching"
			if (status.Phase == configapi.NetworkControllerConfigPhaseInSync) != allowed {
				t.Fatal("retained native identity was not honored", reason, status.Phase)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil || writes.Load() != 0 {
				t.Fatal("missing/replaced policy was recreated or mutated")
			}
			if mode != "unowned-matching" && len(state.ActionApplications) != 1 {
				t.Fatal("retained ownership was discarded")
			}
		})
	}
}
