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
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// Switchport Phase-2 writer.
//
// Switchport isn't a top-level YANG list; it is a sub-container on
// each ethernet interface (/interface/<type>=<name>/switchport). The
// writer therefore fans out across (type, name) the same way
// interface_ethernet does, but writes the switchport payload at the
// deeper path.
//
// netascode:
//
//   interface_switchport:
//     interfaces:
//       - type: GigabitEthernet
//         name: "0/0/1"
//         mode: access           # or trunk
//         access_vlan: 10
//         trunk_allowed_vlans: [10, 20]
//         trunk_native_vlan: 1
//
// Depends on: interface_ethernet, vlan.

var switchportManagedLeaves = []string{
	"mode",
	"access",
	"trunk",
}

type switchportWriter struct{}

func init() { Override(switchportWriter{}) }

func (switchportWriter) Family() string { return "interface_switchport" }

func (switchportWriter) YANGPaths() []string {
	out := make([]string, 0, len(ethernetTypes))
	for _, t := range ethernetTypes {
		out = append(out, "/Cisco-IOS-XE-native:native/interface/"+t+"/switchport")
	}
	return out
}

func (switchportWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	var combined []map[string]any
	for _, t := range ethernetTypes {
		// We cannot list "all switchport containers" in one shot — the
		// container is per-interface. Phase-2 fetches the whole
		// ethernet subtree (re-using interface_ethernet's path layout)
		// and projects the switchport sub-container for each interface
		// into the observed list.
		path := "/Cisco-IOS-XE-native:native/interface/" + t
		raw, err := c.Fetch(ctx, path)
		if err != nil {
			if isRESTCONF404(err) {
				continue
			}
			return nil, fmt.Errorf("interface_switchport: fetch %s: %w", t, err)
		}
		body, err := unwrapYANGEnvelope(raw, "Cisco-IOS-XE-native:"+t)
		if err != nil {
			return nil, fmt.Errorf("interface_switchport: decode %s envelope: %w", t, err)
		}
		if body == nil {
			continue
		}
		list, err := decodeYANGList(body)
		if err != nil {
			return nil, fmt.Errorf("interface_switchport: decode %s list: %w", t, err)
		}
		for _, ifr := range list {
			sw, hasSW := ifr["switchport"].(map[string]any)
			if !hasSW {
				continue
			}
			name, _ := ifr["name"].(string)
			row := map[string]any{"type": t, "name": name}
			for k, v := range sw {
				row[k] = v
			}
			combined = append(combined, row)
		}
	}
	return combined, nil
}

func (switchportWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := coerceSwitchportBlock(desired, "desired")
	if err != nil {
		return nil, err
	}
	observedList, err := coerceList(observed, "observed")
	if err != nil {
		return nil, err
	}

	type key struct{ typ, name string }
	want := map[key]map[string]any{}
	order := []key{}
	for _, e := range desiredList {
		t, _ := e["type"].(string)
		n, _ := e["name"].(string)
		if t == "" || n == "" {
			return nil, fmt.Errorf("interface_switchport: entry missing type or name: %v", e)
		}
		if !knownEthernetType(t) {
			return nil, fmt.Errorf("interface_switchport: unsupported type %q", t)
		}
		k := key{t, n}
		if _, dup := want[k]; !dup {
			order = append(order, k)
		}
		want[k] = e
	}
	got := map[key]map[string]any{}
	for _, e := range observedList {
		t, _ := e["type"].(string)
		n, _ := e["name"].(string)
		got[key{t, n}] = e
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].typ != order[j].typ {
			return order[i].typ < order[j].typ
		}
		return order[i].name < order[j].name
	})

	ops := make([]transport.Op, 0, len(order))
	for _, k := range order {
		entry := want[k]
		observedEntry, inDevice := got[k]
		if inDevice && leavesEqual(entry, observedEntry, switchportManagedLeaves) {
			continue
		}
		body, err := json.Marshal(map[string]any{
			"Cisco-IOS-XE-native:switchport": projectManagedLeaves(entry, switchportManagedLeaves),
		})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: fmt.Sprintf("/Cisco-IOS-XE-native:native/interface/%s=%s/switchport", k.typ, k.name),
			// Wave 7B: structured PathSpec including the trailing
			// "switchport" container, so gNMI emits the right path
			// for switchport edits on interfaces whose name
			// contains '/'.
			PathSpec: pathSpecForInterfaceChild(k.typ, k.name, "switchport"),
			Body:     body,
		})
	}
	return ops, nil
}

func (switchportWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func coerceSwitchportBlock(v any, origin string) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m["interfaces"]; ok {
			return coerceList(inner, origin+".interfaces")
		}
		return nil, nil
	}
	return coerceList(v, origin)
}
