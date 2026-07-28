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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/validation"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	"sigs.k8s.io/yaml"
)

const (
	pinnedNetAsCodeOracleVersion = "0.3.0"
	pinnedSchemaNormalization    = "jq -cS '.properties.nxos' schema.json, including jq's trailing LF"
	pinnedTerraformVersion       = "1.9.8"
	pinnedOracleMethod           = "Pinned Terraform plan followed by the pinned NX-OS provider toBody implementations"
	pinnedOracleNormalization    = "DME fact comparison ignores empty attributes objects and operation batching"
)

func TestSupportedWriterOperationsPassStrictStructuralConformance(t *testing.T) {
	tests := []struct {
		family   string
		desired  any
		observed any
	}{
		{"system", map[string]any{"hostname": "leaf-01", "mtu": 9216}, map[string]any{"hostname": "old", "mtu": 1500}},
		{"feature", map[string]any{"lldp": true}, map[string]any{"lldp": false}},
		{"feature_set", map[string]any{"fex": true}, map[string]any{"fex": false}},
		{"vlan", map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "cvk_probe"}}}, map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "old"}}}},
		{"interface_ethernet", map[string]any{"interfaces": []any{map[string]any{"id": "1/1", "description": "uplink"}}}, map[string]any{"interfaces": []any{map[string]any{"id": "1/1", "description": "old"}}}},
	}

	validator := validation.NewStructuralValidator()
	for _, tc := range tests {
		t.Run(tc.family, func(t *testing.T) {
			writer := GetForRelease(tc.family, "10.3(9)")
			ops, err := writer.Diff(tc.desired, tc.observed)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if len(ops) == 0 {
				t.Fatal("Diff returned no operations for changed intent")
			}
			scope := enginewriters.ScopeOf(writer)
			for i, op := range ops {
				if err := validator.ValidateOperation(validation.Context{
					Platform: "nxos", Family: tc.family, DeviceVersion: "10.3(9)",
					ModelVersion: "0.3.0", AllowedWritePrefixes: scope.WritePrefixes,
				}, op); err != nil {
					t.Fatalf("op[%d] failed strict conformance: %v", i, err)
				}
			}
		})
	}
}

// TestPinnedNetAsCodeOracleArtifactDigests makes accidental fixture drift
// visible. Deliberate oracle refreshes update both the artifacts and
// SHA256SUMS in one reviewed change; modifying, adding, or removing any
// artifact without updating the pin fails this test.
func TestPinnedNetAsCodeOracleArtifactDigests(t *testing.T) {
	root := filepath.Join("testdata", "nxos", pinnedNetAsCodeOracleVersion)
	manifestPath := filepath.Join(root, "SHA256SUMS")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		t.Fatalf("inspect pinned oracle digest manifest: %v", err)
	}
	if !manifestInfo.Mode().IsRegular() {
		t.Fatalf("pinned oracle digest manifest is not a regular file")
	}
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read pinned oracle digest manifest: %v", err)
	}

	entries := map[string]string{}
	previousName := ""
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(rawManifest)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("SHA256SUMS line %d must contain digest and file name, got %q", lineNumber+1, line)
		}
		digest, name := fields[0], fields[1]
		if filepath.Base(name) != name {
			t.Fatalf("SHA256SUMS line %d contains non-local artifact name %q", lineNumber+1, name)
		}
		if previousName != "" && name <= previousName {
			t.Fatalf("SHA256SUMS entries must be unique and sorted: %q follows %q", name, previousName)
		}
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			t.Fatalf("SHA256SUMS line %d has invalid SHA-256 %q", lineNumber+1, digest)
		}
		entries[name] = digest
		previousName = name
	}

	directoryEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read pinned oracle directory: %v", err)
	}
	var artifacts []string
	for _, entry := range directoryEntries {
		if entry.Name() == filepath.Base(manifestPath) {
			continue
		}
		if entry.IsDir() {
			t.Fatalf("pinned oracle directory contains unmanifested nested directory %q", entry.Name())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			t.Fatalf("pinned oracle artifact %q is a symbolic link", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("inspect pinned oracle artifact %q: %v", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("pinned oracle artifact %q is not a regular file", entry.Name())
		}
		artifacts = append(artifacts, entry.Name())
	}
	sort.Strings(artifacts)
	if len(artifacts) != len(entries) {
		t.Fatalf("pinned oracle artifact count=%d, digest entries=%d; artifacts=%v", len(artifacts), len(entries), artifacts)
	}
	for _, name := range artifacts {
		want, ok := entries[name]
		if !ok {
			t.Fatalf("pinned oracle artifact %q has no SHA256SUMS entry", name)
		}
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read pinned oracle artifact %q: %v", name, err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(raw))
		if got != want {
			t.Fatalf("pinned oracle artifact %q digest changed\nwant %s\ngot  %s", name, want, got)
		}
	}
	for name := range entries {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("SHA256SUMS references missing oracle artifact %q: %v", name, err)
		}
	}
}

type providerDMEOracle struct {
	Operations []struct {
		Resource string         `json:"resource"`
		Path     string         `json:"path"`
		Body     map[string]any `json:"body"`
	} `json:"operations"`
}

type goldenContract struct {
	ModelVersion string `json:"modelVersion"`
	Module       struct {
		Source   string `json:"source"`
		Version  string `json:"version"`
		Revision string `json:"revision"`
	} `json:"module"`
	Schema struct {
		Source        string `json:"source"`
		Revision      string `json:"revision"`
		NXOSDigest    string `json:"nxosDigest"`
		Normalization string `json:"normalization"`
	} `json:"schema"`
	Providers map[string]struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
	} `json:"providers"`
	Oracle struct {
		TerraformVersion string `json:"terraformVersion"`
		Managed          *bool  `json:"managed"`
		Refresh          *bool  `json:"refresh"`
		Method           string `json:"method"`
		Normalization    string `json:"normalization"`
	} `json:"oracle"`
}

// TestNetAsCodeProviderGoldenParity compares semantic DME facts rather than
// JSON bytes. The pinned provider includes empty attributes objects and emits
// feature/feature_set in one operation, whereas CVK intentionally omits empty
// objects and applies the two ownership families separately. Paths, class
// ancestry, object identity, attributes, and values must still be identical.
func TestNetAsCodeProviderGoldenParity(t *testing.T) {
	root := filepath.Join("testdata", "nxos", pinnedNetAsCodeOracleVersion)
	for _, name := range []string{"contract.json", "canonical.yaml", "resolved.json", "provider-plan.json", "provider-dme.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("required pinned oracle artifact %s: %v", name, err)
		}
	}

	var resolved map[string]any
	readJSONFixture(t, filepath.Join(root, "resolved.json"), &resolved)
	assertGoldenContract(t, filepath.Join(root, "contract.json"))
	assertCanonicalMatchesResolved(t, filepath.Join(root, "canonical.yaml"), resolved)
	assertProviderPlanProjection(t, filepath.Join(root, "provider-plan.json"))
	var oracle providerDMEOracle
	readJSONFixture(t, filepath.Join(root, "provider-dme.json"), &oracle)

	interfaceRoot, ok := resolved["interfaces"].(map[string]any)
	if !ok {
		t.Fatalf("resolved interfaces has type %T", resolved["interfaces"])
	}
	desired := map[string]any{
		"system":             resolved["system"],
		"feature":            resolved["feature"],
		"feature_set":        resolved["feature_set"],
		"vlan":               resolved["vlan"],
		"interface_ethernet": map[string]any{"interfaces": interfaceRoot["ethernets"]},
	}

	gotFacts := map[string][]string{}
	familyResources := map[string]string{
		"system":             "nxos_system",
		"feature":            "nxos_feature",
		"feature_set":        "nxos_feature",
		"vlan":               "nxos_bridge_domain",
		"interface_ethernet": "nxos_physical_interface",
	}
	for _, family := range []string{"system", "feature", "feature_set", "vlan", "interface_ethernet"} {
		writer := GetForRelease(family, "10.3(9)")
		ops, err := enginewriters.Diff(enginewriters.DiffContext{
			Platform: "nxos", DeviceVersion: "10.3(9)", ModelVersion: "0.3.0",
		}, writer, desired[family], nil)
		if err != nil {
			t.Fatalf("%s Diff: %v", family, err)
		}
		if len(ops) == 0 {
			t.Fatalf("%s Diff returned no create operation", family)
		}
		resource := familyResources[family]
		gotFacts[resource] = append(gotFacts[resource], dmeFactsFromOps(t, ops)...)
	}
	for resource := range gotFacts {
		sort.Strings(gotFacts[resource])
	}
	wantFacts := dmeFactsFromOracleByResource(oracle)
	if !reflect.DeepEqual(gotFacts, wantFacts) {
		t.Fatalf(
			"CVK DME output differs from pinned provider oracle by resource ownership\nwant:\n%s\ngot:\n%s",
			formatDMEFactsByResource(wantFacts),
			formatDMEFactsByResource(gotFacts),
		)
	}
}

func assertGoldenContract(t *testing.T, path string) {
	t.Helper()
	var golden goldenContract
	readJSONFixture(t, path, &golden)
	runtimeContract, ok := nxosschema.NetAsCodeContractForVersion(golden.ModelVersion)
	if !ok {
		t.Fatalf("golden modelVersion %q has no runtime contract", golden.ModelVersion)
	}
	if golden.ModelVersion != pinnedNetAsCodeOracleVersion {
		t.Errorf("model version fixture=%q pinned=%q", golden.ModelVersion, pinnedNetAsCodeOracleVersion)
	}
	provider := golden.Providers["CiscoDevNet/nxos"]
	utils := golden.Providers["netascode/utils"]
	checks := map[string][2]string{
		"runtime model version":  {golden.ModelVersion, runtimeContract.ModelVersion},
		"module source":          {golden.Module.Source, runtimeContract.ModuleSource},
		"module version":         {golden.Module.Version, pinnedNetAsCodeOracleVersion},
		"module revision":        {golden.Module.Revision, runtimeContract.ModuleRevision},
		"schema source":          {golden.Schema.Source, runtimeContract.SchemaSource},
		"schema revision":        {golden.Schema.Revision, runtimeContract.SchemaRevision},
		"schema digest":          {golden.Schema.NXOSDigest, runtimeContract.SchemaDigest},
		"schema normalization":   {golden.Schema.Normalization, pinnedSchemaNormalization},
		"provider source":        {"registry.terraform.io/CiscoDevNet/nxos", runtimeContract.ProviderSource},
		"provider version":       {provider.Version, runtimeContract.ProviderVersion},
		"provider revision":      {provider.Revision, runtimeContract.ProviderRevision},
		"utils provider source":  {"registry.terraform.io/netascode/utils", runtimeContract.UtilsProviderSource},
		"utils provider version": {utils.Version, runtimeContract.UtilsProviderVersion},
		"terraform version":      {golden.Oracle.TerraformVersion, pinnedTerraformVersion},
		"oracle method":          {golden.Oracle.Method, pinnedOracleMethod},
		"oracle normalization":   {golden.Oracle.Normalization, pinnedOracleNormalization},
	}
	for name, pair := range checks {
		if pair[0] != pair[1] {
			t.Errorf("%s fixture=%q runtime=%q", name, pair[0], pair[1])
		}
	}
	providerNames := make([]string, 0, len(golden.Providers))
	for name := range golden.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	if want := []string{"CiscoDevNet/nxos", "netascode/utils"}; !reflect.DeepEqual(providerNames, want) {
		t.Errorf("provider identities=%v, want %v", providerNames, want)
	}
	if golden.Oracle.Managed == nil || *golden.Oracle.Managed {
		t.Errorf("oracle managed=%v, want explicit false", golden.Oracle.Managed)
	}
	if golden.Oracle.Refresh == nil || *golden.Oracle.Refresh {
		t.Errorf("oracle refresh=%v, want explicit false", golden.Oracle.Refresh)
	}
}

func assertCanonicalMatchesResolved(t *testing.T, path string, resolved map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	jsonRaw, err := yaml.YAMLToJSON(raw)
	if err != nil {
		t.Fatalf("convert %s to JSON: %v", path, err)
	}
	var canonical map[string]any
	if err := json.Unmarshal(jsonRaw, &canonical); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if !reflect.DeepEqual(canonical, resolved) {
		t.Fatalf("canonical.yaml and resolved.json differ\ncanonical=%#v\nresolved=%#v", canonical, resolved)
	}
}

func assertProviderPlanProjection(t *testing.T, path string) {
	t.Helper()
	var got map[string]any
	readJSONFixture(t, path, &got)
	want := map[string]any{
		"nxos_system": map[string]any{"name": "leaf-golden", "ethernet_mtu": float64(9216)},
		"nxos_feature": map[string]any{
			"lldp": true, "hmm": true, "pvlan": true, "vn_segment": true,
			"feature_sets": map[string]any{"fex": "enabled", "mpls": "disabled", "virtualization": "enabled"},
		},
		"nxos_bridge_domain": map[string]any{
			"bridge_domains": map[string]any{"vlan-101": map[string]any{"name": "cvk-golden"}},
		},
		"nxos_physical_interface": map[string]any{
			"physical_interfaces": map[string]any{"eth1/1": map[string]any{
				"admin_state": "down", "description": "golden-uplink", "layer": "Layer2",
				"mtu": float64(9216), "user_configured_flags": "admin_layer,admin_mtu,admin_state",
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provider plan projection changed\nwant=%#v\ngot=%#v", want, got)
	}
}

func readJSONFixture(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func dmeFactsFromOps(t *testing.T, ops []transport.Op) []string {
	t.Helper()
	var facts []string
	for i, op := range ops {
		if op.Verb != transport.VerbMerge {
			t.Fatalf("op[%d] verb=%s, want MERGE", i, op.Verb)
		}
		var body map[string]any
		if err := json.Unmarshal(op.Body, &body); err != nil {
			t.Fatalf("op[%d] body: %v", i, err)
		}
		appendDMEFacts(&facts, op.Path, nil, body)
	}
	sort.Strings(facts)
	return facts
}

func dmeFactsFromOracleByResource(oracle providerDMEOracle) map[string][]string {
	facts := map[string][]string{}
	for _, op := range oracle.Operations {
		resourceFacts := facts[op.Resource]
		appendDMEFacts(&resourceFacts, op.Path, nil, op.Body)
		facts[op.Resource] = resourceFacts
	}
	for resource := range facts {
		sort.Strings(facts[resource])
	}
	return facts
}

func formatDMEFactsByResource(facts map[string][]string) string {
	resources := make([]string, 0, len(facts))
	for resource := range facts {
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	var lines []string
	for _, resource := range resources {
		lines = append(lines, "["+resource+"]")
		lines = append(lines, facts[resource]...)
	}
	return strings.Join(lines, "\n")
}

func appendDMEFacts(facts *[]string, dn string, ancestry []string, node map[string]any) {
	classes := make([]string, 0, len(node))
	for class := range node {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		object, ok := node[class].(map[string]any)
		if !ok {
			continue
		}
		attributes, _ := object["attributes"].(map[string]any)
		segment := class + dmeIdentity(attributes)
		current := append(append([]string(nil), ancestry...), segment)
		attributeNames := make([]string, 0, len(attributes))
		for name := range attributes {
			attributeNames = append(attributeNames, name)
		}
		sort.Strings(attributeNames)
		for _, name := range attributeNames {
			*facts = append(*facts, fmt.Sprintf("%s|%s|%s=%v", dn, strings.Join(current, "/"), name, attributes[name]))
		}
		children, _ := object["children"].([]any)
		for _, rawChild := range children {
			child, ok := rawChild.(map[string]any)
			if ok {
				appendDMEFacts(facts, dn, current, child)
			}
		}
	}
}

func dmeIdentity(attributes map[string]any) string {
	for _, name := range []string{"id", "fabEncap", "name"} {
		if value, ok := attributes[name]; ok {
			return fmt.Sprintf("[%s=%v]", name, value)
		}
	}
	return ""
}
