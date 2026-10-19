// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed credential_defaults_export.go
var credentialExportPolicy []byte

//go:embed global_credentials.go
var credentialExportParser []byte
var credentialExportPolicySHA = networkExportDigest(append(append([]byte(nacmodel.CredentialDefaultsPolicySHA256+"\x00"), credentialExportPolicy...), credentialExportParser...))
var CredentialDefaultsExporter = "cvk-credential-defaults-v1:policy-sha256:" + credentialExportPolicySHA

type CredentialDefaultsProvenance struct {
	SiteSelection            *CredentialSiteSelectionProvenance `json:"siteSelection,omitempty"`
	OverridesSHA256          string                             `json:"overridesSHA256,omitempty"`
	Exporter                 string                             `json:"exporter"`
	Format                   string                             `json:"format"`
	ModelVersion             string                             `json:"modelVersion"`
	UpstreamRevision         string                             `json:"upstreamRevision"`
	PolicySHA256             string                             `json:"policySHA256"`
	DefaultsSHA256           string                             `json:"defaultsSHA256"`
	InputSHA256              string                             `json:"inputSHA256"`
	OutputSHA256             string                             `json:"outputSHA256"`
	Resources                int                                `json:"resources"`
	DefaultedFields          int                                `json:"defaultedFields"`
	ExecutionSyntaxValidated bool                               `json:"executionSyntaxValidated"`
	LeafSchemaValidated      bool                               `json:"leafSchemaValidated"`
	ConfigurationParity      bool                               `json:"configurationParity"`
	Limitations              []string                           `json:"limitations"`
}

// Explicit offline preprocessing of one already-selected credential section.
// Missing active fields use pinned defaults; explicit null never means missing.
// Passwords and counters are never defaulted. Runtime compilation is unchanged.
func ExportCredentialDefaults(raw []byte) ([]byte, CredentialDefaultsProvenance, error) {
	return ExportCredentialDefaultsWithOverrides(raw, nil)
}

func ExportCredentialDefaultsWithOverrides(raw, overrides []byte) ([]byte, CredentialDefaultsProvenance, error) {
	fail := func() ([]byte, CredentialDefaultsProvenance, error) {
		return nil, CredentialDefaultsProvenance{}, errors.New("unsupported or unresolved credential default input")
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return fail()
	}
	var model map[string]any
	if node.Decode(&model) != nil || len(model) != 1 {
		return fail()
	}
	network, ok := model["network_settings"].(map[string]any)
	if !ok || len(network) != 1 {
		return fail()
	}
	groups, ok := network["device_credentials"].(map[string]any)
	if !ok {
		return fail()
	}
	defaults, err := nacmodel.CredentialDefaultsWithOverrides(overrides)
	if err != nil {
		return fail()
	}
	// Validate every fallback, including those shadowed by explicit fields or
	// unused in this selection. Probes carry no values or execution authority.
	for family, fields := range defaults {
		probe := map[string]any{"name": "defaults-validation", "username": "defaults-validation"}
		if family == "snmpv3_credentials" {
			probe["snmp_mode"] = "AUTHPRIV"
			probe["auth_password_version"] = 1
			probe["privacy_password_version"] = 1
		} else {
			probe["password_version"] = 1
		}
		for field, value := range fields {
			probe[field] = value
		}
		encoded, err := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"network_settings": map[string]any{"device_credentials": map[string]any{family: []any{probe}}}}})
		if err != nil {
			return fail()
		}
		if _, err := parseGlobalCredentials(encoded, true); err != nil {
			return fail()
		}
	}
	count := 0
	for family, value := range groups {
		rows, ok := value.([]any)
		if !ok {
			return fail()
		}
		for _, v := range rows {
			entry, ok := v.(map[string]any)
			if !ok {
				return fail()
			}
			for field, fallback := range defaults[family] {
				if family == "snmpv3_credentials" {
					mode, _ := entry["snmp_mode"].(string)
					// Inactive protocol defaults have no native effect. Explicit inactive
					// declarations still pass through the strict execution parser and refuse.
					if field == "privacy_type" && mode != "AUTHPRIV" || field == "auth_type" && mode != "AUTHPRIV" && mode != "AUTHNOPRIV" {
						continue
					}
				}
				if _, present := entry[field]; !present {
					entry[field] = fallback
					count++
				}
			}
		}
	}
	out, err := json.MarshalIndent(map[string]any{nacmodel.Stripe: model}, "", "  ")
	if err != nil || len(out)+1 > 1<<20 {
		return fail()
	}
	out = append(out, '\n')
	wants, err := parseGlobalCredentials(out, true)
	if err != nil {
		return fail()
	}
	meta := CredentialDefaultsProvenance{Exporter: CredentialDefaultsExporter, Format: nacmodel.Format, ModelVersion: nacmodel.Version, UpstreamRevision: nacmodel.Revision, PolicySHA256: credentialExportPolicySHA, DefaultsSHA256: nacmodel.DefaultsSHA256, InputSHA256: networkExportDigest(raw), OutputSHA256: networkExportDigest(out), Resources: len(wants), DefaultedFields: count, ExecutionSyntaxValidated: true, Limitations: []string{"Active non-secret HTTPS port and SNMP protocol defaults only; explicit null/inactive declarations must satisfy the existing strict execution contract.", "Credential values, usernames, modes and write-only counters must be supplied separately; inline secret values are rejected.", "Input is one already-selected global credential section; no per-site filtering, file merging or other model sections; overrides are restricted to supported non-secret fields.", "No native identity, Secret resolution, API write, ownership transfer or full nested schema/parity attestation is performed."}}
	if len(overrides) > 0 {
		meta.OverridesSHA256 = networkExportDigest(overrides)
	}
	return out, meta, nil
}
