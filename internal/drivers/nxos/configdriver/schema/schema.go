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
