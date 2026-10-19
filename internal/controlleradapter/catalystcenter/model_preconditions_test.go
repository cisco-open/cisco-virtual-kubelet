// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPinnedModelPreconditionsBeforeNativeExecution(t *testing.T) {
	for _, mode := range []string{"location-conflict", "rf-role", "channel-mode", "power-mode", "width-24", "unknown-profile", "rename-no-serial", "wireless-role", "wireless-locations", "anchor-on-fabric", "wireless-type"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			profile := map[string]any{"name": "office"}
			ap := map[string]any{"name": "AP1", "type": "AccessPoint", "state": "ASSIGN", "site": "Global/Lab", "access_point": map[string]any{"configuration": "office"}}
			wlc := map[string]any{"name": "WLC", "type": "WirelessController", "state": "ASSIGN", "site": "Global/Lab", "primary_managed_ap_locations": []any{"Global/Lab"}}
			devices := []any{ap}
			radio := map[string]any{"band": 5, "role": "SERVING", "channel_assignment_mode": "CUSTOM", "channel_number": 36}
			switch mode {
			case "location-conflict":
				profile["location"] = "room"
				profile["is_assigned_site_as_location"] = true
			case "rf-role":
				radio["role"] = "AUTO"
				profile["radios"] = []any{radio}
			case "channel-mode":
				radio["channel_assignment_mode"] = "GLOBAL"
				profile["radios"] = []any{radio}
			case "power-mode":
				profile["radios"] = []any{map[string]any{"band": 5, "role": "SERVING", "power_level": 3}}
			case "width-24":
				radio["band"] = 2.4
				radio["channel_width"] = 20
				profile["radios"] = []any{radio}
			case "unknown-profile":
				ap["access_point"] = map[string]any{"configuration": "absent"}
			case "rename-no-serial":
				ap["access_point"] = map[string]any{"manage_hostname": true}
			case "wireless-role":
				wlc["fabric_roles"] = []any{"CONTROL_PLANE_NODE"}
				devices = []any{wlc}
			case "wireless-locations":
				delete(wlc, "primary_managed_ap_locations")
				devices = []any{wlc}
			case "anchor-on-fabric":
				wlc["fabric_roles"] = []any{"WIRELESS_CONTROLLER_NODE"}
				wlc["anchor_managed_ap_locations"] = []any{"Global/Lab"}
				devices = []any{wlc}
			case "wireless-type":
				delete(wlc, "type")
				devices = []any{wlc}
			}
			root := map[string]any{"inventory": map[string]any{"devices": devices}}
			section := "inventory"
			switch mode {
			case "location-conflict", "rf-role", "channel-mode", "power-mode", "width-24":
				root = map[string]any{"wireless": map[string]any{"access_point_configurations": []any{profile}}}
				section = "wireless"
			}
			raw, err := json.Marshal(map[string]any{"catalyst_center": root})
			if err != nil {
				t.Fatal(err)
			}
			if validatePinnedModelPreconditions(raw) == nil {
				t.Fatal("pinned constraint accepted")
			}
			config.Spec.ManagedSections = []string{section}
			config.Spec.Source.Inline.Raw = raw
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			reads, writes := 0, 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					reads++
				} else {
					writes++
				}
				t.Error("precondition failure contacted native transport")
			})
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			status := &configapi.NetworkControllerConfigStatus{}
			_, handled, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
			if !handled || reason != "ModelPreconditionFailed" || status.Phase != configapi.NetworkControllerConfigPhaseFailed || reads != 0 || writes != 0 {
				t.Fatal("public preflight did not refuse before API", reason, reads, writes)
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal("public reconcile", err)
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			if published.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || reads != 0 || writes != 0 {
				t.Fatal("public reconcile did not preserve precondition refusal")
			}
			fresh, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Status.Configuration != nil && fresh.Status.Configuration.Operation != nil {
				t.Fatal("invalid model prepared a journal")
			}
		})
	}
}

func TestPinnedPreconditionsPreserveValidModels(t *testing.T) {
	for _, raw := range []string{
		`{"catalyst_center":{"inventory":{"devices":[{"name":"edge","state":"PROVISION","site":"Global/Lab"}]}}}`,
		`{"catalyst_center":{"inventory":{"devices":[{"name":"wlc","type":"WirelessController","state":"ASSIGN","primary_managed_ap_locations":[]}]}}}`,
		`{"catalyst_center":{"inventory":{"devices":[{"name":"ewlc","fabric_roles":["EMBEDDED_WIRELESS_CONTROLLER_NODE"],"primary_managed_ap_locations":["Global/Lab"]}]}}}`,
		`{"catalyst_center":{"inventory":{"devices":[{"name":"anchor","type":"WirelessController","anchor_managed_ap_locations":["Global/Lab"]}]}}}`,
		`{"catalyst_center":{"wireless":{"access_point_configurations":[{"name":"office","radios":[{"band":2.4,"role":"SERVING","channel_assignment_mode":"CUSTOM","channel_number":1,"power_assignment_mode":"CUSTOM","power_level":3},{"band":5,"role":"SERVING","channel_assignment_mode":"CUSTOM","channel_width":80}]}]},"inventory":{"devices":[{"name":"AP1","type":"AccessPoint","state":"ASSIGN","serial_number":"serial","access_point":{"configuration":"office","manage_hostname":true}}]}}}`,
	} {
		if err := validatePinnedModelPreconditions([]byte(raw)); err != nil {
			t.Fatal("valid model constraint refused", err)
		}
	}
}
