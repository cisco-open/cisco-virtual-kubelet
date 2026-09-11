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
	"encoding/json"
	"strings"
	"testing"
)

func TestUpgradeImageSourceIntentJSON(t *testing.T) {
	validSHA := strings.Repeat("a", 64)
	tests := []struct {
		name   string
		source UpgradeImageSource
		want   string
	}{
		{
			name:   "preinstalled marker remains present",
			source: UpgradeImageSource{Preinstalled: &PreinstalledImageSource{}},
			want:   `{"preinstalled":{}}`,
		},
		{
			name: "device file carries authenticated path",
			source: UpgradeImageSource{DeviceFile: &DeviceFileImageSource{
				Path: "flash:cat9k.bin", SHA256: validSHA,
			}},
			want: `{"deviceFile":{"path":"flash:cat9k.bin","sha256":"` + validSHA + `"}}`,
		},
		{
			name:   "legacy local path remains wire compatible",
			source: UpgradeImageSource{LocalPath: "flash:cat9k.bin"},
			want:   `{"localPath":"flash:cat9k.bin"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.source)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("Marshal() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestUpgradeImageSourceDeepCopyDoesNotAliasIntent(t *testing.T) {
	original := &UpgradeImageSource{
		DeviceFile: &DeviceFileImageSource{
			Path:   "flash:cat9k.bin",
			SHA256: strings.Repeat("a", 64),
		},
	}
	copy := original.DeepCopy()
	copy.DeviceFile.Path = "bootflash:other.bin"

	if original.DeviceFile.Path != "flash:cat9k.bin" {
		t.Fatalf("DeepCopy() aliased DeviceFile: original path = %q", original.DeviceFile.Path)
	}
}

func TestIOSXESoftwareUpgradeLegacyWireFieldsRemainCompatible(t *testing.T) {
	legacy := []byte(`{"spec":{"resumePolicy":"Abort","maxRetries":7},"status":{"phase":"Cancelled","retryCount":3}}`)
	var upgrade IOSXESoftwareUpgrade
	if err := json.Unmarshal(legacy, &upgrade); err != nil {
		t.Fatalf("Unmarshal() legacy object error = %v", err)
	}
	if upgrade.Spec.ResumePolicy != "Abort" || upgrade.Spec.MaxRetries != 7 {
		t.Fatalf("legacy spec fields lost: %+v", upgrade.Spec)
	}
	if upgrade.Status.Phase != UpgradePhaseCancelled || upgrade.Status.RetryCount != 3 {
		t.Fatalf("legacy status fields lost: %+v", upgrade.Status)
	}

	roundTrip, err := json.Marshal(&upgrade)
	if err != nil {
		t.Fatalf("Marshal() legacy object error = %v", err)
	}
	for _, field := range []string{`"resumePolicy":"Abort"`, `"maxRetries":7`, `"phase":"Cancelled"`, `"retryCount":3`} {
		if !strings.Contains(string(roundTrip), field) {
			t.Fatalf("Marshal() = %s, want retained field %s", roundTrip, field)
		}
	}

	withoutLegacyFields, err := json.Marshal(IOSXESoftwareUpgradeSpec{})
	if err != nil {
		t.Fatalf("Marshal() empty spec error = %v", err)
	}
	if strings.Contains(string(withoutLegacyFields), "resumePolicy") || strings.Contains(string(withoutLegacyFields), "maxRetries") {
		t.Fatalf("empty spec unexpectedly emits deprecated fields: %s", withoutLegacyFields)
	}
}
