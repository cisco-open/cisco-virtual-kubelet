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
	"testing"
)

// TestAllSchemasPopulatedForEveryFamily pins the contract that every
// registered, non-skeleton writer has an extractable FamilySchema.
// A family registered with real code but no schema entry is a bug the
// lint and docs tools should not silently ignore.
func TestAllSchemasPopulatedForEveryFamily(t *testing.T) {
	schemas := AllSchemas()

	// Combined Phase-1/2/3 list from registry_test.
	want := append([]string(nil), phase1Families...)
	want = append(want, phase2Families...)
	want = append(want, phase3Families...)

	for _, fam := range want {
		s, ok := schemas[fam]
		if !ok {
			t.Errorf("family %q has no Schema()", fam)
			continue
		}
		if s.Family != fam {
			t.Errorf("family %q: Schema.Family=%q", fam, s.Family)
		}
		if s.Shape != "singleton" && s.Shape != "keyed_list" {
			t.Errorf("family %q: Schema.Shape=%q", fam, s.Shape)
		}
		if len(s.ManagedLeaves) == 0 {
			t.Errorf("family %q: Schema.ManagedLeaves is empty", fam)
		}
	}
}

// TestSchemaSkeletonReportsNotFound guards the distinction between
// 'family unknown' and 'family registered with a real writer': a
// skeleton is registered in the registry but has no schema to
// expose, and Schema() must reflect that.
func TestSchemaSkeletonReportsNotFound(t *testing.T) {
	name := "_schema_skeleton_probe_"
	registerSkeleton(name, "/Cisco-IOS-XE-native:native/test")
	t.Cleanup(func() {
		mu.Lock()
		delete(registry, name)
		mu.Unlock()
	})
	if _, ok := Schema(name); ok {
		t.Errorf("Schema(skeleton) returned ok=true")
	}
}

// TestSchemaUnknownFamilyReportsNotFound confirms the zero-value
// fallback path.
func TestSchemaUnknownFamilyReportsNotFound(t *testing.T) {
	if _, ok := Schema("not-a-registered-family"); ok {
		t.Errorf("Schema(unknown) returned ok=true")
	}
}
