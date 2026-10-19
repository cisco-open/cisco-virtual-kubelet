// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
)

//go:embed upstream/0.5.0/defaults.yaml
var pinnedDefaults []byte

// Embed the actual resolution policy to identify uncommitted builds without
// mislabelling a defaults-file digest as a schema or exporter-code digest.
//
//go:embed network_defaults.go
var defaultsPolicySource []byte

var networkDefaultsPolicySHA = defaultsDigest(append(append([]byte{}, defaultsPolicySource...), defaultsDocumentPolicy...))

var NetworkDefaultsExporter = "cvk-network-defaults-v1:policy-sha256:" + networkDefaultsPolicySHA

const NetworkDefaultsVersion = "0.5.0"
const NetworkDefaultsRevision = "a435555fe828f03cf3311d52fe1ab6572a247916"

const DefaultsSHA256 = "8efb7f12853af1f8d2b182fb2b9e323e30ddf2330e61286dbb959a84fd5a505f"

// NetworkDefaultsProvenance describes only offline profile-default expansion.
// Native identities, schema validation and operation authority remain separate.
type DefaultsProvenance struct {
	SiteSelection            *NetworkSiteSelectionProvenance `json:"siteSelection,omitempty"`
	Exporter                 string                          `json:"exporter"`
	ExecutionSyntaxValidated bool                            `json:"executionSyntaxValidated"`
	LeafSchemaValidated      bool                            `json:"leafSchemaValidated"`
	Format                   string                          `json:"format"`
	ModelVersion             string                          `json:"modelVersion"`
	UpstreamRevision         string                          `json:"upstreamRevision"`
	PolicySHA256             string                          `json:"policySHA256"`
	DefaultsSHA256           string                          `json:"defaultsSHA256"`
	InputSHA256              string                          `json:"inputSHA256"`
	OverridesSHA256          string                          `json:"overridesSHA256,omitempty"`
	OutputSHA256             string                          `json:"outputSHA256"`
	ResolutionScope          string                          `json:"resolutionScope"`
	ConfigurationParity      bool                            `json:"configurationParity"`
	Limitations              []string                        `json:"limitations"`
}

// Preserve the existing export API while sharing non-secret provenance fields.
type NetworkDefaultsProvenance = DefaultsProvenance

type NetworkSiteSelectionProvenance struct {
	OptionsSHA256     string `json:"optionsSHA256"`
	SelectedSiteCount int    `json:"selectedSiteCount"`
}

func defaultsDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

var errNetworkDefaults = errors.New("unsupported or unresolved network-profile default input")

// ExportNetworkDefaults is explicit offline export. ParseDocument and all
// runtime reconcilers deliberately do not invoke it. Input must already be a
// single selected controller stripe with network profiles and site references.
func ExportNetworkDefaults(raw, overrides []byte) ([]byte, NetworkDefaultsProvenance, error) {
	meta := NetworkDefaultsProvenance{Exporter: NetworkDefaultsExporter, PolicySHA256: networkDefaultsPolicySHA, Format: Format, ModelVersion: Version, UpstreamRevision: Revision, DefaultsSHA256: DefaultsSHA256, InputSHA256: defaultsDigest(raw), ResolutionScope: "network-settings-profiles-only", Limitations: []string{"Not full nested schema validation or complete model/default parity.", "No multi-file merging, managed-site filtering, native identity resolution, ownership transfer or API execution.", "Explicit null collector selection and null default overrides require upstream resolution."}}
	fail := func() ([]byte, NetworkDefaultsProvenance, error) {
		return nil, NetworkDefaultsProvenance{}, errNetworkDefaults
	}
	if Version != NetworkDefaultsVersion || Revision != NetworkDefaultsRevision || defaultsDigest(pinnedDefaults) != DefaultsSHA256 {
		return fail()
	}
	node, err := ParseDocument(raw)
	if err != nil {
		return fail()
	}
	var model map[string]any
	if node.Decode(&model) != nil {
		return fail()
	}
	for key := range model {
		if key != "sites" && key != "network_settings" {
			return fail()
		}
	}
	profiles, ok := model["network_settings"].(map[string]any)
	if !ok {
		return fail()
	}
	for key := range profiles {
		if key != "network" && key != "telemetry" {
			return fail()
		}
	}
	defaults, err := readNetworkDefaults(pinnedDefaults, false)
	if err != nil {
		return fail()
	}
	if len(overrides) > 0 {
		custom, err := readNetworkDefaults(overrides, true)
		if err != nil {
			return fail()
		}
		meta.OverridesSHA256 = defaultsDigest(overrides)
		for category, fields := range custom {
			current, _ := defaults[category].(map[string]any)
			if current == nil {
				current = map[string]any{}
				defaults[category] = current
			}
			for field, value := range fields.(map[string]any) {
				current[field] = value
			}
		}
	}
	for category, value := range profiles {
		entries, ok := value.([]any)
		if !ok {
			return fail()
		}
		fallback, _ := defaults[category].(map[string]any)
		for _, entry := range entries {
			profile, ok := entry.(map[string]any)
			if !ok {
				return fail()
			}
			if category == "network" {
				// All scalar for_each predicates use the original non-null profile field.
				// Do not inject defaults that would create a previously unselected resource.
				if domain, present := profile["domain_name"]; present && domain != nil {
					if _, present := profile["dns_servers"]; !present {
						if servers, present := fallback["dns_servers"]; present {
							profile["dns_servers"] = servers
						}
					}
				}
				continue
			}
			// Terraform try() falls back on missing attributes, never on explicit null.
			for _, field := range []string{"wired_data_collection", "wireless_telemetry", "catalyst_center_as_snmp_server", "snmp_servers", "catalyst_center_as_syslog_server", "syslog_servers", "catalyst_center_as_network_collector", "enable_netflow_collector_on_devices"} {
				if _, present := profile[field]; !present {
					if value, present := fallback[field]; present {
						profile[field] = value
					}
				}
			}
			choice, ok := profile["catalyst_center_as_network_collector"].(bool)
			if !ok {
				return fail()
			}
			if choice {
				// The module suppresses external fields for Builtin even if input contains
				// them. This is expression evaluation, not a generic recursive merge.
				delete(profile, "netflow_collector_ip_address")
				delete(profile, "netflow_collector_port")
			} else {
				for _, field := range []string{"netflow_collector_ip_address", "netflow_collector_port"} {
					if _, present := profile[field]; !present {
						if value, present := fallback[field]; present {
							profile[field] = value
						}
					}
					if profile[field] == nil {
						return fail()
					}
				}
			}
		}
	}
	out, err := json.MarshalIndent(map[string]any{Stripe: model}, "", "  ")
	if err != nil || len(out)+1 > 1<<20 {
		return fail()
	}
	out = append(out, '\n')
	meta.OutputSHA256 = defaultsDigest(out)
	return out, meta, nil
}

// Preserve YAML node structure so the common parser rejects aliases, duplicate
// keys, extra documents and depth/size violations before decoding a defaults map.
func readNetworkDefaults(raw []byte, restricted bool) (map[string]any, error) {
	scope := ""
	if restricted {
		scope = "profiles"
	}
	return readScopedNetworkDefaults(raw, scope)
}

// Keep all defaults scopes on the same strict single-document decoder. Scope
// controls declarations, never value injection or runtime mutation authority.
func readScopedNetworkDefaults(raw []byte, scope string) (map[string]any, error) {
	if scope != "" && scope != "profiles" && scope != "credentials" {
		return nil, errNetworkDefaults
	}
	restricted := scope != ""
	stripe, err := readDefaultsStripe(raw, restricted)
	if err != nil {
		return nil, err
	}
	if restricted {
		for key := range stripe {
			if key != "network_settings" {
				return nil, errNetworkDefaults
			}
		}
	}
	network, ok := stripe["network_settings"].(map[string]any)
	if !ok {
		return nil, errNetworkDefaults
	}
	if scope == "credentials" {
		if len(network) != 1 {
			return nil, errNetworkDefaults
		}
		if _, ok := network["device_credentials"].(map[string]any); !ok {
			return nil, errNetworkDefaults
		}
		return network, nil
	}
	if restricted {
		allowedNetwork := map[string]bool{"ntp_servers": true, "dhcp_servers": true, "domain_name": true, "dns_servers": true, "timezone": true, "banner": true}
		allowedTelemetry := map[string]bool{"wired_data_collection": true, "wireless_telemetry": true, "catalyst_center_as_snmp_server": true, "snmp_servers": true, "catalyst_center_as_syslog_server": true, "syslog_servers": true, "catalyst_center_as_network_collector": true, "enable_netflow_collector_on_devices": true, "netflow_collector_ip_address": true, "netflow_collector_port": true}
		for category, value := range network {
			if category != "network" && category != "telemetry" {
				return nil, errNetworkDefaults
			}
			fields, ok := value.(map[string]any)
			if !ok {
				return nil, errNetworkDefaults
			}
			allowed := allowedNetwork
			if category == "telemetry" {
				allowed = allowedTelemetry
			}
			for key, value := range fields {
				if !allowed[key] || value == nil {
					return nil, errNetworkDefaults
				}
			}
		}
	}
	return network, nil
}
