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

// Ethernet-interface Phase-1 writer.
//
// netascode shape:
//
//   interface_ethernet:
//     interfaces:
//       - type: GigabitEthernet       # picks the YANG subtree
//         name: "0/0/0"               # identifies the interface within the subtree
//         description: Uplink
//         shutdown: false
//         vrf: MGMT
//         ipv4_address: 10.0.0.1
//         ipv4_address_mask: 255.255.255.0
//
// YANG paths: one per ethernet speed under /Cisco-IOS-XE-native:native/interface.
// Key: (type, name). Managed leaves: description, shutdown, vrf,
// ipv4_address, ipv4_address_mask. mtu is included — it is the most
// common post-apply drift source.
//
// Writers for individual physical-interface leaves that model nested
// containers (ip-address, vrf) with their own sub-path would still
// emit merge ops at the interface level; when Phase-2 grows the
// managed-leaf set, the resolver's additive-only semantics keep the
// writer safe against device-side leaves we do not model.

var ethernetTypes = []string{
	"GigabitEthernet",
	"TwoGigabitEthernet",
	"FiveGigabitEthernet",
	"TenGigabitEthernet",
	"TwentyFiveGigE",
	"FortyGigabitEthernet",
	"HundredGigE",
	"TwoHundredGigE",
	"FourHundredGigE",
}

var ethernetManagedLeaves = []string{
	"description",
	"shutdown",
	"mtu",
}

type ethernetWriter struct{}

func init() { Override(ethernetWriter{}) }

func (ethernetWriter) Family() string { return "interface_ethernet" }

func (ethernetWriter) YANGPaths() []string {
	out := make([]string, 0, len(ethernetTypes))
	for _, t := range ethernetTypes {
		out = append(out, "/Cisco-IOS-XE-native:native/interface/"+t)
	}
	return out
}

// Fetch reads every supported ethernet subtree in parallel (sequential
// for Phase-1 to keep the transport lock simple), concatenating the
// per-type lists into a single observed slice. Each entry is tagged
// with its type so Diff can locate it later.
func (ethernetWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	var combined []map[string]any
	for _, t := range ethernetTypes {
		path := "/Cisco-IOS-XE-native:native/interface/" + t
		raw, err := c.Fetch(ctx, path)
		if err != nil {
			if isRESTCONF404(err) {
				continue
			}
			return nil, fmt.Errorf("interface_ethernet: fetch %s: %w", t, err)
		}
		body, err := unwrapYANGEnvelope(raw, "Cisco-IOS-XE-native:"+t)
		if err != nil {
			return nil, fmt.Errorf("interface_ethernet: decode %s envelope: %w", t, err)
		}
		if body == nil {
			continue
		}
		var list []map[string]any
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("interface_ethernet: decode %s list: %w", t, err)
		}
		for _, el := range list {
			el["type"] = t
			combined = append(combined, el)
		}
	}
	return combined, nil
}

func (ethernetWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredList, err := coerceEthernetBlock(desired, "desired")
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
			return nil, fmt.Errorf("interface_ethernet: entry missing type or name: %v", e)
		}
		if !knownEthernetType(t) {
			return nil, fmt.Errorf("interface_ethernet: unsupported type %q (add to ethernetTypes)", t)
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
		if inDevice && leavesEqual(entry, observedEntry, ethernetManagedLeaves) {
			continue
		}
		proj := projectManagedLeaves(entry, ethernetManagedLeaves)
		proj["name"] = k.name
		body, err := json.Marshal(map[string]any{
			"Cisco-IOS-XE-native:" + k.typ: []any{proj},
		})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: fmt.Sprintf("/Cisco-IOS-XE-native:native/interface/%s=%s", k.typ, k.name),
			// Wave 7B: structured PathSpec so gNMI Set against an
			// interface name containing '/' (e.g. GigabitEthernet
			// "0/0/0") preserves the slash in the key. The string
			// Path stays for RESTCONF/NETCONF; gNMI prefers
			// PathSpec via opToGNMIPath.
			PathSpec: pathSpecForInterface(k.typ, k.name),
			Body:     body,
		})
	}
	return ops, nil
}

func (ethernetWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func knownEthernetType(t string) bool {
	for _, et := range ethernetTypes {
		if et == t {
			return true
		}
	}
	return false
}

func coerceEthernetBlock(v any, origin string) ([]map[string]any, error) {
	if m, ok := v.(map[string]any); ok {
		if inner, ok := m["interfaces"]; ok {
			return coerceList(inner, origin+".interfaces")
		}
		return nil, nil
	}
	return coerceList(v, origin)
}
