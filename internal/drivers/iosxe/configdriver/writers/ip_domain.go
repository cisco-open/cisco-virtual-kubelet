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

// IP domain (DNS) Phase-3 writer.
//
// netascode:
//   ip_domain:
//     name: example.com
//     lookup: true
//     list:
//       - example.com
//
// YANG: /Cisco-IOS-XE-native:native/ip/domain.

func init() {
	Override(singletonWriter{
		family:        "ip_domain",
		yangPath:      "/Cisco-IOS-XE-native:native/ip/domain",
		envelopeKey:   "Cisco-IOS-XE-native:domain",
		managedLeaves: []string{"name", "lookup", "list"},
	})
}

// ipDomainBodyTransform2601 converts the canonical NetAsCode
// ip_domain.name leaf to the IOS-XE 26.01 YANG shape observed on
// Catalyst 9300:
//
//	name → name-container.name-no-vrf
//
// lookup and list keep their baseline shape.
func ipDomainBodyTransform2601(body map[string]any) map[string]any {
	name, ok := body["name"]
	if !ok {
		return body
	}
	delete(body, "name")
	body["name-container"] = map[string]any{
		"name-no-vrf": name,
	}
	return body
}

// ipDomainFetchTransform2601 is the inverse of
// ipDomainBodyTransform2601. It maps IOS-XE 26.01 observed state back
// to the canonical NetAsCode shape so drift comparison remains stable
// across software releases.
func ipDomainFetchTransform2601(body map[string]any) map[string]any {
	container, ok := body["name-container"].(map[string]any)
	if !ok {
		return body
	}
	if name, ok := container["name-no-vrf"]; ok {
		body["name"] = name
	}
	delete(body, "name-container")
	return body
}
