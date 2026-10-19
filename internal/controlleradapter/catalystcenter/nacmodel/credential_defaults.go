// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	_ "embed"
	"errors"
)

//go:embed credential_defaults.go
var credentialDefaultsPolicy []byte

// Bind the shared strict defaults decoder as well as this scoped projection.
var CredentialDefaultsPolicySHA256 = defaultsDigest(append([]byte(NetworkDefaultsExporter+"\x00"), credentialDefaultsPolicy...))

// PinnedCredentialDefaults returns only non-secret active-protocol defaults.
// It shares the pinned bundle and strict decoder with profile default export.
// Model selection, value injection and execution are deliberately separate.
func PinnedCredentialDefaults() (map[string]map[string]any, error) {
	fail := func() (map[string]map[string]any, error) {
		return nil, errors.New("pinned credential defaults unavailable")
	}
	if Version != NetworkDefaultsVersion || Revision != NetworkDefaultsRevision || defaultsDigest(pinnedDefaults) != DefaultsSHA256 {
		return fail()
	}
	network, err := readNetworkDefaults(pinnedDefaults, false)
	if err != nil {
		return fail()
	}
	groups, ok := network["device_credentials"].(map[string]any)
	if !ok {
		return fail()
	}
	result := map[string]map[string]any{}
	for family, fields := range map[string][]string{"https_read_credentials": {"port"}, "https_write_credentials": {"port"}, "snmpv3_credentials": {"auth_type", "privacy_type"}} {
		source, ok := groups[family].(map[string]any)
		if !ok {
			return fail()
		}
		values := map[string]any{}
		for _, field := range fields {
			v, ok := source[field]
			if !ok || v == nil {
				return fail()
			}
			values[field] = v
		}
		result[family] = values
	}
	return result, nil
}

// CredentialDefaultsWithOverrides merges only explicitly supported non-secret
// fields. Values still require the product execution parser's validation,
// including unused/shadowed defaults, before an export may be emitted.
func CredentialDefaultsWithOverrides(raw []byte) (map[string]map[string]any, error) {
	result, err := PinnedCredentialDefaults()
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return result, nil
	}
	fail := func() (map[string]map[string]any, error) {
		return nil, errors.New("unsupported credential defaults override")
	}
	network, err := readScopedNetworkDefaults(raw, "credentials")
	if err != nil {
		return fail()
	}
	groups := network["device_credentials"].(map[string]any)
	for family, value := range groups {
		fields, ok := value.(map[string]any)
		if !ok {
			return fail()
		}
		allowed, ok := result[family]
		if !ok {
			return fail()
		}
		for field, v := range fields {
			if _, ok := allowed[field]; !ok || v == nil {
				return fail()
			}
			allowed[field] = v
		}
	}
	return result, nil
}
