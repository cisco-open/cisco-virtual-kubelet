// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTagUnaryLogicalRulesPreserveAtomicMeaning(t *testing.T) {
	for _, parent := range []string{"AND", "OR"} {
		for _, operation := range []string{"EQ", "LIKE", "ILIKE", "IN"} {
			t.Run(parent+"-"+operation, func(t *testing.T) {
				leaf := map[string]any{"operation": operation, "name": "hostname", "value": "%Case_Sensitive%"}
				rule := map[string]any{"member_type": "networkdevice", "operation": parent, "items": []any{leaf}}
				before, _ := json.Marshal(rule)
				tags, err := parseTemplateTags([]any{map[string]any{"name": "tag", "dynamic_rules": []any{rule}}})
				if err != nil {
					t.Fatal(err)
				}
				actual := tags[0].payload["dynamicRules"].([]any)[0].(map[string]any)
				if actual["memberType"] != "networkdevice" || !reflect.DeepEqual(actual["rules"], leaf) {
					t.Fatal("single-condition lowering changed field/operator/value semantics", actual)
				}
				after, _ := json.Marshal(rule)
				if string(before) != string(after) {
					t.Fatal("logical lowering changed the source model")
				}
			})
		}
	}
}

func TestTagUnaryLogicalRulesRefuseAmbiguousMeaning(t *testing.T) {
	for _, rule := range []map[string]any{
		{"member_type": "networkdevice", "operation": "OR", "items": []any{}},
		{"member_type": "networkdevice", "operation": "AND", "name": "hostname", "items": []any{map[string]any{"operation": "EQ", "name": "hostname", "value": "value"}}},
		{"member_type": "networkdevice", "operation": "AND", "items": []any{map[string]any{"name": "hostname", "value": "value"}}},
		{"member_type": "networkdevice", "operation": "AND", "items": []any{map[string]any{"operation": "OR", "name": "hostname", "value": "value"}}},
	} {
		if _, err := parseTemplateTags([]any{map[string]any{"name": "tag", "dynamic_rules": []any{rule}}}); err == nil {
			t.Fatal("ambiguous or empty logical group was silently lowered")
		}
	}
}
