//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"testing"
)

func TestEnvtestNetworkDefaultExport(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "unknown", "replace", "target-replaced", "preflight-history"} {
		t.Run(mode, func(t *testing.T) {
			install := installWirelessFeatureQualificationAPI(t, api, "defaults-"+mode)
			exerciseNetworkApplication(t, "telemetry", mode, func(a *adapter, config *configapi.NetworkControllerConfig) {
				var model map[string]any
				if json.Unmarshal(config.Spec.Source.Inline.Raw, &model) != nil {
					t.Fatal("invalid fixture")
				}
				profile := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["telemetry"].([]any)[0].(map[string]any)
				for _, field := range []string{"wireless_telemetry", "catalyst_center_as_snmp_server", "catalyst_center_as_syslog_server", "catalyst_center_as_network_collector", "enable_netflow_collector_on_devices"} {
					delete(profile, field)
				}
				raw, err := json.Marshal(model)
				if err != nil {
					t.Fatal(err)
				}
				exported, meta, err := nacmodel.ExportNetworkDefaults(raw, nil)
				if err != nil || meta.ConfigurationParity || meta.LeafSchemaValidated || meta.Exporter != nacmodel.NetworkDefaultsExporter || ValidateNetworkSettingsSource(exported) != nil {
					t.Fatal("invalid pinned export", err)
				}
				config.Spec.Source.Inline.Raw = exported
				config.Spec.ModelSource.Exporter = meta.Exporter
				if config.Spec.ModelSource.SchemaDigest != "" {
					t.Fatal("defaults must not invent schema attestation")
				}
				install(a, config)
				if config.Spec.ModelSource.Exporter != nacmodel.NetworkDefaultsExporter {
					t.Fatal("API changed exporter policy binding")
				}
			})
		})
	}
}
