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
	"encoding/json"
	"strings"
	"testing"
)

// mkRule is a tiny helper so tests don't have to repeat the
// boilerplate map literal. Sequence is required; the rest is the
// per-test variation.
func mkRule(seq int, extras map[string]any) map[string]any {
	out := map[string]any{"sequence": seq}
	for k, v := range extras {
		out[k] = v
	}
	return out
}

func extACLDesired(rules ...any) map[string]any {
	return map[string]any{
		"extended": []any{
			map[string]any{
				"name":  "INGRESS",
				"rules": rules,
			},
		},
	}
}

func TestACLExtendedDiffNoChangeWhenRulesEqual(t *testing.T) {
	t.Parallel()
	w := nestedKeyedListWriter{
		base: keyedListWriter{
			family: aclExtFamily, yangPath: aclExtPath,
			envelopeKey: aclExtEnvelopeKey, innerKey: aclExtInnerKey,
			keyField: aclExtKeyField, managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
	}
	desired := extACLDesired(
		mkRule(10, map[string]any{"action": "permit"}),
		mkRule(20, map[string]any{"action": "deny"}),
	)
	observed := []map[string]any{{
		"name": "INGRESS",
		"rules": []any{
			mkRule(10, map[string]any{"action": "permit"}),
			mkRule(20, map[string]any{"action": "deny"}),
		},
	}}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (equivalent ACLs)", len(ops))
	}
}

func TestACLExtendedDiffEmitsOnlyChangedRule(t *testing.T) {
	t.Parallel()
	// 100-rule ACL with a single ACE edited at sequence 50. The
	// merge body must contain only sequence 50, not all 100 — that's
	// the whole point of per-rule diffing.
	w := nestedKeyedListWriter{
		base: keyedListWriter{
			family: aclExtFamily, yangPath: aclExtPath,
			envelopeKey: aclExtEnvelopeKey, innerKey: aclExtInnerKey,
			keyField: aclExtKeyField, managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
	}
	const total = 100
	desiredRules := make([]any, 0, total)
	observedRules := make([]any, 0, total)
	for i := 1; i <= total; i++ {
		seq := i * 10
		dRule := mkRule(seq, map[string]any{"action": "permit"})
		oRule := mkRule(seq, map[string]any{"action": "permit"})
		if seq == 500 {
			dRule["action"] = "deny" // the operator's edit
		}
		desiredRules = append(desiredRules, dRule)
		observedRules = append(observedRules, oRule)
	}
	desired := extACLDesired(desiredRules...)
	observed := []map[string]any{{
		"name": "INGRESS", "rules": observedRules,
	}}

	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	body := decodeBody(t, ops[0].Body)
	rules := pickRules(t, body)
	if len(rules) != 1 {
		t.Fatalf("got %d changed rules in body, want 1 (only sequence 500)", len(rules))
	}
	if r, _ := rules[0].(map[string]any); fmtAny(r["sequence"]) != "500" {
		t.Errorf("changed rule has sequence %v, want 500", r["sequence"])
	}
}

func TestACLExtendedDiffRulesEqualOrderless(t *testing.T) {
	t.Parallel()
	// A device that returns rules in sequence-sorted order and a
	// hand-written intent in author-order (10, 30, 20) must compare
	// as equal — without per-rule keying, the byte-level compare in
	// the old leavesEqual would have flagged this as drift.
	w := nestedKeyedListWriter{
		base: keyedListWriter{
			family: aclExtFamily, yangPath: aclExtPath,
			envelopeKey: aclExtEnvelopeKey, innerKey: aclExtInnerKey,
			keyField: aclExtKeyField, managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
	}
	desired := extACLDesired(
		mkRule(10, nil), mkRule(30, nil), mkRule(20, nil),
	)
	observed := []map[string]any{{
		"name": "INGRESS",
		"rules": []any{
			mkRule(10, nil), mkRule(20, nil), mkRule(30, nil),
		},
	}}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (orderless equality)", len(ops))
	}
}

func TestACLExtendedPruneDiffEmitsReplaceForInnerOrphan(t *testing.T) {
	t.Parallel()
	// pruneOnRelinquish=true semantics for nested keyed lists:
	// the engine asks PruneDiff for orphans on both axes. The
	// outer ACL is in both intent and device, but the device has
	// an extra rule (sequence 30) the intent dropped. Without
	// per-rule prune the rule lives forever; with it, we emit a
	// REPLACE on the outer ACL whose body is the desired-only
	// rule list.
	w := nestedKeyedListWriter{
		base: keyedListWriter{
			family: aclExtFamily, yangPath: aclExtPath,
			envelopeKey: aclExtEnvelopeKey, innerKey: aclExtInnerKey,
			keyField: aclExtKeyField, managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
	}
	desired := extACLDesired(mkRule(10, nil), mkRule(20, nil))
	observed := []map[string]any{{
		"name": "INGRESS",
		"rules": []any{
			mkRule(10, nil), mkRule(20, nil), mkRule(30, nil),
		},
	}}
	ops, err := w.PruneDiff(desired, observed)
	if err != nil {
		t.Fatalf("PruneDiff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1 (one REPLACE for the inner-orphan ACL)", len(ops))
	}
	if ops[0].Verb != "REPLACE" {
		t.Errorf("verb=%q, want REPLACE (inner-prune is authoritative)", ops[0].Verb)
	}
	rules := pickRules(t, decodeBody(t, ops[0].Body))
	if len(rules) != 2 {
		t.Errorf("REPLACE body carried %d rules, want 2 (the desired set without sequence 30)", len(rules))
	}
}

func TestACLExtendedPruneDiffNoOpWhenNoOrphans(t *testing.T) {
	t.Parallel()
	// Inner sets are equivalent ⇒ no inner-prune op. The base
	// keyedListWriter still emits whole-ACL prune for outer
	// orphans; that's covered by the keyed-list tests.
	w := nestedKeyedListWriter{
		base: keyedListWriter{
			family: aclExtFamily, yangPath: aclExtPath,
			envelopeKey: aclExtEnvelopeKey, innerKey: aclExtInnerKey,
			keyField: aclExtKeyField, managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
	}
	desired := extACLDesired(mkRule(10, nil), mkRule(20, nil))
	observed := []map[string]any{{
		"name": "INGRESS",
		"rules": []any{
			mkRule(10, nil), mkRule(20, nil),
		},
	}}
	ops, err := w.PruneDiff(desired, observed)
	if err != nil {
		t.Fatalf("PruneDiff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (no orphans)", len(ops))
	}
}

func TestACLExtendedDiffNewACLEmitsAllRules(t *testing.T) {
	t.Parallel()
	// A brand-new ACL on the device side — every desired rule
	// counts as changed because there's no observed rule to match.
	w := nestedKeyedListWriter{
		base: keyedListWriter{
			family: aclExtFamily, yangPath: aclExtPath,
			envelopeKey: aclExtEnvelopeKey, innerKey: aclExtInnerKey,
			keyField: aclExtKeyField, managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
	}
	desired := extACLDesired(mkRule(10, nil), mkRule(20, nil))
	ops, err := w.Diff(desired, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	rules := pickRules(t, decodeBody(t, ops[0].Body))
	if len(rules) != 2 {
		t.Errorf("new ACL emitted %d rules, want 2", len(rules))
	}
}

// decodeBody pulls the JSON body out of a transport.Op so tests can
// inspect what would land on the wire.
func decodeBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

// pickRules walks the {envelope: [{name,rules:[...]}]} body and
// returns the rules slice for the first ACL.
func pickRules(t *testing.T, body map[string]any) []any {
	t.Helper()
	envelope, ok := body[aclExtEnvelopeKey].([]any)
	if !ok || len(envelope) == 0 {
		t.Fatalf("body envelope missing or empty: %#v", body)
	}
	first, ok := envelope[0].(map[string]any)
	if !ok {
		t.Fatalf("envelope entry is not a map: %#v", envelope[0])
	}
	// IOS-XE-acl emits <access-list-seq-rule> directly under the
	// <extended> entry — there is no intermediate <rules> wrapper in
	// the YANG model. When YANGInner is set the writer drops the
	// netascode leaf and uses the YANG name directly. Tests that
	// pre-date the unwrap may still see the bare-list shape via the
	// "rules" fallback below.
	if inner, ok := first["access-list-seq-rule"].([]any); ok {
		return inner
	}
	switch r := first["rules"].(type) {
	case []any:
		return r
	case map[string]any:
		if inner, ok := r["access-list-seq-rule"].([]any); ok {
			return inner
		}
	}
	return nil
}

// fmtAny is the same fmt.Sprintf("%v") shape the writer uses for
// sequence stringification, so test comparisons match the writer's
// internal keying.
func fmtAny(v any) string {
	return strings.TrimSpace(stringValue(v))
}

func stringValue(v any) string {
	switch tv := v.(type) {
	case string:
		return tv
	case int:
		return jsonNumber(tv)
	case float64:
		return jsonNumber(int(tv))
	default:
		return ""
	}
}

func jsonNumber(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestACLRuleToYANG locks in the netascode → IOS-XE-acl flat YANG
// translation. The mapping has to be byte-stable because the netconf
// transport's xml emitter uses the field names as XML element names;
// any drift here re-introduces the live-retest 2026-04-28 failure
// `unknown-element <bad-element>src_host</bad-element>`.
func TestACLRuleToYANG(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "deny-host-to-any-with-protocol",
			in: map[string]any{
				"sequence": 10, "action": "deny",
				"protocol": "ip",
				"src_host": "10.90.95.15", "dst_any": true,
			},
			want: map[string]any{
				"sequence": 10,
				"ace-rule": map[string]any{
					"action":       "deny",
					"protocol":     "ip",
					"host-address": "10.90.95.15",
					"dst-any":      []any{nil},
				},
			},
		},
		{
			name: "permit-any-to-any",
			in: map[string]any{
				"sequence": 20, "action": "permit",
				"protocol": "ip",
				"src_any":  true, "dst_any": true,
			},
			want: map[string]any{
				"sequence": 20,
				"ace-rule": map[string]any{
					"action":   "permit",
					"protocol": "ip",
					"any":      []any{nil},
					"dst-any":  []any{nil},
				},
			},
		},
		{
			name: "src-network-with-wildcard",
			in: map[string]any{
				"sequence": 30, "action": "permit", "protocol": "ip",
				"src_prefix": "10.0.0.0", "src_wildcard": "0.0.0.255",
				"dst_any": true,
			},
			want: map[string]any{
				"sequence": 30,
				"ace-rule": map[string]any{
					"action":       "permit",
					"protocol":     "ip",
					"ipv4-address": "10.0.0.0",
					"mask":         "0.0.0.255",
					"dst-any":      []any{nil},
				},
			},
		},
		{
			name: "dst-host-with-port-eq-and-log",
			in: map[string]any{
				"sequence": 40, "action": "permit", "protocol": "tcp",
				"src_any":  true,
				"dst_host": "10.1.1.1", "dst_eq": 80,
				"log": true,
			},
			want: map[string]any{
				"sequence": 40,
				"ace-rule": map[string]any{
					"action":           "permit",
					"protocol":         "tcp",
					"any":              []any{nil},
					"dst-host-address": "10.1.1.1",
					"dst-eq":           80,
					"log":              []any{nil},
				},
			},
		},
		{
			name: "remark-emits-leaf-list",
			in:   map[string]any{"sequence": 5, "remark": "block egress"},
			want: map[string]any{
				"sequence": 5,
				"remarks":  []any{"block egress"},
			},
		},
		{
			name: "false-empty-leaves-dropped",
			in:   map[string]any{"sequence": 1, "src_any": false, "log": "no"},
			want: map[string]any{"sequence": 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := aclRuleToYANG(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d keys, want %d\n  got:  %v\n  want: %v", len(got), len(tc.want), got, tc.want)
			}
			for k, v := range tc.want {
				gv, ok := got[k]
				if !ok {
					t.Errorf("missing key %q in output", k)
					continue
				}
				gj, _ := json.Marshal(gv)
				wj, _ := json.Marshal(v)
				if string(gj) != string(wj) {
					t.Errorf("key %q: got %s, want %s", k, gj, wj)
				}
			}
		})
	}
}
