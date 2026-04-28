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
	"errors"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// VLAN Phase-1 writer.
//
// netascode shape:
//
//   vlan:
//     vlans:
//       - id: 10
//         name: users
//       - id: 20
//         name: voice
//         shutdown: false
//
// YANG path:  /Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list
// Key: id. Managed leaves: name, shutdown.

const (
	vlanListPath    = "/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list"
	vlanEnvelopeKey = "Cisco-IOS-XE-vlan:vlan-list"
)

var vlanManagedLeaves = []string{"name", "shutdown"}

type vlanWriter struct{}

func init() { Override(vlanWriter{}) }

func (vlanWriter) Family() string      { return "vlan" }
func (vlanWriter) YANGPaths() []string { return []string{vlanListPath} }

func (vlanWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	raw, err := c.Fetch(ctx, vlanListPath)
	if err != nil {
		if isRESTCONF404(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	body, err := unwrapYANGEnvelope(raw, vlanEnvelopeKey)
	if err != nil || body == nil {
		return []map[string]any{}, err
	}
	list, err := decodeYANGList(body)
	if err != nil {
		return nil, fmt.Errorf("vlan: decode vlan-list: %w", err)
	}
	return list, nil
}

func (vlanWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := coerceVLANBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
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
		if inDevice && leavesEqual(desiredVLAN, observedVLAN, vlanManagedLeaves) {
			continue
		}
		entry := projectManagedLeaves(desiredVLAN, vlanManagedLeaves)
		entry["id"] = id
		body, err := wrapYANGPayload(vlanEnvelopeKey, []any{entry})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: fmt.Sprintf("%s=%d", vlanListPath, id),
			// 88ac685-fu: NETCONF builder needs PathSpec to emit
			// `<vlan-list><id>998</id>...` instead of the literal
			// `<vlan-list=998>` element. The vlanWriter is hand-
			// written (not the keyed-list base writer) so it
			// previously missed the PathSpec wire-up; live retest
			// 09 phase 1 surfaced `unknown-element vlan-list=998`.
			PathSpec: pathSpecForKeyedListEntry(vlanListPath, "id", fmt.Sprintf("%d", id)),
			Body:     body,
		})
	}
	return ops, nil
}

func (vlanWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

// PruneDiff emits a VerbDelete op for each VLAN id present on the
// device but absent from desired. Implements PruneCapable; the
// engine consults this only when the CR opts in via
// spec.pruneOnRelinquish: true.
func (vlanWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := coerceVLANBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}
	want := map[int]struct{}{}
	for _, v := range desiredList {
		id, err := vlanID(v)
		if err != nil {
			return nil, fmt.Errorf("desired: %w", err)
		}
		want[id] = struct{}{}
	}
	var orphans []int
	for _, v := range observedList {
		id, err := vlanID(v)
		if err != nil {
			return nil, fmt.Errorf("observed: %w", err)
		}
		if _, kept := want[id]; kept {
			continue
		}
		orphans = append(orphans, id)
	}
	sort.Ints(orphans)
	ops := make([]transport.Op, 0, len(orphans))
	for _, id := range orphans {
		ops = append(ops, transport.Op{
			Verb:     transport.VerbDelete,
			Path:     fmt.Sprintf("%s=%d", vlanListPath, id),
			PathSpec: pathSpecForKeyedListEntry(vlanListPath, "id", fmt.Sprintf("%d", id)),
		})
	}
	return ops, nil
}

// coerceVLANBlock accepts either the bare vlan-list slice or the
// nested netascode shape `{"vlans":[...]}`. The resolver produces the
// nested shape; inline CR test fixtures often use the bare slice.
func coerceVLANBlock(v any, origin string) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m["vlans"]; ok {
			return coerceList(inner, origin+".vlans")
		}
		return nil, nil
	}
	return coerceList(v, origin)
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
