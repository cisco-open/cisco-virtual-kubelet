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
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
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
			path := generatedOpsCRDPath(name)
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
	path := generatedOpsCRDPath("ops.cisco.vk_iosxesoftwarerollouts.yaml")
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
	path := generatedOpsCRDPath("ops.cisco.vk_iosxesoftwareupgrades.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade CRD: %v", err)
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	for _, required := range []string{
		"!has(oldSelf.cancel) || !oldSelf.cancel || (has(self.cancel) && self.cancel)",
		"recoveryDeadline is append-only and may advance only in Recovering with a newer audited control revision",
		"self.controlRevision > oldSelf.controlRevision && self.updatedAt > oldSelf.updatedAt",
		"worker configuration revision may change only with a strictly newer inventory observation",
		"worker drain inventory evidence may change only with a strictly newer inventory revision",
		"self.inventoryRevision > oldSelf.inventoryRevision || self == oldSelf",
		"self.observedWorkerConfigRevision == oldSelf.observedWorkerConfigRevision || (self.inventoryRevision > oldSelf.inventoryRevision && self.inventoryObservedAt > oldSelf.inventoryObservedAt && self.updatedAt > oldSelf.updatedAt)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("generated upgrade CRD is missing managed safety rule %q", required)
		}
	}
}

func TestWorkerDrainCRDRequiresNewRevisionForInventoryChanges(t *testing.T) {
	path := generatedOpsCRDPath("ops.cisco.vk_iosxesoftwareupgrades.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parse upgrade CRD: %v", err)
	}
	workerDrain := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.
		Properties["status"].Properties["workerDrain"]
	var schema apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&workerDrain, &schema, nil); err != nil {
		t.Fatalf("convert workerDrain schema: %v", err)
	}
	structural, err := structuralschema.NewStructural(&schema)
	if err != nil {
		t.Fatalf("build workerDrain structural schema: %v", err)
	}
	validator := cel.NewValidator(structural, false, 10_000_000)
	if validator == nil {
		t.Fatal("workerDrain CRD lacks transition validation")
	}

	workerDrainStatus := func() map[string]interface{} {
		return map[string]interface{}{
			"protocolVersion": "pdb-drain-v1", "observedSessionToken": "11111111-1111-4111-8111-111111111111",
			"observedPolicyEpoch": int64(1), "observedControlRevision": int64(0),
			"observedWorkerConfigRevision": "sha256:" + strings.Repeat("a", 64),
			"inventoryRevision":            int64(7), "inventoryObservedAt": "2026-09-12T10:00:00Z",
			"inventoryComplete": true, "remainingAuthorizedPodUIDs": []interface{}{"pod-uid"},
			"foreignDeviceWorkloadCount": int64(0), "unknownDeviceWorkloadCount": int64(0),
			"updatedAt": "2026-09-12T10:00:00Z", "reason": "InventoryComplete", "message": "complete inventory",
		}
	}
	for _, test := range []struct {
		name    string
		mutate  func(map[string]interface{})
		allowed bool
	}{
		{name: "unchanged record supports worker-control rollover", allowed: true},
		{name: "new revision may publish new evidence", allowed: true, mutate: func(current map[string]interface{}) {
			current["inventoryRevision"] = int64(8)
			current["inventoryObservedAt"] = "2026-09-12T10:01:00Z"
			current["updatedAt"] = "2026-09-12T10:01:00Z"
			current["inventoryComplete"] = false
			current["unknownDeviceWorkloadCount"] = int64(1)
		}},
		{name: "same revision completeness change", mutate: func(current map[string]interface{}) {
			current["inventoryComplete"] = false
		}},
		{name: "same revision UID set change", mutate: func(current map[string]interface{}) {
			current["remainingAuthorizedPodUIDs"] = []interface{}{}
		}},
		{name: "same revision count change", mutate: func(current map[string]interface{}) {
			current["foreignDeviceWorkloadCount"] = int64(1)
		}},
		{name: "same revision reason change", mutate: func(current map[string]interface{}) {
			current["reason"] = "Different"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			old, current := workerDrainStatus(), workerDrainStatus()
			if test.mutate != nil {
				test.mutate(current)
			}
			errs, _ := validator.Validate(context.Background(), field.NewPath("status", "workerDrain"), structural, current, old, 10_000_000)
			if (len(errs) == 0) != test.allowed {
				t.Fatalf("allowed=%v validation=%v", test.allowed, errs)
			}
		})
	}
}

func generatedOpsCRDPath(name string) string {
	if override := os.Getenv("CVK_TEST_CRD_DIR"); override != "" {
		return filepath.Join(override, name)
	}
	return filepath.Join("..", "..", "..", "config", "crd", name)
}
