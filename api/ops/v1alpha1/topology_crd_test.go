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

package v1alpha1

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func TestTopologyAwarenessCRDsPassAPIServerStaticValidation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(scheme); err != nil {
		t.Fatalf("add internal apiextensions scheme: %v", err)
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1 apiextensions scheme: %v", err)
	}

	for _, name := range []string{
		"cisco.vk_ciscodevices.yaml",
		"ops.cisco.vk_iosxesoftwareupgrades.yaml",
		"ops.cisco.vk_iosxesoftwarerollouts.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "config", "crd", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read generated CRD %s: %v", path, err)
			}
			var external apiextensionsv1.CustomResourceDefinition
			if err := yaml.Unmarshal(raw, &external); err != nil {
				t.Fatalf("parse generated CRD: %v", err)
			}
			var internal apiextensions.CustomResourceDefinition
			if err := scheme.Convert(&external, &internal, nil); err != nil {
				t.Fatalf("convert CRD for API-server validation: %v", err)
			}
			if len(external.Spec.Versions) != 1 {
				t.Fatalf("served versions = %d, want 1", len(external.Spec.Versions))
			}
			internal.Status.StoredVersions = []string{external.Spec.Versions[0].Name}
			if errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) != 0 {
				t.Fatalf("API-server static validation failed: %v", errs.ToAggregate())
			}
		})
	}
}

func TestRolloutCRDPublishesHardSafetyBounds(t *testing.T) {
	path := filepath.Join("..", "..", "..", "config", "crd", "ops.cisco.vk_iosxesoftwarerollouts.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rollout CRD: %v", err)
	}
	// controller-gen wraps long CEL rules in YAML. Normalize presentation
	// whitespace so this assertion follows the parsed expression rather than
	// one generator line-break choice.
	text := strings.Join(strings.Fields(string(raw)), " ")
	for _, required := range []string{
		"maximum: 100",
		"maxItems: 100",
		"maximum: 262144",
		"maximum: 256",
		"maxItems: 16",
		"set a non-empty selector, or set allowAll=true with",
		"plan is immutable; create a new rollout to change executable",
		"approval is append-only and immutable once recorded",
		"cancellation is terminal",
		"!has(oldSelf.cancel) || !oldSelf.cancel || (has(self.cancel) && self.cancel)",
		"(!has(self.pause) || !self.pause) && (!has(self.cancel) || !self.cancel)",
		"- Reload",
		"- BlockIfRunning",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("generated rollout CRD is missing contract fragment %q", required)
		}
	}
}

func TestUpgradeCRDPublishesHasSafeManagedControl(t *testing.T) {
	path := filepath.Join("..", "..", "..", "config", "crd", "ops.cisco.vk_iosxesoftwareupgrades.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade CRD: %v", err)
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	if required := "!has(oldSelf.cancel) || !oldSelf.cancel || (has(self.cancel) && self.cancel)"; !strings.Contains(text, required) {
		t.Fatalf("generated upgrade CRD is missing has-safe terminal cancellation rule %q", required)
	}
}
