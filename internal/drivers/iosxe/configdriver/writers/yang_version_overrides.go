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

import "sync"

// ──────────────────────────────────────────────────────────────────
// YANG Version Override System
//
// IOS-XE YANG models vary across software versions. Writers are
// authored against a baseline version (17.18.2 / C9300-24P), but
// earlier versions (17.15, 17.16) require:
//
//   - Module prefixes on augmented JSON body keys
//   - Different YANG list-key names
//   - Empty-leaf encoding ([null]) instead of boolean
//   - Different container shapes (flat ↔ nested)
//   - Parent-container creation before keyed-entry PUT
//
// Rather than scattering if/else trees across ~60 writer files, this
// file provides a data-driven override table. Each entry describes
// version-conditional mutations to a family's YANG configuration.
// The table is resolved once per process when SetDeviceVersion is
// called, and writers query the resolved state at Diff/Apply time.
//
// Adding support for a new IOS-XE release is a table entry, not a
// code change in the writer.
// ──────────────────────────────────────────────────────────────────

// VersionOverride describes version-conditional changes to a family's
// YANG wire representation. Multiple overrides can exist per family;
// the first whose version range matches wins.
type VersionOverride struct {
	// Family is the netascode family name (e.g. "route_map", "ntp").
	Family string

	// MinVersion is the inclusive lower bound [major, minor].
	// Use [0,0] for "all versions up to MaxVersion".
	MinVersion [2]int

	// MaxVersion is the exclusive upper bound [major, minor].
	// Use [99,99] for "all versions from MinVersion onward".
	MaxVersion [2]int

	// ElementMap maps JSON body keys that need renaming on this
	// version. The key is the baseline (17.18) name; the value is
	// the version-specific name. Applied recursively to op bodies.
	// Example: "route-map-without-order-seq" →
	//   "Cisco-IOS-XE-route-map:route-map-seq"
	ElementMap map[string]string

	// KeyFieldOverride, if non-empty, replaces the writer's static
	// keyField for list entries on this version range.
	KeyFieldOverride string

	// NestedYANGInnerOverride, if non-empty, replaces the
	// nestedYANGInner element name for nested keyed-list writers.
	NestedYANGInnerOverride string

	// EmptyLeaves lists JSON body keys that must be encoded as
	// YANG empty leaves: true → [null], false/nil → omit.
	EmptyLeaves []string

	// YANGPathOverride, if non-empty, replaces the writer's static
	// yangPath for this version range.
	YANGPathOverride string

	// EnvelopeKeyOverride, if non-empty, replaces the envelope key.
	EnvelopeKeyOverride string

	// BodyTransform, if non-nil, is applied to the projected body
	// map before JSON serialisation. Runs after ElementMap and
	// EmptyLeaves processing. Use for shape changes that can't be
	// expressed as simple key renames.
	BodyTransform func(body map[string]any) map[string]any

	// NeedParentCreation, if true, signals that VerbReplace (PUT)
	// to this family's path may 404 because an intermediate parent
	// container doesn't exist. The transport or writer should
	// create the parent first via a MERGE to the grandparent path.
	NeedParentCreation bool

	// ParentPath is the RESTCONF path of the intermediate container
	// to create before the keyed-entry PUT. Only used when
	// NeedParentCreation is true.
	ParentPath string

	// ParentBody is the minimal JSON body for the parent creation
	// MERGE. Only used when NeedParentCreation is true.
	ParentBody []byte
}

// versionInRange returns true if (major, minor) falls within
// [o.MinVersion, o.MaxVersion).
func (o *VersionOverride) versionInRange(major, minor int) bool {
	if major < o.MinVersion[0] || (major == o.MinVersion[0] && minor < o.MinVersion[1]) {
		return false
	}
	if major > o.MaxVersion[0] || (major == o.MaxVersion[0] && minor >= o.MaxVersion[1]) {
		return false
	}
	return true
}

// ──────────────────────────────────────────────────────────────────
// Resolved override state — populated once by ResolveForVersion
// ──────────────────────────────────────────────────────────────────

var (
	overrideMu sync.RWMutex
	// resolved maps family → the single matching override (or nil).
	resolved = map[string]*VersionOverride{}
)

// ResolveForVersion selects the matching override for each family
// given the device's IOS-XE version. Called once from
// SetDeviceVersion after parsing the version string.
func ResolveForVersion(major, minor int) {
	overrideMu.Lock()
	defer overrideMu.Unlock()
	resolved = make(map[string]*VersionOverride, len(overrideTable))
	for i := range overrideTable {
		o := &overrideTable[i]
		if o.versionInRange(major, minor) {
			// First match wins per family.
			if _, exists := resolved[o.Family]; !exists {
				resolved[o.Family] = o
			}
		}
	}
}

// GetOverride returns the resolved override for a family, or nil if
// the device version doesn't require any overrides for that family.
func GetOverride(family string) *VersionOverride {
	overrideMu.RLock()
	defer overrideMu.RUnlock()
	return resolved[family]
}

// ApplyElementMap rewrites JSON body keys according to the override's
// ElementMap. Recurses into nested maps and slices. Returns a new map;
// the input is not modified.
func ApplyElementMap(body map[string]any, emap map[string]string) map[string]any {
	if len(emap) == 0 {
		return body
	}
	out := make(map[string]any, len(body))
	for k, v := range body {
		newKey := k
		if mapped, ok := emap[k]; ok {
			newKey = mapped
		}
		out[newKey] = applyElementMapValue(v, emap)
	}
	return out
}

func applyElementMapValue(v any, emap map[string]string) any {
	switch tv := v.(type) {
	case map[string]any:
		return ApplyElementMap(tv, emap)
	case []any:
		out := make([]any, len(tv))
		for i, el := range tv {
			out[i] = applyElementMapValue(el, emap)
		}
		return out
	default:
		return v
	}
}

// ApplyEmptyLeaves transforms the named keys to YANG empty-leaf
// encoding: true → [null], false/nil → delete the key.
func ApplyEmptyLeaves(body map[string]any, leaves []string) map[string]any {
	if len(leaves) == 0 {
		return body
	}
	for _, leaf := range leaves {
		v, ok := body[leaf]
		if !ok {
			continue
		}
		if isTrue(v) {
			body[leaf] = []any{nil}
		} else {
			delete(body, leaf)
		}
	}
	return body
}

// ApplyOverrideToBody applies the full chain of override transforms
// to a projected body map: ElementMap → EmptyLeaves → BodyTransform.
func ApplyOverrideToBody(body map[string]any, o *VersionOverride) map[string]any {
	if o == nil {
		return body
	}
	if len(o.ElementMap) > 0 {
		body = ApplyElementMap(body, o.ElementMap)
	}
	if len(o.EmptyLeaves) > 0 {
		body = ApplyEmptyLeaves(body, o.EmptyLeaves)
	}
	if o.BodyTransform != nil {
		body = o.BodyTransform(body)
	}
	return body
}

// ReverseElementMap inverts the ElementMap: YANG-native keys back to
// their baseline (netascode) names. Used in the Fetch path so that
// observed data matches the desired-side key names for comparison.
func ReverseElementMap(body map[string]any, emap map[string]string) map[string]any {
	if len(emap) == 0 {
		return body
	}
	// Build the inverse map.
	inv := make(map[string]string, len(emap))
	for k, v := range emap {
		inv[v] = k
	}
	return ApplyElementMap(body, inv)
}

// ReverseOverrideFromBody applies the full reverse chain of override
// transforms to an observed body: reverse ElementMap → DecodeEmptyLeaves.
func ReverseOverrideFromBody(body map[string]any, o *VersionOverride) map[string]any {
	if o == nil {
		return body
	}
	if len(o.ElementMap) > 0 {
		body = ReverseElementMap(body, o.ElementMap)
	}
	if len(o.EmptyLeaves) > 0 {
		body = DecodeEmptyLeaves(body, o.EmptyLeaves)
	}
	return body
}

// DecodeEmptyLeaves converts YANG empty-leaf encoding back to
// boolean for the Fetch path: [null] → true. Keys not present
// in the body are left untouched.
func DecodeEmptyLeaves(body map[string]any, leaves []string) map[string]any {
	if len(leaves) == 0 {
		return body
	}
	for _, leaf := range leaves {
		v, ok := body[leaf]
		if !ok {
			continue
		}
		if isTrue(v) {
			body[leaf] = true
		}
	}
	return body
}

// ResolvedNestedYANGInner returns the version-overridden inner
// element name for a nested keyed-list family. Falls back to the
// static default if no override exists.
func ResolvedNestedYANGInner(family, defaultInner string) string {
	o := GetOverride(family)
	if o != nil && o.NestedYANGInnerOverride != "" {
		return o.NestedYANGInnerOverride
	}
	return defaultInner
}

// ResolvedKeyField returns the version-overridden key field for a
// family. Falls back to the static default if no override exists.
func ResolvedKeyField(family, defaultKey string) string {
	o := GetOverride(family)
	if o != nil && o.KeyFieldOverride != "" {
		return o.KeyFieldOverride
	}
	return defaultKey
}

// IsLegacyVersion returns true if the override table has an active
// override entry for the given family, meaning the device runs a
// pre-baseline version that requires version-specific transforms.
// Custom writers use this instead of calling DeviceVersionAtLeast
// directly, keeping the version threshold in the table.
func IsLegacyVersion(family string) bool {
	return GetOverride(family) != nil
}

// ResolvedYANGPath returns the version-overridden YANG path for a
// family. Falls back to the static default if no override exists.
func ResolvedYANGPath(family, defaultPath string) string {
	o := GetOverride(family)
	if o != nil && o.YANGPathOverride != "" {
		return o.YANGPathOverride
	}
	return defaultPath
}

// ResolvedEnvelopeKey returns the version-overridden envelope key
// for a family. Falls back to the static default if no override exists.
func ResolvedEnvelopeKey(family, defaultKey string) string {
	o := GetOverride(family)
	if o != nil && o.EnvelopeKeyOverride != "" {
		return o.EnvelopeKeyOverride
	}
	return defaultKey
}
