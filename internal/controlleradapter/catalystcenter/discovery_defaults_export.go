// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed discovery_defaults_export.go
var discoveryDefaultExportPolicy []byte
var discoveryDefaultExportSHA = networkExportDigest(append([]byte(DiscoverySiteSelectionExporter+"\x00"+nacmodel.DiscoveryDefaultsPolicySHA256+"\x00"), discoveryDefaultExportPolicy...))
var DiscoveryDefaultsExporter = "cvk-discovery-defaults-v1:policy-sha256:" + discoveryDefaultExportSHA

func validateDiscoveryDefault(field string, value any) error {
	probe := map[string]any{"name": "CVK_DEFAULT_VALIDATION", "type": "SINGLE", "protocol_order": "SSH", "ip_address_list": "192.0.2.1", "global_credential_list": []any{"reference"}}
	switch field {
	case "type":
		if text, ok := value.(string); ok && discoveryType(text) == "CIDR" {
			probe["ip_address_list"] = "192.0.2.0/24"
		}
	case "ip_address_list":
		if v, ok := value.(string); ok {
			if strings.Contains(v, "/") {
				probe["type"] = "CIDR"
			} else if strings.ContainsAny(v, ",-") {
				probe["type"] = "MULTI RANGE"
			}
		}
	case "cdp_level":
		probe["type"] = "CDP"
	case "lldp_level":
		probe["type"] = "LLDP"
	case "protocol_order", "retry", "time_out", "netconf_port", "preferred_mgmt_ip_method", "ip_filter_list":
	default:
		return errors.New("unsupported discovery default field")
	}
	probe[field] = value
	_, err := parseDiscoveryModels([]any{probe})
	return err
}

// Offline defaults compose with ordinary selection and the runtime compiler.
// Neither credentials, job identity nor create_per_site have a defaults fallback
// in the pinned Terraform expression and cannot be injected by this exporter.
func ExportSelectedDiscoveryWithDefaults(raw, overrides []byte, options nacmodel.SiteSelectionOptions) ([]byte, DiscoverySelectionProvenance, error) {
	fail := func() ([]byte, DiscoverySelectionProvenance, error) {
		return nil, DiscoverySelectionProvenance{}, errors.New("unsupported or unresolved discovery defaults input")
	}
	defaults, err := nacmodel.PinnedDiscoveryDefaults()
	if err != nil {
		return fail()
	}
	if len(overrides) > 0 {
		fields, err := nacmodel.DiscoveryDefaultOverrides(overrides)
		if err != nil {
			return fail()
		}
		for k, v := range fields {
			defaults[k] = v
		}
	}
	for k, v := range defaults {
		if validateDiscoveryDefault(k, v) != nil {
			return fail()
		}
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return fail()
	}
	var model map[string]any
	if node.Decode(&model) != nil {
		return fail()
	}
	inventory, ok := model["inventory"].(map[string]any)
	if !ok {
		return fail()
	}
	rows, ok := inventory["discovery"].([]any)
	if !ok || len(rows) > 100 {
		return fail()
	}
	base, err := json.Marshal(map[string]any{nacmodel.Stripe: model})
	if err != nil || len(base) > 1<<20 {
		return fail()
	}
	budget := len(base)
	costs := map[string]int{}
	for field, value := range defaults {
		key, _ := json.Marshal(field)
		valueJSON, err := json.Marshal(value)
		if err != nil {
			return fail()
		}
		costs[field] = len(key) + len(valueJSON) + 2
	}
	applied := 0
	for _, row := range rows {
		entry, ok := row.(map[string]any)
		if !ok {
			return fail()
		}
		for field, value := range defaults {
			if _, present := entry[field]; !present {
				// Bound expansion before serialization, even when many jobs share
				// one large defaults slice. Runtime job limits apply before filling.
				if costs[field] > (1<<20)-budget {
					return fail()
				}
				budget += costs[field]
				entry[field] = value
				applied++
			}
		}
	}
	expanded, err := json.Marshal(map[string]any{nacmodel.Stripe: model})
	if err != nil {
		return fail()
	}
	out, meta, err := ExportSelectedDiscovery(expanded, options)
	if err != nil {
		return fail()
	}
	meta.Exporter = DiscoveryDefaultsExporter
	meta.PolicySHA256 = discoveryDefaultExportSHA
	meta.InputSHA256 = networkExportDigest(raw)
	meta.DefaultsSHA256 = nacmodel.DefaultsSHA256
	meta.TerraformSHA256 = nacmodel.DiscoveryTerraformSHA256
	meta.AppliedDefaultFields = applied
	if len(overrides) > 0 {
		meta.OverridesSHA256 = networkExportDigest(overrides)
	}
	meta.Limitations = []string{"Scoped non-secret discovery defaults only; no inline credentials, identity/credential-name fallback, file merging or full schema/parity attestation.", "Pinned bundle supplies no discovery defaults; explicit supported overrides precede missing leaf values only. Optional nulls remain unselected and suppress fallback; unresolved required values and incompatible combinations are refused.", "All defaults, including unused/shadowed fields, and all expanded jobs validate before selection. Existing ownership, physical safeguards and native execution remain unchanged."}
	return out, meta, nil
}
