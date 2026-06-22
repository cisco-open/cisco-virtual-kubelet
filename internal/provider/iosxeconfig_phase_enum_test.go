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

// Wave 9.1 schema-aware guard for external-review-wave8-followup
// Finding #1. Every engine phase the reconciler writes to
// IOSXEConfig.status.phase must be present in the generated CRD's
// status.phase enum, otherwise a real apiserver rejects the status
// update with a name-validation-style error and the operator sees
// the lease-blocked path as a status-update failure rather than the
// intended transient phase. fake.Client doesn't enforce CRD enums,
// so unit suites against the fake pass even when the enum is missing
// a phase. This test parses the generated CRD YAML and asserts every
// phase that may land on status.phase is enumerated.

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
)

// statusBoundPhases enumerates the engine phase constants that may
// appear in IOSXEConfig.status.phase. Transitional phases that the
// engine declares but never assigns to Result.Phase
// (PhaseValidating, PhasePlanning, PhaseApplying, PhaseVerifying)
// are kept in the CRD enum for forward compatibility but are not
// listed here — this test guards what actually ships to apiserver.
//
// Add an entry here when the engine starts writing a new phase. The
// CRD enum guard below will fail the build if the kubebuilder
// marker isn't updated to match.
func statusBoundPhases() []string {
	return []string{
		engine.PhasePending,
		engine.PhaseInSync,
		engine.PhaseDrifted,
		engine.PhaseFailed,
		engine.PhasePaused,
		engine.PhaseLeaseBlocked,
	}
}

// TestCRDEnumIncludesEveryStatusBoundEnginePhase parses the
// generated CRD and asserts every status-bound engine phase is
// present in the enum. Pre-fix this test would have failed:
// PhaseLeaseBlocked landed on status.phase from Wave 8.2 onwards
// but the kubebuilder marker still listed only the original nine
// phases.
func TestCRDEnumIncludesEveryStatusBoundEnginePhase(t *testing.T) {
	t.Parallel()
	crdPath := findRepoFile(t, filepath.Join("config", "crd", "config.cisco.vk_iosxeconfigs.yaml"))
	enum := loadPhaseEnum(t, crdPath)

	have := map[string]struct{}{}
	for _, p := range enum {
		have[p] = struct{}{}
	}
	for _, want := range statusBoundPhases() {
		if _, ok := have[want]; !ok {
			t.Errorf(
				"engine phase %q is written to IOSXEConfig.status.phase but missing from CRD enum %v in %s",
				want, enum, crdPath)
		}
	}
}

// findRepoFile walks up from the test's CWD until it finds rel.
// Tests run from the package directory; the generated CRD lives at
// the repo root. Walking up is more robust than a hard-coded
// "../../../.." that breaks if the package moves.
func findRepoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate %s above %s", rel, dir)
		}
		dir = parent
	}
}

// loadPhaseEnum extracts spec.versions[0].schema.openAPIV3Schema.
// properties.status.properties.phase.enum from the CRD. Returned as
// a string slice in declaration order. Failures here mean the CRD
// shape changed — re-check the path against the generator output
// before changing the test.
func loadPhaseEnum(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Status struct {
								Properties struct {
									Phase struct {
										Enum []string `json:"enum"`
									} `json:"phase"`
								} `json:"properties"`
							} `json:"status"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	if len(doc.Spec.Versions) == 0 {
		t.Fatalf("CRD has no versions: %s", path)
	}
	enum := doc.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Status.Properties.Phase.Enum
	if len(enum) == 0 {
		t.Fatalf("phase.enum empty in %s — generator may have changed shape", path)
	}
	return enum
}
