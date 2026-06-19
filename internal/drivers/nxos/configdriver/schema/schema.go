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

// Package schema carries the small NX-OS config-driver family catalogue used
// by the first vertical slice. The values are transport-internal fetch paths,
// not NX-OS YANG paths.
package schema

const (
	FamilySystem            = "system"
	FamilyVLAN              = "vlan"
	FamilyInterfaceEthernet = "interface_ethernet"

	PathSystemHostname    = "/nxos/system/hostname"
	PathVLANBrief         = "/nxos/vlan/brief"
	PathInterfaceEthernet = "/nxos/interface/ethernet"

	DNSystem          = "sys"
	DNBridgeDomain    = "sys/bd"
	DNInterfaceEntity = "sys/intf"
)

var Families = []string{
	FamilySystem,
	FamilyVLAN,
	FamilyInterfaceEthernet,
}

var FetchPaths = []string{
	PathSystemHostname,
	PathVLANBrief,
	PathInterfaceEthernet,
}

// CoverageState records whether a NetAsCode family is ready for use through
// the NX-OS DME writer pipeline.
type CoverageState string

const (
	// CoverageSupported means the family has Fetch -> Diff -> Apply -> Verify
	// coverage in the NX-OS runtime.
	CoverageSupported CoverageState = "supported"
	// CoveragePlanned means the family exists in the NX-OS NetAsCode stripe
	// and is a candidate for the next DME writer waves.
	CoveragePlanned CoverageState = "planned"
	// CoverageDeferred means the family should not be added until a larger
	// platform decision is made.
	CoverageDeferred CoverageState = "deferred"
)

// FamilyCoverage is the production-readiness contract for one NX-OS family.
// It is deliberately close to the public NetAsCode family names so docs, tests,
// and future writer work can share the same source of truth.
type FamilyCoverage struct {
	Family            string
	Section           string
	State             CoverageState
	SupportedFields   []string
	UnsupportedFields []string
	Notes             string
}

// CoverageMatrix records the NX-OS runtime coverage in descending readiness
// order: supported families first, then planned expansion, then deferred areas.
var CoverageMatrix = []FamilyCoverage{
	{
		Family:          FamilySystem,
		Section:         "device",
		State:           CoverageSupported,
		SupportedFields: []string{"system.hostname"},
		UnsupportedFields: []string{
			"system.mtu",
		},
		Notes: "Maps to DME topSystem name and verifies by reading sys.",
	},
	{
		Family:  FamilyVLAN,
		Section: "device",
		State:   CoverageSupported,
		SupportedFields: []string{
			"vlan.vlans[].id",
			"vlan.vlans[].name",
		},
		UnsupportedFields: []string{
			"vlan.vlans[].vni",
			"vlan.vlans[].vn_segment",
		},
		Notes: "Maps to l2BD under sys/bd; VXLAN/EVPN leaves are intentionally excluded from the first slice.",
	},
	{
		Family:  FamilyInterfaceEthernet,
		Section: "interface",
		State:   CoverageSupported,
		SupportedFields: []string{
			"interfaces.ethernets[].id",
			"interfaces.ethernets[].description",
			"interfaces.ethernets[].shutdown",
			"interfaces.ethernets[].mtu",
		},
		UnsupportedFields: []string{
			"interfaces.ethernets[].switchport",
			"interfaces.ethernets[].ip",
			"interfaces.ethernets[].ipv6",
			"interfaces.ethernets[].channel_group",
			"interfaces.ethernets[].ospf",
			"interfaces.ethernets[].pim",
		},
		Notes: "Maps to l1PhysIf under sys/intf; L2/L3 protocol attachments are future family-specific writers.",
	},
	{Family: "interface_switchport", Section: "interface", State: CoveragePlanned, Notes: "Next L2 access/trunk slice after Ethernet base leaves."},
	{Family: "interface_vlan", Section: "interface", State: CoveragePlanned, Notes: "SVI addressing and shutdown state; requires DME mapping plus safe delete semantics."},
	{Family: "interface_loopback", Section: "interface", State: CoveragePlanned, Notes: "Loopback identity and L3 attachment primitives."},
	{Family: "interface_port_channel", Section: "interface", State: CoveragePlanned, Notes: "Port-channel and member coordination must verify bundle state."},
	{Family: "vrf", Section: "device", State: CoveragePlanned, Notes: "Foundational dependency for routed interfaces and routing families."},
	{Family: "static_route", Section: "device", State: CoveragePlanned, Notes: "Requires VRF-aware key ownership and prune tests."},
	{Family: "ntp", Section: "device", State: CoveragePlanned, Notes: "Low-risk management family for early DME expansion."},
	{Family: "logging", Section: "device", State: CoveragePlanned, Notes: "Low-risk management family for early DME expansion."},
	{Family: "snmp_server", Section: "device", State: CoveragePlanned, Notes: "Requires careful secret/reference handling before write support."},
	{Family: "telemetry", Section: "device", State: CoverageDeferred, Notes: "Keep distinct from CVK's runtime telemetry ingestion until model-driven telemetry config ownership is designed."},
	{Family: "evpn", Section: "device", State: CoverageDeferred, Notes: "Requires coordinated VLAN, VNI, NVE, BGP, and fabric policy ownership."},
	{Family: "nve", Section: "interface", State: CoverageDeferred, Notes: "Requires VXLAN/EVPN orchestration semantics rather than an isolated first writer."},
}

// SupportedCoverage returns the currently write-capable NX-OS families.
func SupportedCoverage() []FamilyCoverage {
	return coverageByState(CoverageSupported)
}

// PlannedCoverage returns the queued NX-OS NetAsCode families that should be
// implemented before claiming broad runtime parity with IOS-XE.
func PlannedCoverage() []FamilyCoverage {
	return coverageByState(CoveragePlanned)
}

func coverageByState(state CoverageState) []FamilyCoverage {
	out := make([]FamilyCoverage, 0, len(CoverageMatrix))
	for _, entry := range CoverageMatrix {
		if entry.State == state {
			out = append(out, cloneCoverage(entry))
		}
	}
	return out
}

func cloneCoverage(in FamilyCoverage) FamilyCoverage {
	out := in
	out.SupportedFields = append([]string(nil), in.SupportedFields...)
	out.UnsupportedFields = append([]string(nil), in.UnsupportedFields...)
	return out
}

// FamilyOrder applies the basic parent-before-child ordering for the first
// NX-OS families: global system config, VLANs, then interfaces.
func FamilyOrder(in []string) []string {
	weight := map[string]int{
		FamilySystem:            0,
		FamilyVLAN:              1,
		FamilyInterfaceEthernet: 2,
	}
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && weightOf(weight, out[j]) < weightOf(weight, out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func weightOf(weights map[string]int, family string) int {
	if w, ok := weights[family]; ok {
		return w
	}
	return len(weights) + 1
}
