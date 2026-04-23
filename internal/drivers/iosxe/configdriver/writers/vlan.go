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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// VLAN Phase-1 writer.
//
// netascode shape (per device configuration):
//
//   vlan:
//     vlans:
//       - id: 10
//         name: users
//       - id: 20
//         name: voice
//         shutdown: false
//
// YANG path (Cisco-IOS-XE-native):
//
//   /Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list
//
// Key field: id. Supported leaves in Phase 1: id (uint), name (string),
// shutdown (bool). Additional leaves (device-tracking, remote-span, etc.)
// will be added as the YANG model is regenerated.

const (
	vlanListPath = "/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list"
)

type vlanWriter struct{}

func init() {
	// Replace the skeleton registration with the real writer.
	Override(vlanWriter{})
}

func (vlanWriter) Family() string      { return "vlan" }
func (vlanWriter) YANGPaths() []string { return []string{vlanListPath} }

// Fetch returns the device's current vlan-list as a slice of maps. The
// RESTCONF response for this path is wrapped in a "Cisco-IOS-XE-vlan:
// vlan-list" key; we strip it so the caller sees a clean netascode-style
// slice. A 404/empty response is treated as "no VLANs configured" — an
// empty slice, not an error.
func (vlanWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, vlanListPath)
	if err != nil {
		// RESTCONF reports 404 for an absent subtree as a transport-level
		// error; callers that want "empty" must catch it here. We accept
		// an empty body as equivalent because older IOS-XE returns 204.
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return []map[string]any{}, nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("vlan: decode device response: %w", err)
	}
	body, ok := envelope["Cisco-IOS-XE-vlan:vlan-list"]
	if !ok {
		// Devices sometimes return the list without the expected wrapper
		// when the xpath addresses the list directly; treat that as valid.
		body = raw
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("vlan: decode vlan-list: %w", err)
	}
	return list, nil
}

// Diff computes the minimum-op plan from observed → desired. Semantics:
//
//   - VLANs in desired but not in observed → MERGE (create).
//   - VLANs in both with any leaf different → MERGE on the VLAN id
//     (targeted update rather than full PUT so other leaves the writer
//     doesn't manage survive).
//   - VLANs in observed but not in desired → not deleted. The writer
//     only manages the VLANs the intent names; pruning is a
//     whole-family decision expressed via IOSXEConfig.spec.pruneOnRelinquish.
//
// The returned Op slice is ordered by VLAN id so two semantically-equal
// diffs produce byte-equal RESTCONF payloads — handy for tests and for
// canonical logging.
func (vlanWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := coerceVLANList(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceVLANList(observed, "observed")
	if err != nil {
		return nil, err
	}

	want := map[int]map[string]any{}
	for _, v := range desiredList {
		id, err := vlanID(v)
		if err != nil {
			return nil, fmt.Errorf("desired: %w", err)
		}
		want[id] = v
	}
	got := map[int]map[string]any{}
	for _, v := range observedList {
		id, err := vlanID(v)
		if err != nil {
			return nil, fmt.Errorf("observed: %w", err)
		}
		got[id] = v
	}

	ids := make([]int, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	ops := make([]transport.Op, 0, len(ids))
	for _, id := range ids {
		desiredVLAN := want[id]
		observedVLAN, inDevice := got[id]
		if inDevice && vlanLeafEqual(desiredVLAN, observedVLAN) {
			continue
		}
		body, err := encodeVLAN(id, desiredVLAN)
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: fmt.Sprintf("%s=%d", vlanListPath, id),
			Body: body,
		})
	}
	return ops, nil
}

// Apply sends the diff ops to the device. No extra logic vs. the
// transport — every op is self-contained.
func (vlanWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// coerceVLANList normalises the various Go types netascode-decoded YAML
// can produce into a []map[string]any. Accepts []any (YAML-native) and
// []map[string]any (already normalised) so both resolver output and
// hand-built test inputs work.
func coerceVLANList(v any, origin string) ([]map[string]any, error) {
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

func vlanID(v map[string]any) (int, error) {
	raw, ok := v["id"]
	if !ok {
		return 0, errors.New("vlan entry missing 'id'")
	}
	switch tv := raw.(type) {
	case int:
		return tv, nil
	case int64:
		return int(tv), nil
	case float64:
		return int(tv), nil
	case string:
		var id int
		if _, err := fmt.Sscanf(tv, "%d", &id); err != nil {
			return 0, fmt.Errorf("vlan id %q is not numeric: %w", tv, err)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("vlan id %v has unsupported type %T", raw, raw)
	}
}

// vlanLeafEqual compares the Phase-1 leaves the writer manages. Extra
// leaves on the observed side are ignored so a device that exposes
// leaves we don't model yet doesn't appear perpetually-drifted.
func vlanLeafEqual(desired, observed map[string]any) bool {
	managed := []string{"name", "shutdown"}
	for _, key := range managed {
		dv, dHas := desired[key]
		ov, oHas := observed[key]
		if dHas != oHas {
			return false
		}
		if !dHas {
			continue
		}
		if !leafEqual(dv, ov) {
			return false
		}
	}
	return true
}

func leafEqual(a, b any) bool {
	// Numbers from YAML decode to float64; numbers from JSON decoders
	// also default to float64. So a direct == usually works, but stringly-
	// typed values (yang booleans sometimes come across as "true") need
	// a stringified comparison as a second pass.
	if a == b {
		return true
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// encodeVLAN renders a single VLAN entry as the RESTCONF yang-data+json
// payload the list endpoint expects. The envelope key is the fully-
// qualified YANG list-element name.
func encodeVLAN(id int, v map[string]any) ([]byte, error) {
	entry := map[string]any{"id": id}
	for _, key := range []string{"name", "shutdown"} {
		if val, ok := v[key]; ok {
			entry[key] = val
		}
	}
	payload := map[string]any{
		"Cisco-IOS-XE-vlan:vlan-list": []any{entry},
	}
	return json.Marshal(payload)
}

// isRESTCONF404 recognises the transport-level error the RESTCONF
// adapter emits when the device returns HTTP 404. The RESTCONF adapter
// includes the status string in the error message; we match on that
// rather than typed errors so we do not bind writers to the adapter
// type.
func isRESTCONF404(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsAny(msg, "404", "Not Found")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
