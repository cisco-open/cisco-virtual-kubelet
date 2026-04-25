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

package transport

// Schema-aware gNMI keyed-path support. Wave 5A from the external
// review (Codex Finding #12).
//
// Background: gNMI's PathElem.Key is a typed map<string, string>
// where the keys are the YANG list-key leaf names, NOT positional
// values. parseGNMIPath previously guessed the key name from the
// value type — `name` for strings, `id` for numerics. That works for
// the simple netascode families (vlan keyed by id, vrf keyed by
// name) but is wrong for many real IOS-XE lists: route-target keyed
// by `tag`, prefix-list entries keyed by `seq`, certificate
// trustpoints keyed by `name`, IKEv2 profile keyed by `name`, ip-acl
// extended entries keyed by `sequence`, etc. A wrong key name
// produces a Set whose path matches no list entry on the device,
// silently failing to write.
//
// The fix is a key-name registry indexed by the *last segment of the
// path*. The schema layer (internal/drivers/iosxe/configdriver/schema)
// iterates families.yaml at startup and calls RegisterPathKey(seg,
// keyFields...) once per keyed_list family. parseGNMIPath consults
// the registry on every "/seg=value" segment before falling back to
// the name/id heuristic — so legacy callers without a registered
// schema still produce the same paths they did pre-fix.
//
// The transport package owns the registry (rather than schema) so
// parseGNMIPath stays a pure function with no schema-package import,
// preserving the existing import direction (schema → transport via
// writers, never the reverse).

import (
	"strings"
	"sync"
)

var (
	pathKeyRegistryMu sync.RWMutex
	// pathKeyRegistry maps the LAST segment of a YANG path (with any
	// "module:" prefix stripped — same normalisation parseGNMIPath
	// applies) to the first key field for that list. The first field
	// is sufficient for single-key lists; composite-key lists need
	// the full list and are looked up via pathKeyRegistryComposite.
	pathKeyRegistry = map[string]string{}
	// pathKeyRegistryComposite holds the full key field set for
	// segments whose lists have more than one key. The current
	// path syntax `seg=value` only carries one value, so composite
	// keys are recorded for completeness but parseGNMIPath uses
	// only the first.
	pathKeyRegistryComposite = map[string][]string{}
)

// RegisterPathKey registers the YANG list-key field name(s) for the
// last segment of a path. Idempotent: re-registering with the same
// values is a no-op. The callsite is the schema layer's startup
// loop; calling this from anywhere else is allowed but unusual.
func RegisterPathKey(segment string, keyFields ...string) {
	if len(keyFields) == 0 || segment == "" {
		return
	}
	pathKeyRegistryMu.Lock()
	defer pathKeyRegistryMu.Unlock()
	pathKeyRegistry[segment] = keyFields[0]
	if len(keyFields) > 1 {
		pathKeyRegistryComposite[segment] = append([]string(nil), keyFields...)
	}
}

// pathKeyFor returns the first key field for the given path segment,
// or empty string when no entry is registered. Callers fall back to
// the historical name/id heuristic when this returns "".
func pathKeyFor(segment string) string {
	pathKeyRegistryMu.RLock()
	defer pathKeyRegistryMu.RUnlock()
	return pathKeyRegistry[segment]
}

// LastPathSegment is a small helper that extracts the last "/"-
// separated segment of a YANG xpath, with any "module:" prefix
// stripped. Mirrors the normalisation parseGNMIPath applies to a
// segment before consulting the registry; the schema layer uses it
// to compute the registry key from a families.yaml YANGPath entry.
//
// Example: "/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list"
// returns "vlan-list".
func LastPathSegment(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	last := parts[len(parts)-1]
	if i := strings.Index(last, ":"); i > 0 {
		last = last[i+1:]
	}
	return last
}
