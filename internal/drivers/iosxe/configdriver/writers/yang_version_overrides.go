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
// Each device gets an OverrideResolver built from its reported
// software release, and writers query that captured resolver at
// Fetch/Diff time.
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

	// FetchBodyTransform, if non-nil, is applied on the Fetch path
	// AFTER ReverseElementMap. It is the structural inverse of
	// BodyTransform — converting observed YANG body shapes back to
	// netascode canonical shapes for Diff comparison.
	FetchBodyTransform func(body map[string]any) map[string]any

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

// OverrideResolver is the immutable per-device view of the override
// table. It is built once for a device software version and then
// captured by every writer instance returned for that device.
type OverrideResolver struct {
	deviceVersion string
	major         int
	minor         int
	releaseTag    string
	resolved      map[string]*VersionOverride
}

// NewOverrideResolver validates a device-reported IOS-XE software
// version and resolves the matching override entry per family. Empty
// version means "unknown yet" and returns a baseline resolver; callers
// that require version discovery should fail closed before writes run.
func NewOverrideResolver(version string) (*OverrideResolver, error) {
	if version == "" {
		return newOverrideResolverForVersion("", 0, 0, ""), nil
	}
	major, minor, ok := parseVersionStrict(version)
	if !ok {
		return nil, &ErrMalformedDeviceVersion{Version: version}
	}
	releaseTag, mapped := ReleaseTagForDeviceVersion(major, minor)
	if !mapped {
		return nil, &ErrUnsupportedDeviceVersion{Version: version}
	}
	return newOverrideResolverForVersion(version, major, minor, releaseTag), nil
}

// NewOverrideResolverForMajorMinor constructs a resolver for tests
// that want to exercise a raw major.minor pair without going through
// the production device-version support gate.
func NewOverrideResolverForMajorMinor(major, minor int) *OverrideResolver {
	releaseTag, _ := ReleaseTagForDeviceVersion(major, minor)
	return newOverrideResolverForVersion("", major, minor, releaseTag)
}

func newOverrideResolverForVersion(deviceVersion string, major, minor int, releaseTag string) *OverrideResolver {
	resolved := make(map[string]*VersionOverride, len(overrideTable))
	for i := range overrideTable {
		o := &overrideTable[i]
		if o.versionInRange(major, minor) {
			// First match wins per family.
			if _, exists := resolved[o.Family]; !exists {
				resolved[o.Family] = o
			}
		}
	}
	return &OverrideResolver{
		deviceVersion: deviceVersion,
		major:         major,
		minor:         minor,
		releaseTag:    releaseTag,
		resolved:      resolved,
	}
}

func baselineResolver() *OverrideResolver {
	return newOverrideResolverForVersion("", 0, 0, "")
}

func ensureResolver(r *OverrideResolver) *OverrideResolver {
	if r != nil {
		return r
	}
	return baselineResolver()
}

// DeviceVersion returns the device software version used to build
// this resolver. Empty means "unknown/baseline".
func (r *OverrideResolver) DeviceVersion() string {
	if r == nil {
		return ""
	}
	return r.deviceVersion
}

// DeviceVersionAtLeast returns true if this resolver was built for a
// device version >= the supplied major.minor.
func (r *OverrideResolver) DeviceVersionAtLeast(major, minor int) bool {
	if r == nil || r.deviceVersion == "" {
		return false
	}
	if r.major != major {
		return r.major > major
	}
	return r.minor >= minor
}

// ReleaseTag returns the mapped YANG release tag for this resolver's
// device software version. Empty means no mapped release was used.
func (r *OverrideResolver) ReleaseTag() string {
	if r == nil {
		return ""
	}
	return r.releaseTag
}

// GetOverride returns the resolved override for a family.
func (r *OverrideResolver) GetOverride(family string) (*VersionOverride, bool) {
	if r == nil || r.resolved == nil {
		return nil, false
	}
	o, ok := r.resolved[family]
	return o, ok
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
// transforms to an observed body: reverse ElementMap → DecodeEmptyLeaves
// → FetchBodyTransform.
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
	if o.FetchBodyTransform != nil {
		body = o.FetchBodyTransform(body)
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
func (r *OverrideResolver) ResolvedNestedYANGInner(family, defaultInner string) string {
	o, ok := r.GetOverride(family)
	if ok && o.NestedYANGInnerOverride != "" {
		return o.NestedYANGInnerOverride
	}
	return defaultInner
}

// ResolvedKeyField returns the version-overridden key field for a
// family. Falls back to the static default if no override exists.
func (r *OverrideResolver) ResolvedKeyField(family, defaultKey string) string {
	o, ok := r.GetOverride(family)
	if ok && o.KeyFieldOverride != "" {
		return o.KeyFieldOverride
	}
	return defaultKey
}

// IsLegacyVersion returns true if the override table has an active
// override entry for the given family, meaning the device runs a
// pre-baseline version that requires version-specific transforms.
// Custom writers use this instead of calling DeviceVersionAtLeast
// directly, keeping the version threshold in the table.
func (r *OverrideResolver) IsLegacyVersion(family string) bool {
	_, ok := r.GetOverride(family)
	return ok
}

// ResolvedYANGPath returns the version-overridden YANG path for a
// family. Falls back to the static default if no override exists.
func (r *OverrideResolver) ResolvedYANGPath(family, defaultPath string) string {
	o, ok := r.GetOverride(family)
	if ok && o.YANGPathOverride != "" {
		return o.YANGPathOverride
	}
	return defaultPath
}

// ResolvedEnvelopeKey returns the version-overridden envelope key
// for a family. Falls back to the static default if no override exists.
func (r *OverrideResolver) ResolvedEnvelopeKey(family, defaultKey string) string {
	o, ok := r.GetOverride(family)
	if ok && o.EnvelopeKeyOverride != "" {
		return o.EnvelopeKeyOverride
	}
	return defaultKey
}

// AutoReverseObservedBody is the shared Fetch-side counterpart to
// ApplyOverrideToBody. It runs the *auto-reversible* parts of the
// override chain — ReverseElementMap and DecodeEmptyLeaves — so that
// data fetched from a legacy-version device is comparable against
// the netascode-shaped desired body in leavesEqual.
//
// When the family's override carries a BodyTransform, this function
// is a no-op: BodyTransform is intrinsically one-way (no automatic
// inverse) and the writer must implement a manual reverse path. The
// snmp_server and logging writers do exactly that today.
//
// Both ReverseElementMap and DecodeEmptyLeaves are idempotent, so it
// is safe to call this even when the writer also reverses manually
// downstream (e.g. ntp.go calls DecodeEmptyLeaves inside its own
// yangFetchShape; AutoReverseObservedBody runs first and the second
// call is a no-op).
func (r *OverrideResolver) AutoReverseObservedBody(family string, body map[string]any) map[string]any {
	if body == nil {
		return body
	}
	o, ok := r.GetOverride(family)
	if !ok {
		return body
	}
	if o.BodyTransform != nil {
		// BodyTransform is one-way; if FetchBodyTransform is also
		// provided it is the structural inverse and should run here.
		// Otherwise leave the body alone — the writer implements a
		// manual Fetch-side reverse (e.g. snmp_server, logging).
		if o.FetchBodyTransform != nil {
			body = o.FetchBodyTransform(body)
		}
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
