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
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

func decodeMap(raw []byte, origin string) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", origin, err)
	}
	return out, nil
}

func coerceMap(v any, origin string) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("%s: want map, got %T", origin, v)
}

func coerceList(v any, innerKey, origin string) ([]map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m[innerKey]; ok {
			return coerceList(inner, innerKey, origin+"."+innerKey)
		}
	}
	if list, ok := v.([]map[string]any); ok {
		return list, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: want list, got %T", origin, v)
	}
	out := make([]map[string]any, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: want map, got %T", origin, i, item)
		}
		out = append(out, m)
	}
	return out, nil
}

func stringLeaf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func intLeaf(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		if x == float64(int(x)) {
			return int(x), true
		}
	case json.Number:
		if i, err := strconv.Atoi(string(x)); err == nil {
			return i, true
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return i, true
		}
	}
	return 0, false
}

func boolLeaf(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		return b, err == nil
	}
	return false, false
}

func scalarEqual(a, b any) bool {
	return reflect.DeepEqual(normalScalar(a), normalScalar(b))
}

func rejectUnsupportedKeys(m map[string]any, origin string, supported ...string) error {
	allowed := make(map[string]struct{}, len(supported))
	for _, key := range supported {
		allowed[key] = struct{}{}
	}
	for _, key := range sortedKeys(m) {
		if _, ok := allowed[key]; ok {
			continue
		}
		return fmt.Errorf("%s: field %q is not supported by the NX-OS DME writer yet", origin, key)
	}
	return nil
}

func normalScalar(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		if f, err := strconv.ParseFloat(string(x), 64); err == nil {
			return f
		}
	}
	return v
}

func cliOp(lines ...string) transport.Op {
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			clean = append(clean, line)
		}
	}
	return transport.Op{Verb: transport.VerbCLI, Body: []byte(strings.Join(clean, "\n"))}
}

func dmeMergeOp(path string, payload any) (transport.Op, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return transport.Op{}, err
	}
	return transport.Op{Verb: transport.VerbMerge, Path: path, Body: body}, nil
}

func dmeDeleteOp(path string) transport.Op {
	return transport.Op{Verb: transport.VerbDelete, Path: path}
}

func dmeObject(class string, attrs map[string]string, children ...map[string]any) map[string]any {
	obj := map[string]any{}
	if len(attrs) > 0 {
		obj["attributes"] = attrs
	}
	if len(children) > 0 {
		obj["children"] = children
	}
	return map[string]any{class: obj}
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
