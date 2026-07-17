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

package schema

// NetAsCodeContract pins the upstream evidence used by the strict NX-OS model
// import and by the offline writer oracle. Terraform remains a conformance
// oracle; it is not part of the controller runtime.
type NetAsCodeContract struct {
	ModelVersion         string
	SchemaDigest         string
	ModuleSource         string
	ModuleRevision       string
	SchemaSource         string
	SchemaRevision       string
	ProviderSource       string
	ProviderVersion      string
	ProviderRevision     string
	UtilsProviderSource  string
	UtilsProviderVersion string
}

var netAsCodeContracts = map[string]NetAsCodeContract{
	"0.3.0": {
		ModelVersion: "0.3.0",
		// SHA-256 of `jq -cS '.properties.nxos' schema.json`, including
		// jq's trailing LF, at SchemaRevision.
		SchemaDigest:         "sha256:5d5482679fb28e751d34cdc49342f8434914a7714966ba8244923b95d678698d",
		ModuleSource:         "github.com/netascode/terraform-nxos-nac-nxos",
		ModuleRevision:       "706c1b390b7c23f8950714788129b1c51233de6a",
		SchemaSource:         "github.com/netascode/schema",
		SchemaRevision:       "9e45ad51227a2e534c5ded8f3258c4feb9a53c5d",
		ProviderSource:       "registry.terraform.io/CiscoDevNet/nxos",
		ProviderVersion:      "0.13.1",
		ProviderRevision:     "fe753243f20325b41bbff594a8d311596cd439d6",
		UtilsProviderSource:  "registry.terraform.io/netascode/utils",
		UtilsProviderVersion: "2.0.0",
	},
}

// NetAsCodeContractForVersion returns the immutable upstream contract for a
// public model version.
func NetAsCodeContractForVersion(version string) (NetAsCodeContract, bool) {
	contract, ok := netAsCodeContracts[version]
	return contract, ok
}

// NetAsCodeContracts returns a copy for validation/tests without exposing the
// package-owned map to mutation.
func NetAsCodeContracts() map[string]NetAsCodeContract {
	out := make(map[string]NetAsCodeContract, len(netAsCodeContracts))
	for version, contract := range netAsCodeContracts {
		out[version] = contract
	}
	return out
}
