// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package writers

// Phase-3 crypto / VPN writer family set.
//
// These families carry credentials (shared keys, PSKs, PKI certs).
// The additive-merge semantics mean a device-side key that is not in
// the CR's intent is not erased — credentials should not be
// round-tripped through the reconciler's Fetch path. Operators who
// want to pin keys in intent must supply them; operators who want
// keys managed out-of-band can leave them unset.

func init() {
	// PKI trustpoints.
	Override(keyedListWriter{
		family:      "crypto_pki_trustpoint",
		yangPath:    "/Cisco-IOS-XE-native:native/crypto/pki/trustpoint",
		envelopeKey: "Cisco-IOS-XE-crypto:trustpoint",
		innerKey:    "trustpoints",
		keyField:    "id",
		managedLeaves: []string{
			"enrollment",
			"revocation-check",
			"rsakeypair",
			"subject-name",
			"fingerprint",
		},
	})

	// IKEv2 profiles.
	Override(keyedListWriter{
		family:      "crypto_ikev2_profile",
		yangPath:    "/Cisco-IOS-XE-native:native/crypto/ikev2/profile",
		envelopeKey: "Cisco-IOS-XE-crypto:profile",
		innerKey:    "profiles",
		keyField:    "name",
		managedLeaves: []string{
			"authentication",
			"identity",
			"keyring",
			"match",
			"pki",
			"lifetime",
		},
	})

	// IPsec transform-sets.
	// The "mode" container contains empty-leaf children (tunnel,
	// transport) — YANG `type empty;`. Caught against C8000V
	// 17.16.01a: {"mode":{"tunnel":true}} rejected; device expects
	// {"mode":{"tunnel":[null]}}.
	Override(keyedListWriter{
		family:      "crypto_ipsec_transform_set",
		yangPath:    "/Cisco-IOS-XE-native:native/crypto/ipsec/transform-set",
		envelopeKey: "Cisco-IOS-XE-crypto:transform-set",
		innerKey:    "transform_sets",
		keyField:    "tag",
		managedLeaves: []string{
			"esp",
			"ah",
			"mode",
		},
		yangBodyShape: transformSetToYANG,
	})

	// IPsec profiles.
	Override(keyedListWriter{
		family:      "crypto_ipsec_profile",
		yangPath:    "/Cisco-IOS-XE-native:native/crypto/ipsec/profile",
		envelopeKey: "Cisco-IOS-XE-crypto:profile",
		innerKey:    "profiles",
		keyField:    "name",
		managedLeaves: []string{
			"set",
			"match",
			"responder-only",
		},
	})

	// Crypto maps (classic IPsec).
	Override(keyedListWriter{
		family:      "crypto_map",
		yangPath:    "/Cisco-IOS-XE-native:native/crypto/map",
		envelopeKey: "Cisco-IOS-XE-crypto:map",
		innerKey:    "maps",
		keyField:    "name",
		managedLeaves: []string{
			"sequences",
			"local-address",
		},
	})
}

// transformSetToYANG converts boolean empty-leaf children of the
// "mode" container to RFC 7951 [null] encoding.
func transformSetToYANG(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		if k == "mode" {
			mode, ok := v.(map[string]any)
			if !ok {
				out[k] = v
				continue
			}
			fixed := make(map[string]any, len(mode))
			for mk, mv := range mode {
				switch mk {
				case "tunnel", "transport":
					if isTrue(mv) {
						fixed[mk] = []any{nil}
					}
				default:
					fixed[mk] = mv
				}
			}
			out[k] = fixed
			continue
		}
		out[k] = v
	}
	return out
}
