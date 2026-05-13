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

import (
	"context"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// keyedListWriter is the generalised SectionWriter for netascode families
// that wrap a YANG keyed list. Families that match this shape supply
// only a small config struct; the Fetch/Diff/Apply implementation is
// shared.
//
// Shape contract:
//   - Desired intent is either a bare []any of entries or the nested
//     {"<innerKey>": [...]} shape the resolver produces.
//   - Each entry is a map with a `keyField` leaf identifying it.
//   - Observed state is decoded from the device's yang-data+json
//     envelope keyed by `envelopeKey`.
//
// Diff semantics match vlanWriter: additive (no auto-delete), keyed
// lookup by keyField, leaf-equal comparison over `managedLeaves`.
type keyedListWriter struct {
	family        string
	yangPath      string // RESTCONF path of the list
	envelopeKey   string // "<module>:<list-name>"
	innerKey      string // e.g. "vrfs", "interfaces" in netascode nested shape
	keyField      string // element leaf used as identity
	managedLeaves []string
	extraDesired  func(entry map[string]any) error // optional per-entry validation

	// yangBodyShape, if non-nil, transforms a flat managed-leaf entry
	// into the nested Cisco-IOS-XE-native YANG body shape the device
	// expects (e.g. ipv4_address → ip.address.primary.address).
	// Identity transform is the default and matches the families
	// whose YANG model keeps every managed leaf flat.
	yangBodyShape func(flat map[string]any) map[string]any
	// yangFetchShape, if non-nil, lifts the device's nested YANG
	// response back to the flat managed-leaf shape so leavesEqual
	// can compare desired vs observed without per-family branching.
	yangFetchShape func(yang map[string]any) map[string]any
}

func (w keyedListWriter) Family() string      { return w.family }
func (w keyedListWriter) YANGPaths() []string { return []string{w.yangPath} }

func (w keyedListWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, w.yangPath)
	if err != nil {
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, w.envelopeKey)
	if err != nil || body == nil {
		return []map[string]any{}, err
	}
	list, err := decodeYANGList(body)
	if err != nil {
		return nil, fmt.Errorf("%s: decode list: %w", w.family, err)
	}
	if w.yangFetchShape != nil {
		for i := range list {
			list[i] = w.yangFetchShape(list[i])
		}
	}
	return list, nil
}

func (w keyedListWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := w.coerceBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}

	want := map[string]map[string]any{}
	keyOrder := []string{}
	for _, e := range desiredList {
		if w.extraDesired != nil {
			if err := w.extraDesired(e); err != nil {
				return nil, fmt.Errorf("%s: desired: %w", w.family, err)
			}
		}
		k, err := entryKey(e, w.keyField)
		if err != nil {
			return nil, fmt.Errorf("%s: desired: %w", w.family, err)
		}
		if _, dup := want[k]; !dup {
			keyOrder = append(keyOrder, k)
		}
		want[k] = e
	}
	got := map[string]map[string]any{}
	for _, e := range observedList {
		k, err := entryKey(e, w.keyField)
		if err != nil {
			// Devices return list entries that don't fit the
			// modelled key schema (e.g. system-default ACLs that
			// IOS-XE indexes by sequence rather than name). Drop
			// them from the observed map; the desired-side loop
			// only consults `got[k]` to skip equal entries, so
			// unknown observed entries become a "not in device"
			// signal that's safe for additive Diff. Pruning is
			// handled separately in PruneDiff.
			continue
		}
		got[k] = e
	}

	// Deterministic op order: sort by key string so equivalent diffs are
	// byte-equal. keyOrder preserves first-occurrence order only; we use
	// it when keys are non-scalar but for scalars sort.Strings is safer.
	sort.Strings(keyOrder)

	ops := make([]transport.Op, 0, len(keyOrder))
	for _, k := range keyOrder {
		entry := want[k]
		observedEntry, inDevice := got[k]
		if inDevice && leavesEqual(entry, observedEntry, w.managedLeaves) {
			continue
		}
		proj := projectManagedLeaves(entry, w.managedLeaves)
		if kv, ok := entry[w.keyField]; ok {
			proj[w.keyField] = kv
		}
		if w.yangBodyShape != nil {
			proj = w.yangBodyShape(proj)
		}
		body, err := wrapYANGPayload(w.envelopeKey, []any{proj})
		if err != nil {
			return nil, err
		}
		// When the entry doesn't exist on the device, MERGE to
		// the parent list path (no =key suffix). IOS-XE RESTCONF
		// rejects PATCH to a nonexistent entry path with 404; the
		// parent path creates the entry as part of the MERGE.
		// Caught against C8000V 17.16.01a for prefix_list.
		opPath := w.yangPath + "=" + k
		var pathSpec []transport.PathElement
		if inDevice {
			pathSpec = pathSpecForKeyedListEntry(w.yangPath, w.keyField, k)
		} else {
			opPath = w.yangPath
			pathSpec = pathSpecForKeyedListParent(w.yangPath)
		}
		ops = append(ops, transport.Op{
			Verb:     transport.VerbMerge,
			Path:     opPath,
			PathSpec: pathSpec,
			Body:     body,
		})
	}
	return ops, nil
}

func (w keyedListWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// KeysOf implements writers.KeyExtractable. Returns the entry-key
// values in v in deterministic order. v may be the family block
// (e.g. {vlans: [...]}), a bare list, or nil. Entries missing the
// configured keyField are skipped without erroring — matches the
// lenient observed-side handling in Diff/PruneDiff. The result is
// used by the engine to track ownership of device-side entries when
// spec.atomicReplace=true.
func (w keyedListWriter) KeysOf(v any) []string {
	list, err := w.coerceBlock(v, "keysOf")
	if err != nil || len(list) == 0 {
		return nil
	}
	keys := make([]string, 0, len(list))
	for _, e := range list {
		k, err := entryKey(e, w.keyField)
		if err != nil {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// PruneDiff emits a VerbDelete op for every entry present on the
// device but absent from the desired intent. Implements PruneCapable
// — the engine calls this only when spec.pruneOnRelinquish: true on
// the IOSXEConfig.
//
// Path shape mirrors the merge ops produced by Diff: keyedListPath +
// "=" + key. Body is empty because RESTCONF DELETE on a list-entry
// path needs no payload. NETCONF maps VerbDelete to operation=
// "delete" via its edit-config builder, and the path-to-subtree-
// filter conversion handles the per-entry shape.
func (w keyedListWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := w.coerceBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}
	want := map[string]struct{}{}
	for _, e := range desiredList {
		k, err := entryKey(e, w.keyField)
		if err != nil {
			return nil, fmt.Errorf("%s: desired: %w", w.family, err)
		}
		want[k] = struct{}{}
	}
	var orphans []string
	for _, e := range observedList {
		k, err := entryKey(e, w.keyField)
		if err != nil {
			// Skip observed entries that don't fit the modelled
			// key schema; we can't safely prune what we don't
			// recognise. See Diff() for the same rationale.
			continue
		}
		if _, kept := want[k]; kept {
			continue
		}
		orphans = append(orphans, k)
	}
	sort.Strings(orphans)

	ops := make([]transport.Op, 0, len(orphans))
	for _, k := range orphans {
		ops = append(ops, transport.Op{
			Verb:     transport.VerbDelete,
			Path:     w.yangPath + "=" + k,
			PathSpec: pathSpecForKeyedListEntry(w.yangPath, w.keyField, k),
		})
	}
	return ops, nil
}

// pathSpecForKeyedListEntry builds a structured PathSpec for a
// single keyed-list-entry op (e.g. /native/vlan/vlan-list=10).
// Walks the YANG xpath, strips any "module:" prefix per segment,
// and attaches the typed key on the LAST segment with the writer's
// configured keyField name.
//
// Wave 5A-fu (external-review-followup Finding #4): with the typed
// PathSpec on every keyed-list op, gNMI Set/Delete works correctly
// for keys whose values contain '/' (interface names like
// "GigabitEthernet 0/0/0"). The legacy string Path is preserved
// for RESTCONF and NETCONF; only gNMI consults PathSpec.
func pathSpecForKeyedListEntry(yangPath, keyField, keyValue string) []transport.PathElement {
	segments := splitYANGPathSegments(yangPath)
	if len(segments) == 0 {
		return nil
	}
	out := make([]transport.PathElement, len(segments))
	for i, seg := range segments {
		out[i] = transport.PathElement{Name: seg}
	}
	out[len(out)-1].Keys = map[string]string{keyField: keyValue}
	return out
}

// pathSpecForKeyedListParent builds a structured PathSpec for the
// parent list path (no key). Used when creating new entries via
// MERGE to the list path rather than to a specific entry path.
func pathSpecForKeyedListParent(yangPath string) []transport.PathElement {
	segments := splitYANGPathSegments(yangPath)
	if len(segments) == 0 {
		return nil
	}
	out := make([]transport.PathElement, len(segments))
	for i, seg := range segments {
		out[i] = transport.PathElement{Name: seg}
	}
	return out
}

// splitYANGPathSegments splits a YANG xpath into its segments,
// stripping any "module:" prefix per segment. Mirrors the
// normalisation transport.LastPathSegment applies but keeps the
// full segment list. Empty paths and pure-/ paths return nil.
func splitYANGPathSegments(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	// Trim a single leading slash; preserve internal "//" as
	// empty segments (defensive — netascode paths shouldn't
	// produce them, but if they do the caller sees the original
	// shape).
	if p[0] == '/' {
		p = p[1:]
	}
	out := make([]string, 0, 4)
	for _, raw := range splitPath(p) {
		if raw == "" {
			continue
		}
		seg := raw
		if i := indexByte(seg, ':'); i > 0 {
			seg = seg[i+1:]
		}
		out = append(out, seg)
	}
	return out
}

// splitPath splits on '/' without involving the strings package
// import — the writers package keeps its dependencies tight.
func splitPath(s string) []string {
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// indexByte locates b in s; returns -1 if absent.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// pathSpecForInterface builds a structured PathSpec for an
// interface keyed by name. Walks "/Cisco-IOS-XE-native:native/
// interface/<Type>" and attaches Keys{"name": name} on the final
// segment. Used by handwritten interface writers
// (interface_ethernet, interface_loopback, interface_tunnel,
// interface_port_channel, interface_vlan, interface_virtual_port_group)
// so gNMI Set/Delete preserves the interface name verbatim — even
// when it contains '/' (e.g. "0/0/0").
//
// Wave 7B (external-review-next-actions Finding #5). Pre-fix the
// handwritten writers emitted only string Path; the gNMI fallback
// parseGNMIPath split the path on '/' and the lab case
// GigabitEthernet=0/0/0 produced wrong gNMI elements.
func pathSpecForInterface(ifaceType, name string) []transport.PathElement {
	return []transport.PathElement{
		{Name: "native"},
		{Name: "interface"},
		{Name: ifaceType, Keys: map[string]string{"name": name}},
	}
}

// pathSpecForInterfaceChild adds a trailing child container after
// the keyed interface segment — the shape used by
// interface_switchport (".../interface/<Type>=<name>/switchport").
func pathSpecForInterfaceChild(ifaceType, name, child string) []transport.PathElement {
	return []transport.PathElement{
		{Name: "native"},
		{Name: "interface"},
		{Name: ifaceType, Keys: map[string]string{"name": name}},
		{Name: child},
	}
}

// coerceBlock accepts the family-level container (resolver shape) or a
// bare list. The resolver emits configuration[family] which is
// typically a map with innerKey → list; tests sometimes hand in the
// list directly.
func (w keyedListWriter) coerceBlock(v any, origin string) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m[w.innerKey]; ok {
			return coerceList(inner, origin+"."+w.innerKey)
		}
		return nil, nil
	}
	return coerceList(v, origin)
}

// entryKey returns the URL-safe stringified form of an entry's key
// field. Numbers, strings, and bools are accepted; other types error.
func entryKey(e map[string]any, field string) (string, error) {
	raw, ok := e[field]
	if !ok {
		return "", fmt.Errorf("entry missing %q", field)
	}
	switch tv := raw.(type) {
	case string:
		return tv, nil
	case bool:
		return fmt.Sprintf("%t", tv), nil
	case float64:
		return fmt.Sprintf("%g", tv), nil
	case int, int64:
		return fmt.Sprintf("%d", tv), nil
	default:
		return "", fmt.Errorf("entry key %v has unsupported type %T", raw, raw)
	}
}
