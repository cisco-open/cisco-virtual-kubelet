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

package provider

import (
	"strings"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestValidateNXOSNetAsCodeModelSource(t *testing.T) {
	t.Parallel()

	valid := func() *configv1alpha1.NetAsCodeModelSource {
		contract := nxosNetAsCodeContracts["0.3.0"]
		return &configv1alpha1.NetAsCodeModelSource{
			Format:         configv1alpha1.NetAsCodeModelFormatNXOS,
			ModelVersion:   contract.ModelVersion,
			SchemaDigest:   contract.SchemaDigest,
			Resolved:       true,
			Exporter:       "test-exporter@1.0.0",
			SourceRevision: "0123456789abcdef0123456789abcdef01234567",
		}
	}

	tests := []struct {
		name    string
		mutate  func(*configv1alpha1.NetAsCodeModelSource)
		wantErr string
	}{
		{name: "exact contract"},
		{name: "wrong format", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.Format = configv1alpha1.NetAsCodeModelFormatIOSXE
		}, wantErr: "format"},
		{name: "unresolved", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.Resolved = false
		}, wantErr: "resolved=false"},
		{name: "missing version", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.ModelVersion = ""
		}, wantErr: "modelVersion is required"},
		{name: "unsupported version", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.ModelVersion = "0.4.0"
		}, wantErr: "no supported NX-OS conformance contract"},
		{name: "version whitespace", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.ModelVersion = " 0.3.0 "
		}, wantErr: "must not contain surrounding whitespace"},
		{name: "missing digest", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.SchemaDigest = ""
		}, wantErr: "schemaDigest is required"},
		{name: "wrong digest", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.SchemaDigest = "sha256:" + strings.Repeat("0", 64)
		}, wantErr: "does not match"},
		{name: "missing exporter", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.Exporter = " "
		}, wantErr: "exporter is required"},
		{name: "mutable exporter name", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.Exporter = "test-exporter"
		}, wantErr: "name@semver"},
		{name: "mutable exporter alias", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.Exporter = "test-exporter@latest"
		}, wantErr: "name@semver"},
		{name: "missing source revision", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.SourceRevision = ""
		}, wantErr: "sourceRevision is required"},
		{name: "mutable source revision", mutate: func(src *configv1alpha1.NetAsCodeModelSource) {
			src.SourceRevision = "main"
		}, wantErr: "full 40-character Git SHA"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := valid()
			if tt.mutate != nil {
				tt.mutate(src)
			}
			err := validateNXOSNetAsCodeModelSource(src)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateNXOSNetAsCodeModelSource() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateNXOSNetAsCodeModelSource() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateResolvedNXOSNetAsCodeSource(t *testing.T) {
	t.Parallel()
	valid := map[string]any{
		"system": map[string]any{"hostname": "leaf-01"},
		"interfaces": map[string]any{"ethernets": []any{
			map[string]any{"id": "1/1", "description": "uplink"},
		}},
	}
	if err := validateResolvedNXOSNetAsCodeSource(valid, "leaf-01"); err != nil {
		t.Fatalf("flattened source: %v", err)
	}
	for name, source := range map[string]map[string]any{
		"envelope": {"nxos": map[string]any{"devices": []any{}}},
		"variable": {"system": map[string]any{"hostname": "${hostname}"}},
		"group ref": {"interfaces": map[string]any{"ethernets": []any{
			map[string]any{"id": "1/1", "interface_groups": []any{"uplinks"}},
		}}},
		"runtime alias":                {"interface_ethernet": map[string]any{"interfaces": []any{}}},
		"feature string extension":     {"feature": map[string]any{"lldp": "enabled"}},
		"feature set string extension": {"feature_set": map[string]any{"virtualization": "installed"}},
		"managed device selector":      {"managed_devices": []any{"leaf-01"}, "system": map[string]any{"hostname": "leaf-01"}},
		"cli template":                 {"system": map[string]any{"cli_templates": []any{"bootstrap"}}},
		"template directory":           {"template_directories": []any{"templates"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateResolvedNXOSNetAsCodeSource(source, "leaf-01"); err == nil {
				t.Fatal("validateResolvedNXOSNetAsCodeSource error=nil")
			}
		})
	}
}

func TestValidateNXOSModelDevicePair(t *testing.T) {
	t.Parallel()
	src := &configv1alpha1.NetAsCodeModelSource{ModelVersion: "0.3.0"}
	if err := validateNXOSModelDevicePair(src, "10.5(4)"); err != nil {
		t.Fatalf("qualified pair: %v", err)
	}
	bad := *src
	bad.ModelVersion = "0.2.0"
	if err := validateNXOSModelDevicePair(&bad, "10.5(4)"); err == nil {
		t.Fatal("mismatched pair error=nil")
	}
}

func TestValidateNXOSTargetVersion(t *testing.T) {
	t.Parallel()
	if err := validateNXOSTargetVersion("10.3(9)", "10.3(9)M"); err != nil {
		t.Fatalf("matching profile: %v", err)
	}
	if err := validateNXOSTargetVersion("10.5(4)", "10.3(9)"); err == nil {
		t.Fatal("mismatched target/live release error=nil")
	}
}

func TestValidateNXOSNetAsCodeModelSourceAllowsNativeSource(t *testing.T) {
	t.Parallel()
	if err := validateNXOSNetAsCodeModelSource(nil); err != nil {
		t.Fatalf("native source: %v", err)
	}
}

func TestNXOSNetAsCodeContractPinsUpstreamProvenance(t *testing.T) {
	t.Parallel()
	contract := nxosNetAsCodeContracts["0.3.0"]
	if contract.ModuleRevision == "" || contract.SchemaRevision == "" ||
		contract.ProviderVersion == "" || contract.ProviderRevision == "" {
		t.Fatalf("contract provenance is incomplete: %+v", contract)
	}
	if !strings.HasPrefix(contract.SchemaDigest, "sha256:") || len(contract.SchemaDigest) != len("sha256:")+64 {
		t.Fatalf("schema digest is not canonical SHA-256: %q", contract.SchemaDigest)
	}
}
