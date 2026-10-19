// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"net/http"
	"testing"
)

func TestMulticastVNPublicPreflightAllIntent(t *testing.T) {
	for _, mode := range []string{"later-unready", "replication-unavailable", "wireless"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _ := multicastVNJournalFixture(t, "create", func(w http.ResponseWriter, r *http.Request) bool {
				if mode == "replication-unavailable" && r.URL.Path == fabricMulticastPath {
					w.WriteHeader(404)
					return true
				}
				return false
			})
			var root map[string]any
			if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &root); err != nil {
				t.Fatal(err)
			}
			site := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)["fabric_sites"].([]any)[0].(map[string]any)
			multicast := site["multicast"].(map[string]any)
			switch mode {
			case "replication-unavailable":
				multicast["replication_mode"] = "HEADEND_REPLICATION"
			case "wireless":
				multicast["wireless_multicast_enabled"] = true
			default:
				rows := multicast["virtual_networks"].([]any)
				later := map[string]any{}
				for k, v := range rows[0].(map[string]any) {
					later[k] = v
				}
				later["name"] = "Missing"
				multicast["virtual_networks"] = append(rows, later)
			}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw = raw
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			a.configurationAdmission = nil
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
				t.Error("invalid model requested authority")
				return errConfigurationDeviceAdmission
			}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var status configapi.NetworkControllerConfigStatus
			_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, &status)
			if reason != "GroupPreparationPending" {
				t.Fatal("unexpected preparation result", reason)
			}
			if state := journalState(t, a); state != nil && state.Operation != nil {
				t.Fatal("invalid later intent partially prepared")
			}
		})
	}
}
