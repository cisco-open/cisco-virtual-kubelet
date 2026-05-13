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

// FamilySchema is the reflected metadata every writer exposes so that
// external tooling (cisco-vk-config-lint, cisco-vk-config-docs) can
// operate without a cross-package import cycle and without
// duplicating the writer's own source of truth.
//
// ManagedLeaves is the closed set of leaves the writer controls on
// the device; a CR that sets leaves outside this set is not in
// conflict, but the writer will not touch those leaves — which is
// the additive-merge contract netascode assumes.
//
// InnerKey identifies the netascode envelope key within a family
// configuration block (e.g. "vlans" inside "vlan", "interfaces"
// inside "interface_ethernet"). Empty for singleton families.
//
// KeyField is the list-element identity when the family is a keyed
// list. Empty for singleton families.
type FamilySchema struct {
	Family        string
	Shape         string // "singleton" | "keyed_list"
	ManagedLeaves []string
	InnerKey      string
	KeyField      string
}

// Schema returns the metadata for family, or a zero value + false if
// the writer is a skeleton (no real implementation registered) or the
// family is not registered.
func Schema(family string) (FamilySchema, bool) {
	w := Get(family)
	if w == nil {
		return FamilySchema{}, false
	}
	switch tw := w.(type) {
	case skeleton:
		return FamilySchema{}, false
	case singletonWriter:
		return FamilySchema{
			Family:        tw.family,
			Shape:         "singleton",
			ManagedLeaves: append([]string(nil), tw.managedLeaves...),
		}, true
	case keyedListWriter:
		return FamilySchema{
			Family:        tw.family,
			Shape:         "keyed_list",
			ManagedLeaves: append([]string(nil), tw.managedLeaves...),
			InnerKey:      tw.innerKey,
			KeyField:      tw.keyField,
		}, true
	}
	// Writer is a hand-written type (vlan, interface_ethernet,
	// interface_switchport, system). Look it up by family in the
	// handWrittenSchemas map below; a missing entry here is a bug
	// caught by TestHandWrittenSchemasRegistered.
	if s, ok := handWrittenSchemas[family]; ok {
		return s, true
	}
	return FamilySchema{}, false
}

// handWrittenSchemas is the registry for families whose writers are
// concrete struct types (not keyedListWriter / singletonWriter
// instantiations). Adding a hand-written writer means adding an
// entry here so external tools see the same managed-leaf picture.
var handWrittenSchemas = map[string]FamilySchema{
	"vlan": {
		Family: "vlan", Shape: "keyed_list",
		ManagedLeaves: vlanManagedLeaves,
		InnerKey:      "vlans", KeyField: "id",
	},
	"interface_ethernet": {
		Family: "interface_ethernet", Shape: "keyed_list",
		ManagedLeaves: ethernetManagedLeaves,
		InnerKey:      "interfaces", KeyField: "name",
	},
	"interface_switchport": {
		Family: "interface_switchport", Shape: "keyed_list",
		ManagedLeaves: switchportManagedLeaves,
		InnerKey:      "interfaces", KeyField: "name",
	},
	"system": {
		Family: "system", Shape: "singleton",
		ManagedLeaves: systemManagedLeaves(),
	},
	"dhcp": {
		Family: "dhcp", Shape: "keyed_list",
		ManagedLeaves: dhcpPoolManagedLeaves,
		InnerKey:      "pools", KeyField: "name",
	},
	"access_list_extended": {
		Family: "access_list_extended", Shape: "keyed_list",
		ManagedLeaves: []string{"rules"},
		InnerKey:      aclExtInnerKey, KeyField: aclExtKeyField,
	},
	"access_list_standard": {
		Family: "access_list_standard", Shape: "keyed_list",
		ManagedLeaves: []string{"rules"},
		InnerKey:      "standard", KeyField: "name",
	},
	"prefix_list": {
		Family: "prefix_list", Shape: "keyed_list",
		ManagedLeaves: []string{"description", "sequences"},
		InnerKey:      "prefixes", KeyField: "name",
	},
	"route_map": {
		Family: "route_map", Shape: "keyed_list",
		ManagedLeaves: []string{"description", "entries", "route-map-without-order-seq"},
		InnerKey:      "route_maps", KeyField: "name",
	},
	"ospf": {
		Family: "ospf", Shape: "keyed_list",
		ManagedLeaves: []string{
			"router-id", "network", "redistribute",
			"area", "auto-cost", "passive-interface",
		},
		InnerKey: "processes", KeyField: "id",
	},
	"eigrp": {
		Family: "eigrp", Shape: "keyed_list",
		ManagedLeaves: []string{
			"address-family", "network", "router-id",
			"metric", "eigrp-instance",
		},
		InnerKey: "processes", KeyField: "id",
	},
	"policy_map": {
		Family: "policy_map", Shape: "keyed_list",
		ManagedLeaves: []string{"description", "class", "type"},
		InnerKey:      "policy_maps", KeyField: "name",
	},
	"bgp": {
		Family:        "bgp",
		Shape:         "singleton",
		ManagedLeaves: bgpManagedLeaves,
	},
	"logging": {
		Family:        "logging",
		Shape:         "singleton",
		ManagedLeaves: loggingManagedLeaves,
	},
	"snmp_server": {
		Family:        "snmp_server",
		Shape:         "singleton",
		ManagedLeaves: snmpManagedLeaves,
	},
}

// systemManagedLeaves is a helper that returns the system writer's
// managed leaves sourced from its private systemLeaves slice.
func systemManagedLeaves() []string {
	out := make([]string, 0, len(systemLeaves))
	for _, l := range systemLeaves {
		out = append(out, l.netKey)
	}
	return out
}

// AllSchemas returns a family→schema snapshot for every registered
// family whose schema is extractable. Skeletons are omitted so the
// caller can distinguish real implementations.
func AllSchemas() map[string]FamilySchema {
	out := map[string]FamilySchema{}
	for _, name := range Families() {
		if s, ok := Schema(name); ok {
			out[name] = s
		}
	}
	return out
}
