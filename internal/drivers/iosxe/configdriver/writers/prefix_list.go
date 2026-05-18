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

// Prefix-list writer with per-sequence diffing. Shares the nested-
// keyed-list machinery with the ACL writers; only the family-
// specific binding lives here.
//
// netascode:
//   prefix_list:
//     prefixes:
//       - name: DEFAULT-ONLY
//         description: default only
//         sequences:
//           - no: 10
//             action: permit
//             ip: 0.0.0.0/0
//
// On IOS-XE >= 17.18:
//   YANG: /Cisco-IOS-XE-native:native/ip/prefix-list/prefixes
//   Nested: prefixes[name] → seq[no]
//
// On IOS-XE < 17.18 (discovered via CiscoDevNet/terraform-provider-iosxe):
//   YANG: /Cisco-IOS-XE-native:native/ip/prefix-lists
//   Flat: prefixes[name, no] — compound key, no nesting
//   Description lives in separate prefix-list-description[name] list

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

const (
	prefixListFamily = "prefix_list"

	prefixListYANGPath1718    = "/Cisco-IOS-XE-native:native/ip/prefix-list/prefixes"
	prefixListEnvelopeKey1718 = "Cisco-IOS-XE-native:prefixes"

	prefixListYANGPath1716    = "/Cisco-IOS-XE-native:native/ip/prefix-lists"
	prefixListEnvelopeKey1716 = "Cisco-IOS-XE-native:prefix-lists"
)

// nested1718 is the delegate for >= 17.18 (unchanged behavior).
var prefixListNested1718 = nestedKeyedListWriter{
	base: keyedListWriter{
		family:      prefixListFamily,
		yangPath:    prefixListYANGPath1718,
		envelopeKey: prefixListEnvelopeKey1718,
		innerKey:    "prefixes",
		keyField:    "name",
		managedLeaves: []string{
			"description",
			"sequences",
		},
	},
	nestedLeaf:      "sequences",
	nestedKeyField:  "no",
	nestedYANGInner: "seq",
}

func init() {
	Override(prefixListWriter{})
}

type prefixListWriter struct {
	resolver *OverrideResolver
}

func (w prefixListWriter) Family() string { return prefixListFamily }
func (w prefixListWriter) YANGPaths() []string {
	return []string{w.resolverForUse().ResolvedYANGPath(prefixListFamily, prefixListYANGPath1718)}
}

func (w prefixListWriter) withResolver(r *OverrideResolver) SectionWriter {
	w.resolver = r
	return w
}

func (w prefixListWriter) resolverForUse() *OverrideResolver {
	return ensureResolver(w.resolver)
}

func (w prefixListWriter) nested1718() nestedKeyedListWriter {
	n := prefixListNested1718
	n.base.resolver = w.resolverForUse()
	return n
}

// ── Fetch ───────────────────────────────────────────────────────

func (w prefixListWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	if !w.resolverForUse().IsLegacyVersion(prefixListFamily) {
		return w.nested1718().Fetch(ctx, c)
	}
	return w.fetchLegacy(ctx, c)
}

// fetchLegacy reads the flat /ip/prefix-lists container and converts
// it back to the nested netascode shape.
func (w prefixListWriter) fetchLegacy(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, prefixListYANGPath1716)
	if err != nil {
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, prefixListEnvelopeKey1716)
	if err != nil || body == nil {
		return []map[string]any{}, err
	}
	var container map[string]any
	if err := json.Unmarshal(body, &container); err != nil {
		return nil, fmt.Errorf("%s: decode container: %w", prefixListFamily, err)
	}
	return prefixListFromYANG1716(container), nil
}

// ── Diff ────────────────────────────────────────────────────────

func (w prefixListWriter) Diff(desired, observed any) ([]transport.Op, error) {
	if !w.resolverForUse().IsLegacyVersion(prefixListFamily) {
		return w.nested1718().Diff(desired, observed)
	}
	return w.diffLegacy(desired, observed)
}

func (w prefixListWriter) diffLegacy(desired, observed any) ([]transport.Op, error) {
	desiredList, err := w.nested1718().base.coerceBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}
	if prefixListsEqual(desiredList, observedList) {
		return nil, nil
	}
	// Convert the entire desired netascode shape to the 17.16 flat YANG body.
	proj := prefixListToYANG1716(desiredList)
	body, err := wrapYANGPayload(prefixListEnvelopeKey1716, proj)
	if err != nil {
		return nil, err
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: prefixListYANGPath1716,
		Body: body,
	}}, nil
}

// ── Apply ───────────────────────────────────────────────────────

func (w prefixListWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// ── comparison helper ───────────────────────────────────────────

// prefixListsEqual compares two prefix-list slices (both in netascode
// nested form). Each outer entry is matched by name; sequences
// compared as unordered sets.
func prefixListsEqual(desired, observed []map[string]any) bool {
	if len(desired) != len(observed) {
		return false
	}
	obsIdx := map[string]map[string]any{}
	for _, o := range observed {
		k := fmt.Sprintf("%v", o["name"])
		obsIdx[k] = o
	}
	for _, d := range desired {
		k := fmt.Sprintf("%v", d["name"])
		o, ok := obsIdx[k]
		if !ok {
			return false
		}
		if !scalarEqual(d["description"], o["description"]) {
			return false
		}
		seqKey := "sequences"
		if _, ok := d[seqKey]; !ok {
			seqKey = "seqs"
		}
		dSeqs, _ := d[seqKey].([]any)
		oSeqs, _ := o["sequences"].([]any)
		if !rulesEqualUnordered(dSeqs, oSeqs, "no") {
			return false
		}
	}
	return true
}

// rulesEqualUnordered compares two slices of map[string]any as
// unordered sets keyed by keyField.
func rulesEqualUnordered(a, b []any, keyField string) bool {
	if len(a) != len(b) {
		return false
	}
	idx := map[string]map[string]any{}
	for _, item := range b {
		m, ok := item.(map[string]any)
		if !ok {
			return false
		}
		k := fmt.Sprintf("%v", m[keyField])
		idx[k] = m
	}
	for _, item := range a {
		m, ok := item.(map[string]any)
		if !ok {
			return false
		}
		k := fmt.Sprintf("%v", m[keyField])
		bm, ok := idx[k]
		if !ok {
			return false
		}
		// Compare all keys in both maps.
		allKeys := map[string]struct{}{}
		for kk := range m {
			allKeys[kk] = struct{}{}
		}
		for kk := range bm {
			allKeys[kk] = struct{}{}
		}
		for kk := range allKeys {
			if !scalarEqual(m[kk], bm[kk]) {
				return false
			}
		}
	}
	return true
}

// ── 17.16 body transform (desired → YANG flat) ─────────────────

// prefixListToYANG1716 converts the nested netascode shape to the
// flat compound-keyed YANG shape.
//
// Input (netascode):
//
//	[{name: "PL1", description: "...", sequences: [{no: 10, action: "permit", ip: "10.0.0.0/8"}]}]
//
// Output (YANG 17.16):
//
//	{prefixes: [{name: "PL1", no: 10, action: "permit", ip: "10.0.0.0/8"}],
//	 prefix-list-description: [{name: "PL1", description: "..."}]}
func prefixListToYANG1716(entries []map[string]any) map[string]any {
	var flatPrefixes []any
	var descriptions []any
	for _, entry := range entries {
		name, _ := entry["name"]
		if desc, ok := entry["description"]; ok && desc != nil && desc != "" {
			descriptions = append(descriptions, map[string]any{
				"name":        name,
				"description": desc,
			})
		}
		seqKey := "sequences"
		if _, ok := entry[seqKey]; !ok {
			seqKey = "seqs"
		}
		seqs, _ := entry[seqKey].([]any)
		for _, s := range seqs {
			rule, ok := s.(map[string]any)
			if !ok {
				continue
			}
			flat := map[string]any{"name": name}
			for k, v := range rule {
				// Drop nil/false leaves — YAML schemas may
				// inject optional fields with zero-values
				// that would become spurious YANG elements.
				if v == nil || v == false {
					continue
				}
				flat[k] = v
			}
			flatPrefixes = append(flatPrefixes, flat)
		}
	}
	out := map[string]any{}
	if len(flatPrefixes) > 0 {
		out["prefixes"] = flatPrefixes
	}
	if len(descriptions) > 0 {
		out["prefix-list-description"] = descriptions
	}
	return out
}

// ── 17.16 fetch transform (YANG flat → netascode nested) ────────

// prefixListFromYANG1716 converts the flat compound-keyed YANG shape
// back to the nested netascode form.
func prefixListFromYANG1716(container map[string]any) []map[string]any {
	// Index descriptions by name.
	descIdx := map[string]string{}
	if descs, ok := container["prefix-list-description"].([]any); ok {
		for _, d := range descs {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			n := fmt.Sprintf("%v", dm["name"])
			if desc, ok := dm["description"].(string); ok {
				descIdx[n] = desc
			}
		}
	}

	// Group flat prefixes by name.
	type namedSeqs struct {
		order int
		seqs  []any
	}
	grouped := map[string]*namedSeqs{}
	var nameOrder []string
	if prefixes, ok := container["prefixes"].([]any); ok {
		for _, p := range prefixes {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			n := fmt.Sprintf("%v", pm["name"])
			if _, exists := grouped[n]; !exists {
				grouped[n] = &namedSeqs{order: len(nameOrder)}
				nameOrder = append(nameOrder, n)
			}
			// Build the sequence entry (everything except "name").
			seq := map[string]any{}
			for k, v := range pm {
				if k != "name" {
					seq[k] = v
				}
			}
			grouped[n].seqs = append(grouped[n].seqs, seq)
		}
	}

	// Build the netascode nested result.
	result := make([]map[string]any, 0, len(nameOrder))
	for _, n := range nameOrder {
		entry := map[string]any{
			"name":      n,
			"sequences": grouped[n].seqs,
		}
		if desc, ok := descIdx[n]; ok {
			entry["description"] = desc
		}
		result = append(result, entry)
	}
	return result
}
