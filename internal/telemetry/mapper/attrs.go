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

package mapper

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// ResourceAttrExtractor matches gNMI Update paths against operator-configured
// path → attribute-key mappings and extracts the leaf values as OTel resource
// attributes. The matcher strips gNMI list-key selectors from the canonical
// path so a config like /app-hosting-list/details/state matches every list
// instance (every app). The extracted value is then attached to every metric
// event that shares the same list-key scope.
type ResourceAttrExtractor struct {
	byPath map[string]string
}

func NewResourceAttrExtractor(attrs []configv1alpha1.ResourceAttribute) ResourceAttrExtractor {
	byPath := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr.Path == "" || attr.Key == "" {
			continue
		}
		byPath[stripPathListKeys(normalizeCanonicalPath(attr.Path))] = attr.Key
	}
	return ResourceAttrExtractor{byPath: byPath}
}

// Extract returns resource attributes extracted from update paths that have no
// list-key selector. List-keyed updates are surfaced via ExtractByEntity so
// each entity's metric events can carry its own attrs.
func (e ResourceAttrExtractor) Extract(notif *gpb.Notification) []KeyValue {
	if notif == nil || len(e.byPath) == 0 {
		return nil
	}
	out := make([]KeyValue, 0, len(e.byPath))
	for _, update := range notif.GetUpdate() {
		canonical, keys, _, _ := FlattenPath(notif.GetPrefix(), update.GetPath())
		if len(keys) > 0 {
			continue
		}
		if key, ok := e.byPath[canonical]; ok {
			if value, ok := typedValueString(update.GetVal()); ok {
				out = append(out, KeyValue{Key: key, Value: value})
			}
		}
	}
	return out
}

// ExtractByEntity groups extracted resource attributes by their full list-key
// scope. Mapper.Process attaches attrs from the update's exact scope and
// prefix scopes, with the longest matching scope taking precedence.
func (e ResourceAttrExtractor) ExtractByEntity(notif *gpb.Notification) map[string][]KeyValue {
	if notif == nil || len(e.byPath) == 0 {
		return nil
	}
	out := map[string][]KeyValue{}
	for _, update := range notif.GetUpdate() {
		canonical, _, _, tuple := FlattenPath(notif.GetPrefix(), update.GetPath())
		if len(tuple) == 0 {
			continue
		}
		stripped := stripPathListKeys(canonical)
		attrKey, ok := e.byPath[stripped]
		if !ok {
			continue
		}
		value, ok := typedValueString(update.GetVal())
		if !ok {
			continue
		}
		entity := entityScopeKey(tuple)
		out[entity] = append(out[entity], KeyValue{Key: attrKey, Value: value})
	}
	return out
}

// stripPathListKeys removes "[k=v]" selectors from each element of a canonical
// path so configured paths can wildcard-match every list instance.
func stripPathListKeys(canonical string) string {
	var b strings.Builder
	depth := 0
	for _, r := range canonical {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func entityScopeKey(t ListKeyTuple) string {
	if len(t) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(t); {
		if b.Len() > 0 {
			b.WriteByte('|')
		}
		listPath := t[i].ListPath
		b.WriteString(listName(listPath))
		for i < len(t) && t[i].ListPath == listPath {
			b.WriteByte('[')
			b.WriteString(t[i].KeyName)
			b.WriteByte('=')
			b.WriteString(t[i].KeyValue)
			b.WriteByte(']')
			i++
		}
	}
	return b.String()
}

func entityScopePrefixKeys(t ListKeyTuple) []string {
	if len(t) == 0 {
		return nil
	}
	keys := make([]string, 0, len(t))
	for i := range t {
		if i+1 < len(t) && t[i+1].ListPath == t[i].ListPath {
			continue
		}
		keys = append(keys, entityScopeKey(t[:i+1]))
	}
	return keys
}

func listName(listPath string) string {
	listPath = strings.Trim(listPath, "/")
	if listPath == "" {
		return ""
	}
	if idx := strings.LastIndexByte(listPath, '/'); idx >= 0 {
		return listPath[idx+1:]
	}
	return listPath
}

func typedValueString(v *gpb.TypedValue) (string, bool) {
	if v == nil {
		return "", false
	}
	switch x := v.Value.(type) {
	case *gpb.TypedValue_StringVal:
		return x.StringVal, true
	case *gpb.TypedValue_AsciiVal:
		return x.AsciiVal, true
	case *gpb.TypedValue_BoolVal:
		return fmt.Sprintf("%t", x.BoolVal), true
	case *gpb.TypedValue_BytesVal:
		return string(x.BytesVal), true
	case *gpb.TypedValue_IntVal:
		return fmt.Sprintf("%d", x.IntVal), true
	case *gpb.TypedValue_UintVal:
		return fmt.Sprintf("%d", x.UintVal), true
	case *gpb.TypedValue_FloatVal:
		return fmt.Sprintf("%g", x.FloatVal), true
	case *gpb.TypedValue_DoubleVal:
		return fmt.Sprintf("%g", x.DoubleVal), true
	case *gpb.TypedValue_DecimalVal:
		if x.DecimalVal == nil {
			return "", false
		}
		return fmt.Sprintf("%d.%09d", x.DecimalVal.Digits, x.DecimalVal.Precision), true
	case *gpb.TypedValue_JsonVal:
		return string(x.JsonVal), true
	case *gpb.TypedValue_JsonIetfVal:
		return string(x.JsonIetfVal), true
	case *gpb.TypedValue_LeaflistVal:
		return fmt.Sprintf("%v", x.LeaflistVal), true
	case *gpb.TypedValue_ProtoBytes:
		return base64.StdEncoding.EncodeToString(x.ProtoBytes), true
	case *gpb.TypedValue_AnyVal:
		return x.AnyVal.String(), true
	default:
		return "", false
	}
}

func numericValue(v *gpb.TypedValue) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.Value.(type) {
	case *gpb.TypedValue_IntVal:
		return float64(x.IntVal), true
	case *gpb.TypedValue_UintVal:
		return float64(x.UintVal), true
	case *gpb.TypedValue_FloatVal:
		return float64(x.FloatVal), true
	case *gpb.TypedValue_DoubleVal:
		return x.DoubleVal, true
	case *gpb.TypedValue_DecimalVal:
		if x.DecimalVal == nil {
			return 0, false
		}
		scale := float64(1)
		for i := uint32(0); i < x.DecimalVal.Precision; i++ {
			scale *= 10
		}
		return float64(x.DecimalVal.Digits) / scale, true
	case *gpb.TypedValue_JsonVal:
		return numericJSON(x.JsonVal)
	case *gpb.TypedValue_JsonIetfVal:
		return numericJSON(x.JsonIetfVal)
	default:
		return 0, false
	}
}

func numericJSON(raw []byte) (float64, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value interface{}
	if err := decoder.Decode(&value); err != nil {
		return 0, false
	}
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func logScalarValue(v *gpb.TypedValue) (string, bool) {
	if v == nil {
		return "", false
	}
	if _, ok := numericValue(v); ok {
		return "", false
	}
	switch x := v.Value.(type) {
	case *gpb.TypedValue_StringVal:
		return x.StringVal, true
	case *gpb.TypedValue_AsciiVal:
		return x.AsciiVal, true
	case *gpb.TypedValue_BoolVal:
		return fmt.Sprintf("%t", x.BoolVal), true
	case *gpb.TypedValue_BytesVal:
		return string(x.BytesVal), true
	case *gpb.TypedValue_JsonVal:
		return string(x.JsonVal), true
	case *gpb.TypedValue_JsonIetfVal:
		return string(x.JsonIetfVal), true
	default:
		return "", false
	}
}
