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

package intent

import (
	"reflect"
	"testing"
)

func TestFixYAML11BoolKeys_FalseToNo(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"prefix_list": map[string]any{
			"prefixes": []any{
				map[string]any{
					"name": "PL1",
					"sequences": []any{
						map[string]any{"false": float64(10), "action": "permit", "ip": "10.0.0.0/8"},
						map[string]any{"false": float64(20), "action": "deny", "ip": "0.0.0.0/0"},
					},
				},
			},
		},
	}
	FixYAML11BoolKeys(input)
	seqs := input["prefix_list"].(map[string]any)["prefixes"].([]any)[0].(map[string]any)["sequences"].([]any)
	for i, s := range seqs {
		m := s.(map[string]any)
		if _, ok := m["false"]; ok {
			t.Errorf("seqs[%d]: still has 'false' key", i)
		}
		if _, ok := m["no"]; !ok {
			t.Errorf("seqs[%d]: missing 'no' key", i)
		}
	}
}

func TestFixYAML11BoolKeys_NoConflict(t *testing.T) {
	t.Parallel()
	// If "no" already exists, "false" should NOT overwrite it.
	input := map[string]any{
		"no":    float64(10),
		"false": "some-other-value",
	}
	FixYAML11BoolKeys(input)
	if input["no"] != float64(10) {
		t.Errorf("no = %v, want 10 (should not be overwritten)", input["no"])
	}
	if input["false"] != "some-other-value" {
		t.Errorf("false key should remain when 'no' already exists")
	}
}

func TestFixYAML11BoolKeys_NoFalseKey(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"name":   "test",
		"action": "permit",
	}
	orig := map[string]any{
		"name":   "test",
		"action": "permit",
	}
	FixYAML11BoolKeys(input)
	if !reflect.DeepEqual(input, orig) {
		t.Errorf("map should be unchanged when no 'false' key exists")
	}
}

func TestFixYAML11BoolKeys_DeepNesting(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"level1": map[string]any{
			"level2": []any{
				map[string]any{
					"level3": map[string]any{
						"false": float64(42),
					},
				},
			},
		},
	}
	FixYAML11BoolKeys(input)
	l3 := input["level1"].(map[string]any)["level2"].([]any)[0].(map[string]any)["level3"].(map[string]any)
	if _, ok := l3["false"]; ok {
		t.Error("deep nested 'false' key should be renamed")
	}
	if l3["no"] != float64(42) {
		t.Errorf("no = %v, want 42", l3["no"])
	}
}

func TestFixYAML11BoolKeys_NonMap(t *testing.T) {
	t.Parallel()
	// Non-map values should pass through unchanged.
	if FixYAML11BoolKeys("hello") != "hello" {
		t.Error("string should pass through")
	}
	if FixYAML11BoolKeys(nil) != nil {
		t.Error("nil should pass through")
	}
	if FixYAML11BoolKeys(float64(42)) != float64(42) {
		t.Error("float should pass through")
	}
}
