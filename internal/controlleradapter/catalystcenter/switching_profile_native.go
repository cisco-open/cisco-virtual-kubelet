// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
)

// Private populated native state and its typed view come from one response.
// Unknown fields remain protected in memory; no unkeyed digest or raw native
// value belongs in status, errors, logs or public dependency identities.
type switchingProfileNativeObservation struct {
	profile networkProfile
	native  map[string]any
}

func (*switchingProfileNativeObservation) String() string {
	return "<private switching profile native observation>"
}
func (*switchingProfileNativeObservation) GoString() string {
	return "<private switching profile native observation>"
}

func decodeSwitchingProfileNative(raw []byte, summary networkProfile) (*switchingProfileNativeObservation, error) {
	if !validAPIID(summary.ID) || summary.Namespace != "switching" {
		return nil, errInvalidResponse
	}
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, errInvalidResponse
	}
	var p networkProfile
	if json.Unmarshal(envelope.Response, &p) != nil || p.ID != summary.ID || p.Name != summary.Name || p.Namespace != summary.Namespace || p.Attributes == nil {
		return nil, errInvalidResponse
	}
	var native map[string]any
	decoder := json.NewDecoder(bytes.NewReader(envelope.Response))
	decoder.UseNumber()
	if decoder.Decode(&native) != nil || native == nil {
		return nil, errInvalidResponse
	}
	groups, ok := native["profileAttributes"].([]any)
	if !ok {
		return nil, errInvalidResponse
	}
	seen := map[string]bool{}
	for _, item := range groups {
		group, ok := item.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		key, ok := group["key"].(string)
		if !ok || key == "" || seen[key] {
			return nil, errInvalidResponse
		}
		seen[key] = true
		switch key {
		case "site.settings":
			normalizePrivateProfileAttributes(group)
		case "cli.templates", "day0.templates":
			attrs, exists := group["attribs"]
			if !exists {
				continue
			}
			if attrs == nil {
				continue
			}
			values, ok := attrs.([]any)
			if !ok {
				return nil, errInvalidResponse
			}
			ids := map[string]bool{}
			for _, value := range values {
				entry, ok := value.(map[string]any)
				if !ok {
					return nil, errInvalidResponse
				}
				id, ok := entry["value"].(string)
				if !ok || entry["key"] != "template.id" || !validAPIID(id) || ids[id] {
					return nil, errInvalidResponse
				}
				ids[id] = true
			}
			sort.Slice(values, func(i, j int) bool {
				return values[i].(map[string]any)["value"].(string) < values[j].(map[string]any)["value"].(string)
			})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].(map[string]any)["key"].(string) < groups[j].(map[string]any)["key"].(string)
	})
	return &switchingProfileNativeObservation{profile: p, native: native}, nil
}
func (c *client) readSwitchingProfileNative(ctx context.Context, summary networkProfile) (*switchingProfileNativeObservation, error) {
	if !validAPIID(summary.ID) || summary.Namespace != "switching" {
		return nil, errInvalidResponse
	}
	raw, err := c.get(ctx, networkProfilesPath+"/"+summary.ID, url.Values{"populated": {"true"}}, 200)
	if err != nil {
		return nil, err
	}
	return decodeSwitchingProfileNative(raw, summary)
}
func sameSwitchingNative(a, b *switchingProfileNativeObservation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(a.native, b.native)
}

// Use the existing approved projection/comparison identity. This returns a
// domain-separated keyed comparison only; publication and lifecycle authority
// require independent original-operation/owner/source receipt validation.
func switchingProfileNativeMAC(ctx context.Context, comparison *templateInputComparison, binding string, observed *switchingProfileNativeObservation) (string, error) {
	if comparison == nil || len(comparison.key) != 32 || !validTemplateComparisonDigest(comparison.keyID) || comparison.check == nil || !validSwitchingProfileBinding(binding) || observed == nil || observed.native == nil || observed.profile.Namespace != "switching" || !validAPIID(observed.profile.ID) || !validProfileName(observed.profile.Name) {
		return "", errConfigurationSecret
	}
	if err := comparison.check(ctx); err != nil {
		return "", err
	}
	raw, err := json.Marshal(observed.native)
	if err != nil {
		return "", errInvalidResponse
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte("cvk-switching-profile-native-v1\x00" + comparison.keyID + "\x00" + binding + "\x00" + switchingNameDigest(observed.profile.Name) + "\x00" + observed.profile.ID + "\x00"))
	mac.Write(raw)
	if err := comparison.check(ctx); err != nil {
		return "", err
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}
