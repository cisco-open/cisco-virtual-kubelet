// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
)

// Hash the complete detail object, including attributes not managed by the
// source. Decode numbers without float rounding and refuse ambiguous duplicate
// keys. Object order/whitespace are irrelevant; array order remains significant.
func wirelessFeatureNativeFingerprint(raw []byte) (string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func() (any, error)
	value = func() (any, error) {
		token, err := d.Token()
		if err != nil {
			return nil, errInvalidResponse
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return token, nil
		}
		switch delimiter {
		case '{':
			out := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, errInvalidResponse
				}
				name, ok := key.(string)
				if !ok {
					return nil, errInvalidResponse
				}
				if _, exists := out[name]; exists {
					return nil, errInvalidResponse
				}
				child, err := value()
				if err != nil {
					return nil, err
				}
				out[name] = child
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, errInvalidResponse
			}
			return out, nil
		case '[':
			out := []any{}
			for d.More() {
				child, err := value()
				if err != nil {
					return nil, err
				}
				out = append(out, child)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, errInvalidResponse
			}
			return out, nil
		default:
			return nil, errInvalidResponse
		}
	}
	result, err := value()
	if err != nil {
		return "", err
	}
	if _, ok := result.(map[string]any); !ok {
		return "", errInvalidResponse
	}
	if _, err = d.Token(); err != io.EOF {
		return "", errInvalidResponse
	}
	return featureIdentityDigest(result), nil
}
func validWirelessFeatureFingerprint(hash string) bool {
	if len(hash) != 64 || strings.ToLower(hash) != hash {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// Legacy creator/applied receipts have only a UUID. New publications append a
// full-state digest in the same bounded resource string. Missing legacy digests
// retain ownership/recovery semantics, but cannot prove native state for pruning.
func featureReceiptIdentity(resource, prefix, key string) (id, fingerprint string, err error) {
	head := prefix + key + ":"
	if !strings.HasPrefix(resource, head) {
		return "", "", errConfigurationOwned
	}
	parts := strings.Split(strings.TrimPrefix(resource, head), ":")
	if len(parts) < 1 || len(parts) > 2 || !canonicalWirelessFeatureUUID(parts[0]) {
		return "", "", errConfigurationOwned
	}
	if len(parts) == 2 {
		if !validWirelessFeatureFingerprint(parts[1]) {
			return "", "", errConfigurationOwned
		}
		fingerprint = parts[1]
	}
	return parts[0], fingerprint, nil
}
