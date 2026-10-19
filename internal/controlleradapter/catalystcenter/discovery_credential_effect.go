// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

const discoveryCredentialEffectReceiptPrefix = "discovery-credential-record:v2:"
const discoveryCredentialReceiptFamily = "discovery-credential-record:"
const discoveryCredentialRequiredMask uint64 = 15 // name, type, addresses, protocol

// Closed public-only vocabulary. A full credential-bearing request must never
// be fingerprinted by subtracting password-looking properties from its body.
var discoveryCredentialPublicFields = append(slices.Clone(discoveryFingerprintFields), "userNameList", "snmpVersion", "snmpMode", "snmpUserName", "snmpAuthProtocol", "snmpPrivProtocol", "snmpRoCommunityDesc", "snmpRwCommunityDesc")

type discoveryCredentialPublicEffect struct {
	mask   uint64
	digest string
}

func validDiscoveryCredentialEffectMask(mask uint64) bool {
	return mask&discoveryCredentialRequiredMask == discoveryCredentialRequiredMask && mask>>len(discoveryCredentialPublicFields) == 0
}
func lowerDiscoveryDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func discoveryCredentialEffect(fields map[string]any) (discoveryCredentialPublicEffect, error) {
	mask := uint64(0)
	selected := map[string]any{}
	for key, value := range fields {
		index := slices.Index(discoveryCredentialPublicFields, key)
		if index < 0 {
			return discoveryCredentialPublicEffect{}, errConfigurationSecret
		}
		mask |= 1 << index
		switch key {
		case "cdpLevel", "lldpLevel", "retry", "timeout":
			min, max := 0, 16
			if key == "retry" {
				max = 10
			}
			if key == "timeout" {
				min, max = 1, 3600
			}
			n, err := policyServerInteger(value, min, max)
			if err != nil {
				return discoveryCredentialPublicEffect{}, errConfigurationTransition
			}
			value = *n
		case "globalCredentialIdList", "ipFilterList", "userNameList": // Shared canonicalizer validates string sets.
		case "ipAddressList":
			text, ok := value.(string)
			if !ok || text == "" || len(text) > 8192 {
				return discoveryCredentialPublicEffect{}, errConfigurationTransition
			}
		default:
			if _, err := globalCredentialText(value); err != nil {
				return discoveryCredentialPublicEffect{}, errConfigurationTransition
			}
		}
		selected[key] = value
	}
	if !validDiscoveryCredentialEffectMask(mask) {
		return discoveryCredentialPublicEffect{}, errConfigurationTransition
	}
	canonical, err := canonicalDiscoveryPublicValues(selected)
	if err != nil {
		return discoveryCredentialPublicEffect{}, err
	}
	return discoveryCredentialPublicEffect{mask: mask, digest: discoveryPublicDigest(canonical)}, nil
}
func nativeDiscoveryCredentialEffect(got discoveryMetadata, mask uint64) (discoveryCredentialPublicEffect, error) {
	if !validDiscoveryCredentialEffectMask(mask) {
		return discoveryCredentialPublicEffect{}, errConfigurationTransition
	}
	raw, err := json.Marshal(got)
	if err != nil {
		return discoveryCredentialPublicEffect{}, err
	}
	var public map[string]any
	if json.Unmarshal(raw, &public) != nil {
		return discoveryCredentialPublicEffect{}, errInvalidResponse
	}
	selected := map[string]any{}
	for i, key := range discoveryCredentialPublicFields {
		if mask&(1<<i) != 0 {
			value, present := public[key]
			if !present {
				return discoveryCredentialPublicEffect{}, errInvalidResponse
			}
			// Existing discovery matching treats a native null/empty filter as
			// the empty set. Preserve that equivalence for an explicitly selected
			// empty filter, without inventing values for other missing fields.
			if key == "ipFilterList" && value == nil {
				value = []string{}
			}
			selected[key] = value
		}
	}
	return discoveryCredentialEffect(selected)
}
func checkedDiscoveryCredentialEffect(public map[string]any, got discoveryMetadata) (discoveryCredentialPublicEffect, error) {
	expected, err := discoveryCredentialEffect(public)
	if err != nil {
		return discoveryCredentialPublicEffect{}, err
	}
	actual, err := nativeDiscoveryCredentialEffect(got, expected.mask)
	if err != nil {
		return discoveryCredentialPublicEffect{}, err
	}
	if actual != expected {
		return discoveryCredentialPublicEffect{}, errConfigurationOwned
	}
	return actual, nil
}
func discoveryCredentialEffectReceiptKey(name, id string, effect discoveryCredentialPublicEffect) string {
	if !lowerDiscoveryDigest(name) || !validAPIID(id) || !validDiscoveryCredentialEffectMask(effect.mask) || !lowerDiscoveryDigest(effect.digest) {
		return ""
	}
	return discoveryCredentialEffectReceiptPrefix + name + ":" + strconv.FormatUint(effect.mask, 16) + ":" + effect.digest + ":" + id
}
func discoveryCredentialReceiptSelected(resource string) bool {
	return strings.HasPrefix(resource, discoveryCredentialReceiptFamily)
}
func discoveryCredentialReceiptForName(resource, name string) bool {
	parts := strings.SplitN(strings.TrimPrefix(resource, discoveryCredentialReceiptFamily), ":", 3)
	return discoveryCredentialReceiptSelected(resource) && len(parts) >= 2 && parts[1] == name
}

type discoveryCredentialReceiptIdentity struct {
	name, id string
	effect   discoveryCredentialPublicEffect
	legacy   bool
}

func parseDiscoveryCredentialReceipt(resource string) (discoveryCredentialReceiptIdentity, error) {
	var identity discoveryCredentialReceiptIdentity
	if strings.HasPrefix(resource, discoveryCredentialReceiptPrefix) {
		parts := strings.Split(strings.TrimPrefix(resource, discoveryCredentialReceiptPrefix), ":")
		if len(parts) != 2 || !lowerDiscoveryDigest(parts[0]) || !validAPIID(parts[1]) {
			return identity, errConfigurationOwned
		}
		identity.name, identity.id, identity.legacy = parts[0], parts[1], true
		return identity, nil
	}
	if !strings.HasPrefix(resource, discoveryCredentialEffectReceiptPrefix) {
		return identity, errConfigurationOwned
	}
	parts := strings.Split(strings.TrimPrefix(resource, discoveryCredentialEffectReceiptPrefix), ":")
	if len(parts) != 4 || !lowerDiscoveryDigest(parts[0]) || !lowerDiscoveryDigest(parts[2]) || !validAPIID(parts[3]) {
		return identity, errConfigurationOwned
	}
	mask, err := strconv.ParseUint(parts[1], 16, 64)
	if err != nil || !validDiscoveryCredentialEffectMask(mask) || strconv.FormatUint(mask, 16) != parts[1] {
		return identity, errConfigurationOwned
	}
	identity.name, identity.id, identity.effect = parts[0], parts[3], discoveryCredentialPublicEffect{mask: mask, digest: parts[2]}
	return identity, nil
}
