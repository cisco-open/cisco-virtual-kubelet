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

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

type ResourceAttrExtractor struct {
	byPath map[string]string
}

func NewResourceAttrExtractor(attrs []configv1alpha1.ResourceAttribute) ResourceAttrExtractor {
	byPath := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr.Path == "" || attr.Key == "" {
			continue
		}
		byPath[normalizeCanonicalPath(attr.Path)] = attr.Key
	}
	return ResourceAttrExtractor{byPath: byPath}
}

func (e ResourceAttrExtractor) Extract(notif *gpb.Notification) []KeyValue {
	if notif == nil || len(e.byPath) == 0 {
		return nil
	}
	out := make([]KeyValue, 0, len(e.byPath))
	for _, update := range notif.GetUpdate() {
		canonical, _, _ := FlattenPath(notif.GetPrefix(), update.GetPath())
		if key, ok := e.byPath[canonical]; ok {
			if value, ok := typedValueString(update.GetVal()); ok {
				out = append(out, KeyValue{Key: key, Value: value})
			}
		}
	}
	return out
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
