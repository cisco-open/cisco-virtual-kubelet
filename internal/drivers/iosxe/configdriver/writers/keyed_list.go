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
	"encoding/json"
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
	family         string
	yangPath       string // RESTCONF path of the list
	envelopeKey    string // "<module>:<list-name>"
	innerKey       string // e.g. "vrfs", "interfaces" in netascode nested shape
	keyField       string // element leaf used as identity
	managedLeaves  []string
	extraDesired   func(entry map[string]any) error // optional per-entry validation
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
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("%s: decode list: %w", w.family, err)
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
			return nil, fmt.Errorf("%s: observed: %w", w.family, err)
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
		body, err := wrapYANGPayload(w.envelopeKey, []any{proj})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: w.yangPath + "=" + k,
			Body: body,
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
			return nil, fmt.Errorf("%s: observed: %w", w.family, err)
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
			Verb: transport.VerbDelete,
			Path: w.yangPath + "=" + k,
		})
	}
	return ops, nil
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
