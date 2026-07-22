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
	"fmt"
	"regexp"
	"strings"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

var (
	immutableNXOSExporter = regexp.MustCompile(`^.+@(?:v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?|[0-9a-f]{40}|sha256:[0-9a-f]{64})$`)
	immutableNXOSRevision = regexp.MustCompile(`^(?:[0-9a-f]{40}|sha256:[0-9a-f]{64})$`)
)

type nxosNetAsCodeContract = nxosschema.NetAsCodeContract

var nxosNetAsCodeContracts = nxosschema.NetAsCodeContracts()

// validateNXOSNetAsCodeModelSource fails closed when an NXOSConfig declares
// NetAsCode provenance that cannot be tied to an exact conformance baseline.
// A nil source remains valid for native CVK-authored configuration. Once a CR
// opts into modelSource, all contract and audit-provenance fields are required.
func validateNXOSNetAsCodeModelSource(src *configv1alpha1.NetAsCodeModelSource) error {
	if src == nil {
		return nil
	}
	if src.Format != configv1alpha1.NetAsCodeModelFormatNXOS {
		return fmt.Errorf("format %q is not supported; want %q", src.Format, configv1alpha1.NetAsCodeModelFormatNXOS)
	}
	if !src.Resolved {
		return fmt.Errorf("resolved=false is not supported; export resolved per-device NetAsCode intent")
	}

	version := strings.TrimSpace(src.ModelVersion)
	if version == "" {
		return fmt.Errorf("modelVersion is required")
	}
	contract, ok := nxosNetAsCodeContracts[version]
	if !ok {
		return fmt.Errorf("modelVersion %q has no supported NX-OS conformance contract", src.ModelVersion)
	}
	if src.ModelVersion != version {
		return fmt.Errorf("modelVersion must not contain surrounding whitespace")
	}
	if src.SchemaDigest == "" {
		return fmt.Errorf("schemaDigest is required for modelVersion %q", version)
	}
	if src.SchemaDigest != contract.SchemaDigest {
		return fmt.Errorf("schemaDigest %q does not match modelVersion %q contract digest %q",
			src.SchemaDigest, version, contract.SchemaDigest)
	}
	exporter := strings.TrimSpace(src.Exporter)
	if exporter == "" {
		return fmt.Errorf("exporter is required for provenance")
	}
	if !immutableNXOSExporter.MatchString(exporter) {
		return fmt.Errorf("exporter must use name@semver, name@full-git-sha, or name@sha256:digest")
	}
	revision := strings.TrimSpace(src.SourceRevision)
	if revision == "" {
		return fmt.Errorf("sourceRevision is required for provenance")
	}
	if !immutableNXOSRevision.MatchString(revision) {
		return fmt.Errorf("sourceRevision must be a full 40-character Git SHA or sha256:digest")
	}
	return nil
}

func validateNXOSModelDevicePair(src *configv1alpha1.NetAsCodeModelSource, deviceVersion string) error {
	if src == nil || strings.TrimSpace(deviceVersion) == "" {
		return nil
	}
	profile, err := nxosschema.ProfileForDeviceVersion(deviceVersion)
	if err != nil {
		return err
	}
	if src.ModelVersion != profile.ModelVersion {
		return fmt.Errorf("modelVersion %q does not match NX-OS %q qualified modelVersion %q",
			src.ModelVersion, deviceVersion, profile.ModelVersion)
	}
	return nil
}

func validateNXOSTargetVersion(targetVersion, deviceVersion string) error {
	targetVersion = strings.TrimSpace(targetVersion)
	if targetVersion == "" || strings.TrimSpace(deviceVersion) == "" {
		return nil
	}
	profile, err := nxosschema.ProfileForDeviceVersion(deviceVersion)
	if err != nil {
		return err
	}
	if targetVersion != profile.Release {
		return fmt.Errorf("%q does not match live NX-OS %q release profile %q",
			targetVersion, deviceVersion, profile.Release)
	}
	return nil
}

// validateResolvedNXOSNetAsCodeSource protects the provenance boundary. A
// payload marked resolved must already be a single-device canonical model; CVK
// must not silently become a second implementation of NetAsCode inheritance.
func validateResolvedNXOSNetAsCodeSource(config map[string]any, _ string) error {
	for _, key := range []string{
		"nxos", "global", "devices", "device_groups", "templates", "variables", "interface_groups", "configuration",
		"managed_devices", "managed_device_groups", "yaml_files", "yaml_directories", "template_files", "template_directories", "write_model_file", "cli_templates",
	} {
		if _, ok := config[key]; ok {
			return fmt.Errorf("top-level %q requires NetAsCode expansion; export flattened per-device configuration", key)
		}
	}
	if _, ok := config["interface_ethernet"]; ok {
		return fmt.Errorf("top-level %q is a CVK runtime shape; use canonical interfaces.ethernets", "interface_ethernet")
	}
	if path, ok := unresolvedNXOSConstruct(config, ""); ok {
		return fmt.Errorf("unresolved construct at %s", path)
	}
	for _, family := range []string{"feature", "feature_set"} {
		raw, present := config[family]
		if !present {
			continue
		}
		leaves, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be a mapping", family)
		}
		for key, value := range leaves {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s.%s must be boolean in the NetAsCode NX-OS model, got %T", family, key, value)
			}
		}
	}
	return nil
}

func unresolvedNXOSConstruct(value any, path string) (string, bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			switch key {
			case "interface_groups", "device_groups", "templates", "variables",
				"managed_devices", "managed_device_groups", "yaml_files", "yaml_directories", "template_files", "template_directories", "write_model_file", "cli_templates":
				return childPath, true
			}
			if found, ok := unresolvedNXOSConstruct(child, childPath); ok {
				return found, true
			}
		}
	case []any:
		for i, child := range current {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if found, ok := unresolvedNXOSConstruct(child, childPath); ok {
				return found, true
			}
		}
	case string:
		if strings.Contains(current, "${") {
			return path, true
		}
	}
	return "", false
}
