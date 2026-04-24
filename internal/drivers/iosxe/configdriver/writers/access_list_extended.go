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

// Extended access-list writer with per-rule diffing.
//
// netascode shape:
//
//   access_list_extended:
//     extended:
//       - name: IOX-INGRESS
//         rules:
//           - sequence: 10
//             action: permit
//             protocol: ip
//             src_any: true
//             dst_any: true
//
// YANG path: /Cisco-IOS-XE-native:native/ip/access-list/extended
//
// Phase-1 treated each ACL's `rules` as one opaque managed leaf, so a
// single ACE change forced a re-push of every line in the ACL — slow
// for large ACLs and hostile to commit-log review. Phase-4 keys the
// inner rules list by `sequence` and emits a merge op containing only
// the rules that are new or changed (RESTCONF MERGE on the outer
// path leaves untouched-sequence rules alone). Equivalent ACLs no
// longer trigger spurious drift, and a single ACE edit produces a
// one-rule body instead of a hundred-rule one.
//
// Rule deletion is handled by the engine's PruneOnRelinquish path —
// rules in the device but not in intent surface as residual drift
// today; under spec.pruneOnRelinquish: true an explicit DELETE will
// follow when per-rule prune is wired (Phase-4.5).

import (
	"context"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

const (
	aclExtFamily      = "access_list_extended"
	aclExtPath        = "/Cisco-IOS-XE-native:native/ip/access-list/extended"
	aclExtEnvelopeKey = "Cisco-IOS-XE-acl:extended"
	aclExtInnerKey    = "extended"
	aclExtKeyField    = "name"
	aclExtRuleKey     = "sequence"
)

func init() {
	Override(extendedACLWriter{
		base: keyedListWriter{
			family:        aclExtFamily,
			yangPath:      aclExtPath,
			envelopeKey:   aclExtEnvelopeKey,
			innerKey:      aclExtInnerKey,
			keyField:      aclExtKeyField,
			managedLeaves: []string{"rules"},
		},
	})
}

// extendedACLWriter wraps keyedListWriter so Family/YANGPaths/Fetch/
// Apply/PruneDiff stay shared with every other keyed-list family.
// Only Diff is overridden — that's where per-rule diffing lives.
type extendedACLWriter struct {
	base keyedListWriter
}

func (w extendedACLWriter) Family() string      { return w.base.Family() }
func (w extendedACLWriter) YANGPaths() []string { return w.base.YANGPaths() }
func (w extendedACLWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	return w.base.Fetch(ctx, c)
}
func (w extendedACLWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	return w.base.Apply(ctx, c, ops)
}

// PruneDiff delegates to the base keyedListWriter, which handles the
// per-ACL-name (outer-key) prune. Per-rule prune is a follow-up.
func (w extendedACLWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	return w.base.PruneDiff(desired, observed)
}

func (w extendedACLWriter) Diff(desired, observed any) ([]transport.Op, error) {
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
		k, err := entryKey(e, aclExtKeyField)
		if err != nil {
			return nil, fmt.Errorf("%s: desired: %w", aclExtFamily, err)
		}
		if _, dup := want[k]; !dup {
			keyOrder = append(keyOrder, k)
		}
		want[k] = e
	}
	got := map[string]map[string]any{}
	for _, e := range observedList {
		k, err := entryKey(e, aclExtKeyField)
		if err != nil {
			return nil, fmt.Errorf("%s: observed: %w", aclExtFamily, err)
		}
		got[k] = e
	}
	sort.Strings(keyOrder)

	var ops []transport.Op
	for _, name := range keyOrder {
		desiredACL := want[name]
		observedACL, inDevice := got[name]

		desiredRules := indexRulesBySequence(desiredACL["rules"])
		var observedRules map[string]map[string]any
		if inDevice {
			observedRules = indexRulesBySequence(observedACL["rules"])
		}
		// changed = rules in desired that are absent or different on
		// observed. Equal rules are dropped from the merge body so a
		// stable ACL produces no traffic at all (the outer if-blank
		// continue skips it entirely).
		changed := changedRules(desiredRules, observedRules)
		if inDevice && len(changed) == 0 {
			continue
		}
		body := map[string]any{aclExtKeyField: name}
		if len(changed) > 0 {
			body["rules"] = changed
		}
		payload, err := wrapYANGPayload(aclExtEnvelopeKey, []any{body})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: aclExtPath + "=" + name,
			Body: payload,
		})
	}
	return ops, nil
}

// indexRulesBySequence accepts the netascode list shape (`rules: [...]`)
// or the YANG-decoded mapped shape (`rules: {access-list-seq-rule: [...]}`)
// and returns a sequence-keyed map. Entries without a sequence are
// skipped — Phase-1 already required sequence on the desired side, and
// any device-side rule without one is unmatchable.
func indexRulesBySequence(v any) map[string]map[string]any {
	out := map[string]map[string]any{}
	var entries []any
	switch tv := v.(type) {
	case nil:
		return out
	case []any:
		entries = tv
	case map[string]any:
		if inner, ok := tv["access-list-seq-rule"].([]any); ok {
			entries = inner
		} else {
			return out
		}
	default:
		return out
	}
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		seq, ok := m[aclExtRuleKey]
		if !ok {
			continue
		}
		out[fmt.Sprintf("%v", seq)] = m
	}
	return out
}

// changedRules returns the desired rules that are absent on observed
// or differ leaf-for-leaf. The result is a list (preserves
// netascode's `rules: [...]` shape on the merge body) sorted by
// sequence so equivalent diffs are byte-equal.
func changedRules(want, have map[string]map[string]any) []any {
	seqs := make([]string, 0, len(want))
	for s := range want {
		seqs = append(seqs, s)
	}
	sort.Strings(seqs)
	out := make([]any, 0, len(seqs))
	for _, s := range seqs {
		wRule := want[s]
		if hRule, ok := have[s]; ok && rulesEqual(wRule, hRule) {
			continue
		}
		out = append(out, wRule)
	}
	return out
}

// rulesEqual compares two rule maps leaf-for-leaf, ignoring extra
// leaves on the device that the desired rule doesn't model. Same
// shape contract as leavesEqual but managed-leaves is "every leaf
// declared on the desired rule" — operators control the surface by
// what they author, not by a writer-baked allowlist.
func rulesEqual(want, have map[string]any) bool {
	for k, wv := range want {
		hv, ok := have[k]
		if !ok || !scalarEqual(wv, hv) {
			return false
		}
	}
	return true
}
