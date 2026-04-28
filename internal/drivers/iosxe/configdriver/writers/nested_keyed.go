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

// nestedKeyedListWriter is the generalised SectionWriter for
// netascode families whose intent is a keyed list of outer entries
// (the existing keyedListWriter shape) where one of the managed
// leaves is itself a keyed list of rules — ACL ACEs, prefix-list
// sequences, route-map entries, BGP neighbors, OSPF networks, EIGRP
// networks, policy-map class actions.
//
// The Phase-1 keyedListWriter handled this surface by treating the
// inner list as one opaque leaf, so a single ACE / sequence / entry
// edit forced the whole list back through Apply. Phase-4 keys the
// inner list by its own field (sequence, name, network, etc.) and
// emits a merge body containing only the changed members. RESTCONF
// MERGE on the outer path leaves untouched-key inner members alone,
// so the device wire matches operator intuition: a one-line edit is
// a one-line op.
//
// Equality on the inner list is orderless (a device that returns
// rules in a different order than the intent listed them no longer
// triggers spurious drift), and equivalent rule sets emit zero ops
// for that outer entry.
//
// The wrapper delegates Family/YANGPaths/Fetch/Apply/PruneDiff to
// the embedded base, which already implements them — only Diff is
// rewritten.

import (
	"context"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// nestedListSpec describes one managed leaf whose value is itself a
// keyed list. Families with multiple such leaves (OSPF processes
// have a `network` list and an `area` list, etc.) supply a slice of
// these.
type nestedListSpec struct {
	// Leaf is the netascode managed-leaf name (e.g. "rules",
	// "sequences", "network").
	Leaf string

	// KeyField is the inner key — sequence number, prefix, area-id.
	// Stringified via fmt.Sprintf so numeric and string keys both
	// work.
	KeyField string

	// YANGInner is the YANG-decoded inner-list name. Set when the
	// device wraps the inner list under a per-family YANG
	// container (e.g. "access-list-seq-rule"). Leave empty when
	// the device returns the bare slice; the leaf name is then
	// the default fallback.
	YANGInner string
}

// nestedKeyedListWriter wraps keyedListWriter and adds per-inner-key
// diffing for one or more managed leaves. The outer entry's other
// managed leaves are still compared whole.
type nestedKeyedListWriter struct {
	base keyedListWriter

	// nested lists the leaves that participate in per-element
	// diffing. The shorthand fields below are kept so existing
	// initializers don't all have to migrate at once; either form
	// is accepted, with the slice form taking precedence when
	// non-nil.
	nested []nestedListSpec

	nestedLeaf      string // alias for one-spec usage
	nestedKeyField  string // alias for one-spec usage
	nestedYANGInner string // alias for one-spec usage
}

// specs returns the nested-leaf specifications normalized into the
// slice form. Single-spec callers populate the alias fields; multi-
// spec callers populate the slice. Either path produces the same
// shape.
func (w nestedKeyedListWriter) specs() []nestedListSpec {
	if len(w.nested) > 0 {
		return w.nested
	}
	if w.nestedLeaf == "" {
		return nil
	}
	return []nestedListSpec{{
		Leaf:      w.nestedLeaf,
		KeyField:  w.nestedKeyField,
		YANGInner: w.nestedYANGInner,
	}}
}

func (w nestedKeyedListWriter) Family() string      { return w.base.Family() }
func (w nestedKeyedListWriter) YANGPaths() []string { return w.base.YANGPaths() }

func (w nestedKeyedListWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	return w.base.Fetch(ctx, c)
}

func (w nestedKeyedListWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	return w.base.Apply(ctx, c, ops)
}

// PruneDiff covers both axes of pruning for a nested-keyed family:
//
//   - outer-key prune: whole entries (ACL by name, route-map by
//     name, OSPF process by id) the device has but the intent
//     doesn't. Same shape the keyedListWriter base produces; we
//     reuse that exact code path so the op format matches.
//
//   - inner-key prune: rules / sequences / networks present on a
//     device-side outer entry that the intent's same outer entry
//     no longer references. These can't always be expressed as
//     standalone DELETE ops because YANG container shapes vary
//     per family — instead we emit a REPLACE op containing the
//     full desired inner list. RESTCONF PUT (VerbReplace) on the
//     outer-entry path replaces the inner list verbatim, so the
//     orphan rules drop. The body is the desired inner list, not
//     a partial one, because a REPLACE is by definition the new
//     authoritative state.
//
// REPLACE-on-outer-entry has higher blast radius than the per-rule
// MERGE Diff produces, so we only emit it under spec.pruneOn-
// Relinquish: true and only for entries where there's actually an
// inner-orphan to drop. Equivalent (no-orphan) entries pass
// through untouched.
func (w nestedKeyedListWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	specs := w.specs()
	if len(specs) == 0 {
		return w.base.PruneDiff(desired, observed)
	}

	outerOps, err := w.base.PruneDiff(desired, observed)
	if err != nil {
		return nil, err
	}

	desiredList, err := w.base.coerceBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}

	want := map[string]map[string]any{}
	for _, e := range desiredList {
		k, err := entryKey(e, w.base.keyField)
		if err != nil {
			return nil, fmt.Errorf("%s: desired: %w", w.base.family, err)
		}
		want[k] = e
	}
	got := map[string]map[string]any{}
	keyOrder := []string{}
	for _, e := range observedList {
		k, err := entryKey(e, w.base.keyField)
		if err != nil {
			// Skip observed entries we can't recognise; we
			// can't safely prune what we don't model.
			continue
		}
		if _, dup := got[k]; !dup {
			keyOrder = append(keyOrder, k)
		}
		got[k] = e
	}
	sort.Strings(keyOrder)

	var innerOps []transport.Op
	for _, k := range keyOrder {
		desiredEntry, kept := want[k]
		if !kept {
			// Whole outer entry is being pruned — outerOps already
			// covers it.
			continue
		}
		observedEntry := got[k]
		hasOrphan := false
		body := projectManagedLeavesExcept(desiredEntry, w.base.managedLeaves)
		if kv, ok := desiredEntry[w.base.keyField]; ok {
			body[w.base.keyField] = kv
		}
		for _, spec := range specs {
			desiredInner := indexNested(desiredEntry[spec.Leaf], spec)
			observedInner := indexNested(observedEntry[spec.Leaf], spec)
			if !innerHasOrphans(desiredInner, observedInner) {
				continue
			}
			hasOrphan = true
			// Body carries the full desired inner list — REPLACE
			// is authoritative.
			body[spec.Leaf] = desiredInnerSlice(desiredInner)
		}
		if !hasOrphan {
			continue
		}
		payload, err := wrapYANGPayload(w.base.envelopeKey, []any{body})
		if err != nil {
			return nil, err
		}
		innerOps = append(innerOps, transport.Op{
			Verb:     transport.VerbReplace,
			Path:     w.base.yangPath + "=" + k,
			PathSpec: pathSpecForKeyedListEntry(w.base.yangPath, w.base.keyField, k),
			Body:     payload,
		})
	}
	return append(outerOps, innerOps...), nil
}

func innerHasOrphans(want, have map[string]map[string]any) bool {
	for k := range have {
		if _, kept := want[k]; !kept {
			return true
		}
	}
	return false
}

// desiredInnerSlice serialises the desired inner map back to a
// stable, key-sorted list — the shape REPLACE expects.
func desiredInnerSlice(m map[string]map[string]any) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

func (w nestedKeyedListWriter) Diff(desired, observed any) ([]transport.Op, error) {
	specs := w.specs()
	if len(specs) == 0 {
		// Degenerate case — fall back to plain keyed-list diffing
		// so callers that misconfigured the wrapper still produce
		// a meaningful op stream rather than an empty one.
		return w.base.Diff(desired, observed)
	}

	desiredList, err := w.base.coerceBlock(desired, "desired")
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
		k, err := entryKey(e, w.base.keyField)
		if err != nil {
			return nil, fmt.Errorf("%s: desired: %w", w.base.family, err)
		}
		if _, dup := want[k]; !dup {
			keyOrder = append(keyOrder, k)
		}
		want[k] = e
	}
	got := map[string]map[string]any{}
	for _, e := range observedList {
		k, err := entryKey(e, w.base.keyField)
		if err != nil {
			// Skip observed entries whose key field is missing —
			// IOS-XE returns system ACLs and other defaults that
			// don't fit the modelled schema. See keyedListWriter
			// Diff for the same rationale.
			continue
		}
		got[k] = e
	}
	sort.Strings(keyOrder)

	nestedNames := make([]string, 0, len(specs))
	specByLeaf := make(map[string]nestedListSpec, len(specs))
	for _, s := range specs {
		nestedNames = append(nestedNames, s.Leaf)
		specByLeaf[s.Leaf] = s
	}

	var ops []transport.Op
	for _, k := range keyOrder {
		desiredEntry := want[k]
		observedEntry, inDevice := got[k]

		// Build the per-leaf changed-set for every nested leaf.
		changedByLeaf := map[string][]any{}
		for _, spec := range specs {
			desiredInner := indexNested(desiredEntry[spec.Leaf], spec)
			var observedInner map[string]map[string]any
			if inDevice {
				observedInner = indexNested(observedEntry[spec.Leaf], spec)
			}
			c := changedNested(desiredInner, observedInner)
			if len(c) > 0 {
				changedByLeaf[spec.Leaf] = c
			}
		}

		// Outside the nested leaves we still honour managedLeaves
		// equality so a label-only edit on (say) a route-map
		// entry's description still triggers an op.
		outerEqual := inDevice && leavesEqualExcept(desiredEntry, observedEntry, w.base.managedLeaves, nestedNames...)
		if outerEqual && len(changedByLeaf) == 0 {
			continue
		}
		body := projectManagedLeavesExcept(desiredEntry, w.base.managedLeaves, nestedNames...)
		if kv, ok := desiredEntry[w.base.keyField]; ok {
			body[w.base.keyField] = kv
		}
		for leaf, changed := range changedByLeaf {
			// The netascode-side leaf name (e.g. "rules") is a
			// logical grouping the device's YANG model doesn't
			// represent: the IOS-XE-acl `extended` entry holds
			// the rule list under <access-list-seq-rule> directly,
			// not under an intermediate <rules> container. When
			// YANGInner is set, emit the inner list under the
			// YANG name and drop the netascode leaf entirely.
			// Caught against the live Cat9300 retest of test 08
			// (2026-04-28) where the device rejected
			// `unknown-element <bad-element>rules</bad-element>`.
			if spec, ok := specByLeaf[leaf]; ok && spec.YANGInner != "" {
				body[spec.YANGInner] = changed
			} else {
				body[leaf] = changed
			}
		}
		payload, err := wrapYANGPayload(w.base.envelopeKey, []any{body})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb:     transport.VerbMerge,
			Path:     w.base.yangPath + "=" + k,
			PathSpec: pathSpecForKeyedListEntry(w.base.yangPath, w.base.keyField, k),
			Body:     payload,
		})
	}
	return ops, nil
}

// indexNested returns a key→entry map for one nested-leaf value.
// Accepts the netascode list shape and the YANG-decoded mapped
// shape; entries without the configured key field are skipped.
func indexNested(v any, spec nestedListSpec) map[string]map[string]any {
	out := map[string]map[string]any{}
	entries := coerceInner(v, spec)
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		key, ok := m[spec.KeyField]
		if !ok {
			continue
		}
		out[fmt.Sprintf("%v", key)] = m
	}
	return out
}

// coerceInner accepts both the netascode list shape (`rules: [...]`)
// and the device-decoded mapped shape (`rules: {<inner>: [...]}`).
// Inner-list YANG names vary per family — the spec carries the
// expected name and falls back to the netascode leaf when none is
// provided.
func coerceInner(v any, spec nestedListSpec) []any {
	switch tv := v.(type) {
	case nil:
		return nil
	case []any:
		return tv
	case map[string]any:
		candidates := []string{spec.YANGInner, spec.Leaf}
		for _, name := range candidates {
			if name == "" {
				continue
			}
			if list, ok := tv[name].([]any); ok {
				return list
			}
		}
	}
	return nil
}

// changedNested returns the desired entries that are missing from
// observed or differ leaf-for-leaf — sorted by stringified key for
// byte-stable output.
func changedNested(want, have map[string]map[string]any) []any {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		wantInner := want[k]
		if haveInner, ok := have[k]; ok && rulesEqual(wantInner, haveInner) {
			continue
		}
		out = append(out, wantInner)
	}
	return out
}

// rulesEqual compares two inner-keyed entries leaf-for-leaf,
// ignoring extra leaves the device exposed but the operator didn't
// model. Same allowlist-by-author rule applied per-rule that ACLs
// already use; rule-shapes are too varied to keep a writer-baked
// allowlist.
func rulesEqual(want, have map[string]any) bool {
	for k, wv := range want {
		hv, ok := have[k]
		if !ok || !scalarEqual(wv, hv) {
			return false
		}
	}
	return true
}

// leavesEqualExcept compares the subset of managed leaves *except*
// the named ones. Used by nestedKeyedListWriter to decide whether
// the outer-entry merge body needs the non-nested leaves at all.
func leavesEqualExcept(desired, observed map[string]any, managed []string, except ...string) bool {
	skip := map[string]struct{}{}
	for _, e := range except {
		skip[e] = struct{}{}
	}
	for _, key := range managed {
		if _, drop := skip[key]; drop {
			continue
		}
		dv, dHas := desired[key]
		ov, oHas := observed[key]
		if dHas != oHas {
			return false
		}
		if !dHas {
			continue
		}
		if !scalarEqual(dv, ov) {
			return false
		}
	}
	return true
}

// projectManagedLeavesExcept is projectManagedLeaves with one or
// more named leaves left out. The nested leaves are filled in
// separately by the caller after pruning to their changed subset.
func projectManagedLeavesExcept(src map[string]any, managed []string, except ...string) map[string]any {
	skip := map[string]struct{}{}
	for _, e := range except {
		skip[e] = struct{}{}
	}
	out := map[string]any{}
	for _, key := range managed {
		if _, drop := skip[key]; drop {
			continue
		}
		if v, ok := src[key]; ok {
			out[key] = v
		}
	}
	return out
}
