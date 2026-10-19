// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"testing"
)

func TestRMAUnmarkPublicCompiler(t *testing.T) {
	for _, mode := range []string{"cleanup", "retain", "no-history-delete", "ordinary", "foreign", "missing-device", "extra-device", "role", "no-serial", "reprovision", "no-admission", "report", "delete-cr"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			var root map[string]any
			if err := json.Unmarshal([]byte(strings.Replace(rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`), "FOC12345678", "OLD-SERIAL", 1)), &root); err != nil {
				t.Fatal(err)
			}
			inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
			device := inventory["devices"].([]any)[0].(map[string]any)
			delete(device, "rma")
			switch mode {
			case "retain", "ordinary":
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
			case "extra-device":
				inventory["devices"] = append(inventory["devices"].([]any), map[string]any{"name": "other", "serial_number": "OTHER", "state": "PROVISION", "site": "Global/Lab/Building"})
			case "role":
				device["device_role"] = "ACCESS"
				device["role_source"] = "MANUAL"
			case "no-serial":
				delete(device, "serial_number")
			case "reprovision":
				device["state"] = "REPROVISION"
			case "report":
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "delete-cr":
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw = raw
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Operation: cvk.NetworkControllerConfigurationOperation{Resource: "rma-mark:" + strings.Repeat("a", 64), ConfigName: config.Name, ConfigUID: string(config.UID), TargetSerial: "old-serial"}}}}
			switch mode {
			case "ordinary", "no-history-delete":
				nc.Status.Configuration = nil
			case "foreign":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "missing-device":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Operation: cvk.NetworkControllerConfigurationOperation{Resource: "rma-mark:" + strings.Repeat("b", 64), ConfigName: config.Name, ConfigUID: string(config.UID), TargetSerial: "missing"}})
			}
			if mode != "no-admission" {
				a.workerPodName, a.workerPodUID = "connector", "connector-uid"
				if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := a.compileOwnedInventoryApply(nc, config, source)
			valid := mode == "cleanup" || mode == "ordinary"
			if mode == "delete-cr" && supportedConfigurationApply(config) {
				t.Fatal("CR deletion enabled")
			}
			if (err == nil) != valid {
				t.Fatal("incorrect public cleanup selection", err)
			}
			if valid {
				_, cleanup := plan.executor.(*rmaInventoryStages)
				if cleanup != (mode == "cleanup") {
					t.Fatal("ordinary provisioning route replaced")
				}
			}
		})
	}
}
