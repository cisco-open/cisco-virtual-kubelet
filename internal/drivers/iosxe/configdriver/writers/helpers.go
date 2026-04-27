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
	"strings"
)

// coerceMap normalises a netascode-decoded YAML value into
// map[string]any. Used by Fetch/Diff when the desired or observed
// payload is expected to be a container, not a list.
func coerceMap(v any, origin string) (map[string]any, error) {
	switch tv := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return tv, nil
	default:
		return nil, fmt.Errorf("%s: is %T, want map", origin, v)
	}
}

// coerceList normalises either []any (YAML-native) or []map[string]any
// (already normalised) into []map[string]any.
func coerceList(v any, origin string) ([]map[string]any, error) {
	switch tv := v.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return tv, nil
	case []any:
		out := make([]map[string]any, 0, len(tv))
		for i, el := range tv {
			m, ok := el.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: element is %T, want map", origin, i, el)
			}
			out = append(out, m)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: list is %T, want slice", origin, v)
	}
}

// unwrapYANGEnvelope extracts the inner body from a RESTCONF response
// that wraps the payload under a "<module>:<element>" key. Returns the
// raw input when the envelope is absent; callers that require the
// envelope must validate separately.
func unwrapYANGEnvelope(raw []byte, envelopeKey string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		// Callers that want to accept both shapes can fall back to the
		// raw body when unmarshalling as envelope fails.
		return nil, err
	}
	if body, ok := env[envelopeKey]; ok {
		return body, nil
	}
	// Envelope absent — return the original bytes so decoders that
	// accept a direct payload can still work.
	return json.RawMessage(raw), nil
}

// wrapYANGPayload produces a RESTCONF request body that wraps body under
// envelopeKey. This is the reverse of unwrapYANGEnvelope and is used
// when writers PUT/PATCH a single entry or a container.
func wrapYANGPayload(envelopeKey string, body any) ([]byte, error) {
	return json.Marshal(map[string]any{envelopeKey: body})
}

// leavesEqual compares the subset of leaves the writer manages. Extra
// leaves present only on the observed side are ignored so devices that
// expose leaves the Phase-1 writer does not model do not read as
// perpetually drifted.
func leavesEqual(desired, observed map[string]any, managed []string) bool {
	for _, key := range managed {
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

// decodeYANGList unmarshals body as `[]map[string]any`, tolerating
// the single-object shape some NETCONF candidate-datastore responses
// emit for one-entry lists. RESTCONF and most NETCONF backends wrap
// a list as `[{...}]` even when the list has exactly one element;
// IOS-XE 17.x's candidate datastore sometimes returns the bare
// `{...}` instead. The keyed-list family writers all need the same
// tolerance — caught against a live Cat9300 retest where test 07's
// interface_loopback Fetch failed with `cannot unmarshal object
// into Go value of type []map[string]interface {}` immediately
// after the device-mode shift to candidate-only.
func decodeYANGList(body []byte) ([]map[string]any, error) {
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err == nil {
		return list, nil
	}
	var single map[string]any
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, err
	}
	return []map[string]any{single}, nil
}

// scalarEqual compares two YAML-decoded scalars. YAML and JSON both
// produce float64 for numbers, so == is usually enough; the stringified
// second pass handles mixed representations (e.g. yang "true" vs bool).
// Composite values (maps, slices) are not == comparable in Go and would
// panic — fall back to deep-equal for those, then to stringified compare.
func scalarEqual(a, b any) bool {
	if !isComparable(a) || !isComparable(b) {
		if reflect.DeepEqual(a, b) {
			return true
		}
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
	if a == b {
		return true
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// isComparable reports whether v's dynamic type can be == compared
// without panicking. Maps and slices cannot; structs and arrays can
// only if every field/element is itself comparable, which the runtime
// checks lazily and panics on. We treat that family as not-comparable
// up-front to avoid the recover-after-panic dance.
func isComparable(v any) bool {
	if v == nil {
		return true
	}
	switch reflect.TypeOf(v).Kind() {
	case reflect.Map, reflect.Slice, reflect.Func:
		return false
	default:
		return true
	}
}

// projectManagedLeaves returns a copy of src containing only the keys
// listed in managed. Writers use it to send a merge-shaped payload that
// does not overwrite leaves the device has configured outside CVK's
// scope.
func projectManagedLeaves(src map[string]any, managed []string) map[string]any {
	out := map[string]any{}
	for _, key := range managed {
		if v, ok := src[key]; ok {
			out[key] = v
		}
	}
	return out
}

// isRESTCONF404 recognises the transport-level error the RESTCONF
// adapter emits when the device returns HTTP 404. Matches on the
// status string rather than typed errors so writers do not bind to
// the adapter type.
func isRESTCONF404(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(msg, "Not Found")
}

// fetchOrEmpty is a small helper used by singleton writers: on 404 or
// empty body, return nil instead of an error so the caller can treat
// "not present on device" as "empty observed state".
func fetchOrEmptyErr(err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if isRESTCONF404(err) {
		return true, nil
	}
	return false, err
}
