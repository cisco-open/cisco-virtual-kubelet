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

package controlleradapter

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestControllerFormatExtensibilityDoesNotBroadenDeviceCRDs(t *testing.T) {
	deviceCRDs := []string{
		"../../config/crd/config.cisco.vk_iosxeconfigs.yaml",
		"../../config/crd/config.cisco.vk_nxosconfigs.yaml",
		"../../config/crd/config.cisco.vk_iosxeconfigbundles.yaml",
	}
	for _, path := range deviceCRDs {
		t.Run(path, func(t *testing.T) {
			formats := modelSourceFormatSchemas(t, path)
			if len(formats) == 0 {
				t.Fatal("CRD has no modelSource.format schema")
			}
			for _, format := range formats {
				if got := stringSlice(format["enum"]); !reflect.DeepEqual(got, []string{"netascode-iosxe", "netascode-nxos"}) {
					t.Fatalf("device model format enum=%v", got)
				}
				if _, open := format["pattern"]; open {
					t.Fatalf("device model format unexpectedly uses open pattern: %v", format)
				}
			}
		})
	}

	formats := modelSourceFormatSchemas(t, "../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	if len(formats) != 1 || formats[0]["pattern"] != `^netascode-[a-z0-9]+(-[a-z0-9]+)*$` {
		t.Fatalf("controller model format schema=%v", formats)
	}
	if _, closed := formats[0]["enum"]; closed {
		t.Fatalf("controller model format must remain extensible: %v", formats[0])
	}
}

func TestSourceUnionAdmissionIsScopedToNetworkControllerConfig(t *testing.T) {
	deviceCRDs := []string{
		"../../config/crd/config.cisco.vk_iosxeconfigs.yaml",
		"../../config/crd/config.cisco.vk_nxosconfigs.yaml",
		"../../config/crd/config.cisco.vk_iosxeconfigbundles.yaml",
	}
	for _, path := range deviceCRDs {
		sources := configurationSourceSchemas(t, path)
		if len(sources) == 0 {
			t.Fatalf("existing device CRD %s has no ConfigurationSource schema", path)
		}
		for _, source := range sources {
			if _, changed := source["x-kubernetes-validations"]; changed {
				t.Fatalf("existing device CRD %s acquired source-level admission validation: %v", path, source["x-kubernetes-validations"])
			}
		}
	}

	const controllerPath = "../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml"
	controllerSources := configurationSourceSchemas(t, controllerPath)
	if len(controllerSources) != 1 {
		t.Fatalf("NetworkControllerConfig source schemas=%d, want 1", len(controllerSources))
	}
	controllerSourceProperties, _ := controllerSources[0]["properties"].(map[string]any)
	inlineSchema, _ := controllerSourceProperties["inline"].(map[string]any)
	if description := fmt.Sprint(inlineSchema["description"]); strings.Contains(strings.ToLower(description), "iosxe") {
		t.Fatalf("controller inline source schema leaks device-specific documentation: %q", description)
	}
	found := false
	for _, validation := range validationRules(t, controllerPath) {
		if validation["rule"] == "has(self.source.inline) != has(self.source.configMapRef)" &&
			validation["message"] == "exactly one of source.inline or source.configMapRef must be set" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("NetworkControllerConfig CRD is missing its spec-scoped exactly-one source admission rule")
	}
}

func configurationSourceSchemas(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []map[string]any
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if properties, ok := typed["properties"].(map[string]any); ok {
				if source, ok := properties["source"].(map[string]any); ok {
					if sourceProperties, ok := source["properties"].(map[string]any); ok {
						_, hasInline := sourceProperties["inline"]
						_, hasConfigMapRef := sourceProperties["configMapRef"]
						if hasInline && hasConfigMapRef {
							out = append(out, source)
						}
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
	return out
}

func validationRules(t *testing.T, path string) []map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []map[string]string
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if rules, ok := typed["x-kubernetes-validations"].([]any); ok {
				for _, item := range rules {
					rule, ok := item.(map[string]any)
					if !ok {
						continue
					}
					out = append(out, map[string]string{
						"rule":    fmt.Sprint(rule["rule"]),
						"message": fmt.Sprint(rule["message"]),
					})
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
	return out
}

func modelSourceFormatSchemas(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CRD: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse CRD: %v", err)
	}
	var out []map[string]any
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if properties, ok := typed["properties"].(map[string]any); ok {
				if modelSource, ok := properties["modelSource"].(map[string]any); ok {
					if modelProperties, ok := modelSource["properties"].(map[string]any); ok {
						if format, ok := modelProperties["format"].(map[string]any); ok {
							out = append(out, format)
						}
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
	return out
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil
		}
		out = append(out, text)
	}
	return out
}
