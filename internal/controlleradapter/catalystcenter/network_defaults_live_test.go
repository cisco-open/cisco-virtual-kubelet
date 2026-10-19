//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"os"
	"testing"
)

func TestLiveCatalystCenterNetworkDefaultExport(t *testing.T) {
	if os.Getenv("CATC_NETWORK_DEFAULT_EXPORT") != "isolated-pinned" || os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") != "isolated-public-preflight" || os.Getenv("CATC_NETWORK_TASK_RECOVERY") != "isolated-original-tasks" || os.Getenv("CATC_NETWORK_APPLICATION_QUALIFICATION") != "isolated-latest-applications" {
		t.Skip("isolated pinned exporter qualification disabled")
	}
	qualifyLiveSiteExecution(t, true)
}

func liveExportNetworkDefaultModel(t *testing.T, raw []byte) []byte {
	t.Helper()
	var model map[string]any
	if json.Unmarshal(raw, &model) != nil {
		t.Fatal("invalid input fixture")
	}
	profile := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["telemetry"].([]any)[0].(map[string]any)
	for _, key := range []string{"wired_data_collection", "wireless_telemetry", "catalyst_center_as_snmp_server", "catalyst_center_as_syslog_server", "catalyst_center_as_network_collector", "enable_netflow_collector_on_devices"} {
		delete(profile, key)
	}
	profile["wired_data_collection"] = false
	input, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	exported, meta, err := nacmodel.ExportNetworkDefaults(input, nil)
	if err != nil || meta.DefaultsSHA256 != nacmodel.DefaultsSHA256 || meta.ConfigurationParity || meta.Exporter != nacmodel.NetworkDefaultsExporter {
		t.Fatal("pinned export provenance unavailable", err)
	}
	if ValidateNetworkSettingsSource(exported) != nil {
		t.Fatal("exported fixture failed actual offline compiler")
	}
	if json.Unmarshal(exported, &model) != nil {
		t.Fatal("invalid exported fixture")
	}
	actual := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["telemetry"].([]any)[0].(map[string]any)
	for key, value := range map[string]bool{"wired_data_collection": false, "wireless_telemetry": false, "catalyst_center_as_snmp_server": true, "catalyst_center_as_syslog_server": true, "catalyst_center_as_network_collector": true, "enable_netflow_collector_on_devices": false} {
		if actual[key] != value {
			t.Fatal("pinned default or explicit false lost")
		}
	}
	t.Log("offline exporter materialized pinned telemetry defaults, preserved explicit false and passed the existing network compiler; no schema/parity inference")
	return exported
}
func liveNetworkDefaultExporterIdentity() string { return nacmodel.NetworkDefaultsExporter }
