// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// Only these non-secret fields may enter an ownership fingerprint. Extending
// inline credential support must not accidentally hash password material.
var discoveryFingerprintFields = []string{"name", "discoveryType", "ipAddressList", "protocolOrder", "globalCredentialIdList", "cdpLevel", "lldpLevel", "retry", "timeout", "netconfPort", "preferredMgmtIPMethod", "ipFilterList"}

const discoveryRequiredMask uint64 = 31

func discoveryFieldMask(fields map[string]any) (uint64, error) {
	mask := uint64(0)
	for key := range fields {
		i := slices.Index(discoveryFingerprintFields, key)
		if i < 0 {
			return 0, errConfigurationTransition
		}
		mask |= 1 << i
	}
	if mask&discoveryRequiredMask != discoveryRequiredMask {
		return 0, errConfigurationTransition
	}
	return mask, nil
}
func discoveryFingerprintStrings(value any) ([]string, error) {
	switch rows := value.(type) {
	case []string:
		return slices.Clone(rows), nil
	case []any:
		out := []string{}
		for _, row := range rows {
			s, ok := row.(string)
			if !ok {
				return nil, errInvalidResponse
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, errInvalidResponse
	}
}
func discoveryCanonicalEffect(fields map[string]any) (map[string]any, error) {
	if _, err := discoveryFieldMask(fields); err != nil {
		return nil, err
	}
	return canonicalDiscoveryPublicValues(fields)
}

// Callers must first enforce their resource's field allowlist and required mask.
// Keep the established normalization shared without widening ordinary discovery.
func canonicalDiscoveryPublicValues(fields map[string]any) (map[string]any, error) {
	kind, ok := fields["discoveryType"].(string)
	if !ok || discoveryType(kind) == "" {
		return nil, errInvalidResponse
	}
	kind = discoveryType(kind)
	out := map[string]any{}
	for key, value := range fields {
		if value == nil {
			return nil, errInvalidResponse
		}
		switch key {
		case "discoveryType":
			value = kind
			if kind == "SINGLE" || kind == "MULTI RANGE" {
				value = "RANGE"
			}
		case "ipAddressList":
			s, ok := value.(string)
			if !ok {
				return nil, errInvalidResponse
			}
			normalized, err := discoveryAddresses(s, kind == "CIDR")
			if err != nil {
				return nil, err
			}
			value = normalized
		case "protocolOrder":
			s, ok := value.(string)
			if !ok {
				return nil, errInvalidResponse
			}
			value = strings.ToUpper(s)
		case "globalCredentialIdList":
			ids, err := discoveryFingerprintStrings(value)
			if err != nil || len(ids) == 0 {
				return nil, errInvalidResponse
			}
			slices.Sort(ids)
			for i, id := range ids {
				if !validAPIID(id) || i > 0 && id == ids[i-1] {
					return nil, errInvalidResponse
				}
			}
			value = ids
		case "userNameList":
			users, err := discoveryFingerprintStrings(value)
			if err != nil || len(users) == 0 {
				return nil, errInvalidResponse
			}
			slices.Sort(users)
			for i, user := range users {
				if _, err := globalCredentialText(user); err != nil || i > 0 && user == users[i-1] {
					return nil, errInvalidResponse
				}
			}
			value = users
		case "ipFilterList":
			var rows []string
			var err error
			if text, ok := value.(string); ok {
				rows = []string{}
				if text != "" {
					rows = strings.Split(text, ",")
				}
			} else {
				rows, err = discoveryFingerprintStrings(value)
				if err != nil {
					return nil, err
				}
			}
			normalized := []string{}
			for _, row := range rows {
				v, err := discoveryAddresses(row, false)
				if err != nil {
					return nil, err
				}
				normalized = append(normalized, strings.Split(v, ",")...)
			}
			slices.Sort(normalized)
			if len(slices.Compact(slices.Clone(normalized))) != len(normalized) {
				return nil, errInvalidResponse
			}
			value = normalized
		}
		out[key] = value
	}
	return out, nil
}
func discoveryNativeEffect(got discoveryMetadata, mask uint64) (map[string]any, error) {
	if !validDiscoveryFieldMask(mask) {
		return nil, errConfigurationTransition
	}
	raw, err := json.Marshal(got)
	if err != nil {
		return nil, err
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	selected := map[string]any{}
	for i, key := range discoveryFingerprintFields {
		if mask&(1<<i) != 0 {
			value, present := all[key]
			if !present {
				return nil, errInvalidResponse
			}
			selected[key] = value
		}
	}
	return discoveryCanonicalEffect(selected)
}
func validDiscoveryFieldMask(mask uint64) bool {
	return mask&discoveryRequiredMask == discoveryRequiredMask && mask>>len(discoveryFingerprintFields) == 0
}
func ownedDiscoveryFingerprintParts(resource string) (uint64, string, bool) {
	resource = discoveryLogicalResource(resource)
	if !strings.HasPrefix(resource, ownedDiscoveryPrefix) {
		return 0, "", false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(resource, ownedDiscoveryPrefix), discoveryPruneSuffix), ":")
	if len(parts) == 2 {
		return 0, parts[1], true
	} // Read-only legacy result recovery.
	if len(parts) != 3 {
		return 0, "", false
	}
	mask, err := strconv.ParseUint(parts[1], 16, 64)
	if err != nil || !validDiscoveryFieldMask(mask) || strconv.FormatUint(mask, 16) != parts[1] {
		return 0, "", false
	}
	return mask, parts[2], true
}

// Shared strong-field verifier for normal and retained-source observation.
// A fingerprint is a native effect check, never an adoption or dispatch permit.
func verifyOwnedDiscoveryEffect(got discoveryMetadata, resource string) error {
	mask, digest, valid := ownedDiscoveryFingerprintParts(resource)
	if !valid || mask == 0 {
		return errConfigurationOwned
	}
	effect, err := discoveryNativeEffect(got, mask)
	if err != nil {
		return err
	}
	if discoveryPublicDigest(effect) != digest {
		return errConfigurationStale
	}
	return nil
}
