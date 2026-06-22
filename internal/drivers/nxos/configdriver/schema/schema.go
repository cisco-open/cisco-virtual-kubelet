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

// Package schema carries the NX-OS config-driver family catalogue. Fetch path
// values are transport-internal DME paths, not NX-OS YANG paths. The coverage
// matrix is intentionally broader than the first writer slice so the runtime
// has an explicit NetAsCode parity target.
package schema

import "strings"

const (
	FamilySystem            = "system"
	FamilyFeature           = "feature"
	FamilyFeatureSet        = "feature_set"
	FamilyVLAN              = "vlan"
	FamilyInterfaceEthernet = "interface_ethernet"

	PathSystemHostname    = "/nxos/system/hostname"
	PathFeature           = "/nxos/feature"
	PathFeatureSet        = "/nxos/feature-set"
	PathVLANBrief         = "/nxos/vlan/brief"
	PathInterfaceEthernet = "/nxos/interface/ethernet"

	DNSystem          = "sys"
	DNBridgeDomain    = "sys/bd"
	DNInterfaceEntity = "sys/intf"
)

var Families = []string{
	FamilySystem,
	FamilyFeature,
	FamilyFeatureSet,
	FamilyVLAN,
	FamilyInterfaceEthernet,
}

var FetchPaths = []string{
	PathSystemHostname,
	PathFeature,
	PathFeatureSet,
	PathVLANBrief,
	PathInterfaceEthernet,
}

// FeatureDMEMapping maps the public NetAsCode NX-OS feature leaf to the DME
// feature-management class used by NX-API REST.
type FeatureDMEMapping struct {
	Field string
	Class string
}

var featureDMEMappings = []FeatureDMEMapping{
	{Field: "analytics", Class: "fmAnalytics"},
	{Field: "bash_shell", Class: "fmBashShell"},
	{Field: "bfd", Class: "fmBfd"},
	{Field: "bgp", Class: "fmBgp"},
	{Field: "dhcp", Class: "fmDhcp"},
	{Field: "evpn", Class: "fmEvpn"},
	{Field: "fabric_forwarding", Class: "fmHmm"},
	{Field: "grpc", Class: "fmGrpc"},
	{Field: "hsrp", Class: "fmHsrp"},
	{Field: "interface_vlan", Class: "fmInterfaceVlan"},
	{Field: "isis", Class: "fmIsis"},
	{Field: "lacp", Class: "fmLacp"},
	{Field: "lldp", Class: "fmLldp"},
	{Field: "macsec", Class: "fmMacsec"},
	{Field: "netflow", Class: "fmNetflow"},
	{Field: "ngmvpn", Class: "fmNgmvpn"},
	{Field: "ngoam", Class: "fmNgoam"},
	{Field: "nv_overlay", Class: "fmNvo"},
	{Field: "nxapi", Class: "fmNxapi"},
	{Field: "ospf", Class: "fmOspf"},
	{Field: "ospfv3", Class: "fmOspfv3"},
	{Field: "pim", Class: "fmPim"},
	{Field: "private_vlan", Class: "fmPvlan"},
	{Field: "ptp", Class: "fmPtp"},
	{Field: "scp_server", Class: "fmScpServer"},
	{Field: "security_group", Class: "fmSecurityGroup"},
	{Field: "service_acceleration", Class: "fmServiceAcceleration"},
	{Field: "sflow", Class: "fmSflow"},
	{Field: "sftp_server", Class: "fmSftpServer"},
	{Field: "ssh", Class: "fmSsh"},
	{Field: "tacacs", Class: "fmTacacsplus"},
	{Field: "telemetry", Class: "fmTelemetry"},
	{Field: "telnet", Class: "fmTelnet"},
	{Field: "udld", Class: "fmUdld"},
	{Field: "vn_segment_vlan_based", Class: "fmVnSegment"},
	{Field: "vpc", Class: "fmVpc"},
}

var featureSetFields = []string{"fex", "mpls", "virtualization"}

// FeatureDMEMappings returns the supported NetAsCode feature leaf to DME class
// mapping.
func FeatureDMEMappings() []FeatureDMEMapping {
	out := make([]FeatureDMEMapping, len(featureDMEMappings))
	copy(out, featureDMEMappings)
	return out
}

// FeatureFields returns the supported NetAsCode feature leaves.
func FeatureFields() []string {
	out := make([]string, 0, len(featureDMEMappings))
	for _, mapping := range featureDMEMappings {
		out = append(out, mapping.Field)
	}
	return out
}

// FeatureSetFields returns the supported NetAsCode feature_set leaves.
func FeatureSetFields() []string {
	return append([]string(nil), featureSetFields...)
}

// FeatureDMEClasses returns the DME feature-management classes used by the
// NetAsCode feature family.
func FeatureDMEClasses() []string {
	classes := make([]string, 0, len(featureDMEMappings))
	for _, mapping := range featureDMEMappings {
		classes = append(classes, mapping.Class)
	}
	return classes
}

// FeatureSetDMEClasses returns the DME classes used by the NetAsCode
// feature_set family.
func FeatureSetDMEClasses() []string {
	return []string{"fsetFeatureSet"}
}

// FeatureDMEClassQuery returns the comma-separated class selector used by
// NX-API REST DME fetches.
func FeatureDMEClassQuery() string {
	classes := make([]string, 0, len(featureDMEMappings)+2)
	classes = append(classes, "fmEntity")
	for _, mapping := range featureDMEMappings {
		classes = append(classes, mapping.Class)
	}
	classes = append(classes, "fsetFeatureSet")
	return strings.Join(classes, ",")
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

// SourcePatternCoverage records NetAsCode envelope/source patterns that are
// resolved before family writers receive canonical per-device intent.
type SourcePatternCoverage struct {
	Pattern string
	State   CoverageState
	Notes   string
}

// CoverageMatrix records the NX-OS runtime coverage in descending readiness
// order: supported families first, then planned expansion, then deferred areas.
var CoverageMatrix = []FamilyCoverage{
	{
		Family:          FamilySystem,
		Section:         "device",
		State:           CoverageSupported,
		SupportedFields: []string{"system.hostname", "system.mtu"},
		UnsupportedFields: []string{
			"system.ethernet",
			"system.boot",
			"system.clock",
			"system.nxapi",
			"system.ssh",
		},
		Notes: "Maps hostname to DME topSystem name and MTU to ethpmInst.systemJumboMtu; verifies by reading sys.",
	},
	{
		Family:          FamilyFeature,
		Section:         "device",
		State:           CoverageSupported,
		SupportedFields: prefixFields("feature.", FeatureFields()),
		UnsupportedFields: []string{
			"feature.hmm",
			"feature.pvlan",
			"feature.vn_segment",
		},
		Notes: "Maps NetAsCode feature booleans to DME fmEntity feature-management children; disabling nxapi, ssh, scp_server, sftp_server, or tacacs is rejected to avoid management lockout.",
	},
	{
		Family:          FamilyFeatureSet,
		Section:         "device",
		State:           CoverageSupported,
		SupportedFields: prefixFields("feature_set.", FeatureSetFields()),
		Notes:           "Maps fex, mpls, and virtualization feature-set booleans to fsetFeatureSet adminSt.",
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
		Notes: "Maps to l2BD under sys/bd; VXLAN/EVPN leaves are intentionally excluded from the first slice. Prune deletes are supported for CR-owned VLANs except VLAN 1.",
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
	{Family: "aaa", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; requires secret-safe credential/reference handling."},
	{Family: "analytics", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family backed by the NX-OS analytics resource surface."},
	{Family: "arp", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; includes global and interface attachment leaves."},
	{Family: "banner", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; should be a low-risk management writer."},
	{Family: "bfd", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; routing dependencies need ordered verification."},
	{Family: "bgp", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; must coordinate VRF, route-policy, EVPN, and neighbor ownership."},
	{Family: "cdp", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; low-risk global/interface discovery settings."},
	{Family: "clock", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; should share management writer fixtures with NTP."},
	{Family: "community_list", Section: "device", State: CoveragePlanned, Notes: "NetAsCode policy family; dependency for route maps and BGP."},
	{Family: "dhcp", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; requires VRF/interface-aware ownership."},
	{Family: "dns", Section: "device", State: CoveragePlanned, Notes: "NetAsCode management family."},
	{Family: "evpn", Section: "device", State: CoveragePlanned, Notes: "Fabric/VXLAN wave dependency; implement with VLAN, NVE, BGP, and fabric policy ownership."},
	{Family: "fabric_forwarding", Section: "device", State: CoveragePlanned, Notes: "Fabric/VXLAN wave dependency; implement with VLAN, NVE, EVPN, and BGP."},
	{Family: "hsrp", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; depends on SVI and L3 interface support."},
	{Family: "hypershield", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; keep behind feature gates until lab coverage exists."},
	{Family: "ip_access_list", Section: "device", State: CoveragePlanned, Notes: "Policy family; must preserve entry ordering and sequence ownership."},
	{Family: "ip_prefix_list", Section: "device", State: CoveragePlanned, Notes: "Policy family; dependency for route maps and routing protocols."},
	{Family: "ip_route", Section: "device", State: CoveragePlanned, Notes: "Static route family; requires VRF-aware key ownership and prune tests."},
	{Family: "ipv6_access_list", Section: "device", State: CoveragePlanned, Notes: "Policy family; must preserve entry ordering and sequence ownership."},
	{Family: "ipv6_prefix_list", Section: "device", State: CoveragePlanned, Notes: "Policy family; dependency for route maps and routing protocols."},
	{Family: "ipv6_route", Section: "device", State: CoveragePlanned, Notes: "Static route family; requires VRF-aware key ownership and prune tests."},
	{Family: "isis", Section: "device", State: CoveragePlanned, Notes: "Routing family; depends on feature, interface, and route-policy primitives."},
	{Family: "key_chain", Section: "device", State: CoveragePlanned, Notes: "Authentication/policy dependency; requires secret redaction and reference handling."},
	{Family: "lldp", Section: "device", State: CoveragePlanned, Notes: "NetAsCode device family; low-risk global/interface discovery settings."},
	{Family: "logging", Section: "device", State: CoveragePlanned, Notes: "Low-risk management family for early DME expansion."},
	{Family: "nd", Section: "device", State: CoveragePlanned, Notes: "IPv6 neighbor discovery family; depends on IPv6 interface support."},
	{Family: "netflow", Section: "device", State: CoveragePlanned, Notes: "Telemetry-adjacent config family; keep separate from CVK runtime telemetry ingestion."},
	{Family: "ntp", Section: "device", State: CoveragePlanned, Notes: "Low-risk management family for early DME expansion."},
	{Family: "nxapi", Section: "device", State: CoveragePlanned, Notes: "Management plane family; must avoid disabling the active transport path."},
	{Family: "ospf", Section: "device", State: CoveragePlanned, Notes: "Routing family; depends on feature, VRF, interface, and route-policy primitives."},
	{Family: "ospfv3", Section: "device", State: CoveragePlanned, Notes: "Routing family; depends on IPv6 interface and route-policy primitives."},
	{Family: "pim", Section: "device", State: CoveragePlanned, Notes: "Multicast family; depends on routed interfaces and feature state."},
	{Family: "ptp", Section: "device", State: CoveragePlanned, Notes: "Management/timing family; includes interface attachments."},
	{Family: "qos", Section: "device", State: CoveragePlanned, Notes: "QoS family spanning default, network, and queuing policy resource surfaces."},
	{Family: "route_map", Section: "device", State: CoveragePlanned, Notes: "Policy family; dependency for routing protocols and redistribution."},
	{Family: "security_group", Section: "device", State: CoveragePlanned, Notes: "Policy/security family; keep gated until ownership and lab coverage are explicit."},
	{Family: "sflow", Section: "device", State: CoveragePlanned, Notes: "Telemetry-adjacent config family; keep separate from CVK runtime telemetry ingestion."},
	{Family: "snmp", Section: "device", State: CoveragePlanned, Notes: "Requires careful secret/reference handling before write support."},
	{Family: "span", Section: "device", State: CoveragePlanned, Notes: "Requires source/destination ownership checks to avoid traffic-impacting collisions."},
	{Family: "spanning_tree", Section: "device", State: CoveragePlanned, Notes: "L2 family; depends on VLAN/interface support."},
	{Family: "ssh", Section: "device", State: CoveragePlanned, Notes: "Management plane family; must avoid disabling active access."},
	{Family: "telemetry", Section: "device", State: CoveragePlanned, Notes: "Model-driven telemetry configuration; keep distinct from CVK telemetry collection."},
	{Family: "udld", Section: "device", State: CoveragePlanned, Notes: "L2 safety family; includes global and interface attachment leaves."},
	{Family: "vpc", Section: "device", State: CoveragePlanned, Notes: "Requires peer-aware validation and topology-aware rollout semantics."},
	{Family: "vrf", Section: "device", State: CoveragePlanned, Notes: "Foundational dependency for routed interfaces and routing families."},
	{Family: "interface_loopback", Section: "interface", State: CoveragePlanned, Notes: "Loopback identity and L3 attachment primitives."},
	{Family: "interface_management", Section: "interface", State: CoveragePlanned, Notes: "Management interface family; must avoid disrupting active access."},
	{Family: "interface_nve", Section: "interface", State: CoveragePlanned, Notes: "Fabric/VXLAN wave dependency; implement with VLAN, EVPN, and BGP."},
	{Family: "interface_port_channel", Section: "interface", State: CoveragePlanned, Notes: "Port-channel and member coordination must verify bundle state."},
	{Family: "interface_subinterface", Section: "interface", State: CoveragePlanned, Notes: "Subinterface support from the Terraform module; depends on Ethernet/port-channel L3 ownership."},
	{Family: "interface_vlan", Section: "interface", State: CoveragePlanned, Notes: "SVI addressing and shutdown state; requires DME mapping plus safe delete semantics."},
	{Family: "cli_templates", Section: "device", State: CoverageDeferred, Notes: "NetAsCode renders ordered CLI templates; NXOSConfig keeps config writes model/DME-first and DeviceOperation owns ad-hoc CLI execution."},
}

// SourcePatternMatrix records the NetAsCode NX-OS entity/source abstractions.
// The runtime supports resolved inline intent and a subset of the inventory
// envelope. Filesystem-oriented Terraform inputs stay outside the controller
// contract unless a future source controller renders them into resolved intent.
var SourcePatternMatrix = []SourcePatternCoverage{
	{Pattern: "nxos.devices[].configuration", State: CoverageSupported, Notes: "Per-device configuration is selected by deviceRef/name."},
	{Pattern: "nxos.device_groups[].configuration", State: CoverageSupported, Notes: "Device groups merge before the device-specific configuration."},
	{Pattern: "nxos.global.configuration", State: CoverageSupported, Notes: "Global configuration merges before matching device groups."},
	{Pattern: "nxos.variables", State: CoverageSupported, Notes: "Global, group, and device variables render ${name} placeholders."},
	{Pattern: "nxos.templates[type=model]", State: CoverageSupported, Notes: "Model templates merge by order before variable rendering."},
	{Pattern: "nxos.interface_groups", State: CoverageSupported, Notes: "Interface groups expand into interface entries before family normalization."},
	{Pattern: "interfaces.ethernets", State: CoverageSupported, Notes: "Normalized into the internal interface_ethernet family shape."},
	{Pattern: "managed_devices", State: CoveragePlanned, Notes: "Kubernetes reconciliation selects one device per CR today; fleet filtering belongs in a future source/orchestration layer."},
	{Pattern: "managed_device_groups", State: CoveragePlanned, Notes: "Kubernetes reconciliation selects one device per CR today; group filtering belongs in a future source/orchestration layer."},
	{Pattern: "yaml_files/yaml_directories", State: CoverageDeferred, Notes: "Terraform reads local files; CVK should consume rendered intent from a ConfigMap, GitOps, or source controller."},
	{Pattern: "template_files/template_directories", State: CoverageDeferred, Notes: "File templates require external rendering before reaching NXOSConfig."},
	{Pattern: "write_model_file", State: CoverageDeferred, Notes: "Terraform output artifact behavior; CVK records status/revisions instead of writing local files."},
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

// SourcePatternsByState returns NetAsCode source/envelope patterns by runtime
// coverage state.
func SourcePatternsByState(state CoverageState) []SourcePatternCoverage {
	out := make([]SourcePatternCoverage, 0, len(SourcePatternMatrix))
	for _, entry := range SourcePatternMatrix {
		if entry.State == state {
			out = append(out, entry)
		}
	}
	return out
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

func prefixFields(prefix string, fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, prefix+field)
	}
	return out
}

// FamilyOrder applies the basic parent-before-child ordering for the first
// NX-OS families: global system config, VLANs, then interfaces.
func FamilyOrder(in []string) []string {
	weight := map[string]int{
		FamilySystem:            0,
		FamilyFeature:           1,
		FamilyFeatureSet:        2,
		FamilyVLAN:              3,
		FamilyInterfaceEthernet: 4,
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
