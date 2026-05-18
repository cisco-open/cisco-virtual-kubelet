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

package validation

import "fmt"

func validateReleaseProfile(ctx Context, envelope string, payload any) error {
	switch {
	case ctx.Family == "ip_domain" && ctx.ReleaseTag == "2601":
		return validateIPDomain2601(envelope, payload)
	default:
		return nil
	}
}

func validateIPDomain2601(envelope string, payload any) error {
	if envelope != "Cisco-IOS-XE-native:domain" {
		return fmt.Errorf("ip_domain 2601: envelope %q, want Cisco-IOS-XE-native:domain", envelope)
	}
	body, ok := payload.(map[string]any)
	if !ok {
		return fmt.Errorf("ip_domain 2601: payload must be a JSON object, got %T", payload)
	}
	if _, hasCanonical := body["name"]; hasCanonical {
		return fmt.Errorf("ip_domain 2601: canonical NetAsCode leaf name must be translated to name-container.name-no-vrf")
	}
	if _, hasLookup := body["lookup"]; hasLookup {
		// lookup is unchanged across the currently validated releases.
	}
	if _, hasList := body["list"]; hasList {
		// list is unchanged across the currently validated releases.
	}
	if raw, ok := body["name-container"]; ok {
		container, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("ip_domain 2601: name-container must be a JSON object, got %T", raw)
		}
		if _, ok := container["name-no-vrf"]; !ok {
			return fmt.Errorf("ip_domain 2601: name-container missing name-no-vrf")
		}
	}
	return nil
}
