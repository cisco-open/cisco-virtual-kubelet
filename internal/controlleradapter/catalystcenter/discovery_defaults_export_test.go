// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func discoveryDefaultsFixture(t *testing.T, raw []byte, protocol string) ([]byte, DiscoverySelectionProvenance) {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("invalid discovery fixture")
	}
	rows := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)
	for _, row := range rows {
		m := row.(map[string]any)
		delete(m, "type")
		delete(m, "protocol_order")
	}
	source, _ := json.Marshal(root)
	override, _ := json.Marshal(map[string]any{"defaults": map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": map[string]any{"type": "SINGLE", "protocol_order": protocol}}}}})
	out, meta, err := ExportSelectedDiscoveryWithDefaults(source, override, nacmodel.SiteSelectionOptions{})
	if err != nil || meta.AppliedDefaultFields != 2*len(rows) || meta.Exporter != DiscoveryDefaultsExporter {
		t.Fatal("discovery defaults fixture failed", err)
	}
	return out, meta
}

func TestDiscoveryDefaultsPrecedenceAndSelection(t *testing.T) {
	source := discoverySelectionSource(t, discoveryModel())
	var root map[string]any
	json.Unmarshal(source, &root)
	rows := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)
	delete(rows[0].(map[string]any), "protocol_order")
	source, _ = json.Marshal(root)
	overrides := []byte("defaults:\n  catalyst_center:\n    inventory:\n      discovery:\n        type: SINGLE\n        protocol_order: SSH,TELNET\n        retry: 2\n")
	out, meta, err := ExportSelectedDiscoveryWithDefaults(source, overrides, nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := parseInventoryIntent(out)
	if err != nil || len(intent.discoveries) != 1 || intent.discoveries[0].fields["protocolOrder"] != "SSH,TELNET" || intent.discoveries[0].fields["retry"] != 2 {
		t.Fatal("wrong selected resolved intent", err)
	}
	if meta.AppliedDefaultFields != 3 || meta.SelectedJobs != 1 || meta.DeclaredJobs != 2 || meta.DefaultsSHA256 != nacmodel.DefaultsSHA256 || meta.TerraformSHA256 != nacmodel.DiscoveryTerraformSHA256 || meta.OverridesSHA256 != networkExportDigest(overrides) || meta.OutputSHA256 != networkExportDigest(out) || meta.InputSHA256 != networkExportDigest(source) || meta.ConfigurationParity || meta.LeafSchemaValidated {
		t.Fatal("incorrect defaults provenance")
	}
	all, _, err := ExportSelectedDiscoveryWithDefaults(source, overrides, nacmodel.SiteSelectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseInventoryIntent(all)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range parsed.discoveries {
		if job.name == "excluded-global" && job.fields["protocolOrder"] != "SSH" {
			t.Fatal("default replaced explicit value")
		}
	}
}

func TestDiscoveryDefaultsFields(t *testing.T) {
	for field, value := range map[string]any{"type": "CIDR", "protocol_order": "SSH,TELNET", "ip_address_list": "192.0.2.0/24", "cdp_level": 2, "lldp_level": 3, "retry": 0, "time_out": 30, "netconf_port": "830", "preferred_mgmt_ip_method": "UseLoopBack", "ip_filter_list": []any{"192.0.2.2"}} {
		t.Run(field, func(t *testing.T) {
			if err := validateDiscoveryDefault(field, value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDiscoveryDefaultsRefusal(t *testing.T) {
	source := discoverySelectionSource(t, discoveryModel())
	for _, fields := range []string{`{"name":"NO_PRINT"}`, `{"global_credential_list":["NO_PRINT"]}`, `{"create_per_site":true}`, `{"password_list":["NO_PRINT"]}`, `{"type":null}`, `{"retry":-1}`, `{"time_out":0}`, `{"netconf_port":830}`, `{"protocol_order":"NO_PRINT"}`, `{"ip_filter_list":["bad"]}`, `{"unknown":"NO_PRINT"}`} {
		t.Run(fields, func(t *testing.T) {
			overrides := []byte(`{"defaults":{"catalyst_center":{"inventory":{"discovery":` + fields + `}}}}`)
			out, meta, err := ExportSelectedDiscoveryWithDefaults(source, overrides, nacmodel.SiteSelectionOptions{})
			if err == nil || out != nil || meta.Exporter != "" || strings.Contains(err.Error(), "NO_PRINT") {
				t.Fatal("invalid/shadowed discovery default accepted or disclosed")
			}
		})
	}
	for _, overrides := range []string{"defaults: {catalyst_center: {inventory: {discovery: {retry: 1, retry: 2}}}}", "defaults: {catalyst_center: {inventory: {discovery: &x {retry: 1}}}}", "defaults: {catalyst_center: {inventory: {discovery: {retry: .nan}}}}", "defaults: {catalyst_center: {inventory: {discovery: {}}}}\n---\n{}", "defaults: {catalyst_center: {network_settings: {}}}", "defaults: {catalyst_center: {inventory: {discovery: {}, devices: []}}}"} {
		if out, _, err := ExportSelectedDiscoveryWithDefaults(source, []byte(overrides), nacmodel.SiteSelectionOptions{}); err == nil || out != nil {
			t.Fatal("ambiguous/out-of-scope defaults accepted")
		}
	}
	for _, bad := range []string{strings.Replace(string(source), `"protocol_order":"SSH"`, `"protocol_order":null`, 1), strings.Replace(string(source), `"excluded-global"`, `"excluded-global","password_list":["NO_PRINT"]`, 1)} {
		if out, _, err := ExportSelectedDiscoveryWithDefaults([]byte(bad), []byte(`{"defaults":{"catalyst_center":{"inventory":{"discovery":{"protocol_order":"SSH"}}}}}`), nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}); err == nil || out != nil {
			t.Fatal("null/invalid excluded input was repaired or discarded")
		}
	}
}

func TestDiscoveryDefaultsPinnedEmpty(t *testing.T) {
	fields, err := nacmodel.PinnedDiscoveryDefaults()
	if err != nil || len(fields) != 0 {
		t.Fatal("pinned module gained invented discovery defaults", err)
	}
	old, _, err := ExportSelectedDiscovery(discoveryModel(), nacmodel.SiteSelectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out, meta, err := ExportSelectedDiscoveryWithDefaults(discoveryModel(), nil, nacmodel.SiteSelectionOptions{})
	if err != nil || !bytes.Equal(old, out) || meta.AppliedDefaultFields != 0 {
		t.Fatal("empty pinned defaults changed resolved model", err)
	}
}

func TestDiscoveryDefaultsManifestLifecycle(t *testing.T) {
	discoveryManifestScenario(t, discoveryCycleScenario{replacement: true, defaults: true})
}

func TestDiscoveryDefaultsExpansionBudget(t *testing.T) {
	rows := []any{}
	for i := 0; i < 100; i++ {
		rows = append(rows, map[string]any{"name": fmt.Sprint("job", i), "type": "SINGLE", "protocol_order": "SSH", "ip_address_list": "192.0.2.1", "global_credential_list": []any{"reference"}})
	}
	filters := []any{}
	for i := 0; i < 128; i++ {
		addresses := []string{}
		for j := 0; j < 128; j++ {
			n := i*128 + j
			addresses = append(addresses, fmt.Sprintf("10.%d.%d.%d", n>>16, (n>>8)&255, n&255))
		}
		filters = append(filters, strings.Join(addresses, ","))
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": rows}}})
	defaults, _ := json.Marshal(map[string]any{"defaults": map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": map[string]any{"ip_filter_list": filters}}}}})
	if out, _, err := ExportSelectedDiscoveryWithDefaults(raw, defaults, nacmodel.SiteSelectionOptions{}); err == nil || out != nil {
		t.Fatal("repeated defaults exceeded output budget")
	}
	rows = append(rows, rows[0])
	raw, _ = json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": rows}}})
	if out, _, err := ExportSelectedDiscoveryWithDefaults(raw, nil, nacmodel.SiteSelectionOptions{}); err == nil || out != nil {
		t.Fatal("excessive job count accepted")
	}
}
