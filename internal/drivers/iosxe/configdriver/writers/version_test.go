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
	"os"
	"testing"
)

// TestMain sets a default device version for the writer test suite.
// All existing tests were developed against C9300 17.18.2, so the
// version is set accordingly. Individual tests may override.
func TestMain(m *testing.M) {
	SetDeviceVersion("17.18.2")
	os.Exit(m.Run())
}

func TestParseVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in          string
		wantMajor   int
		wantMinor   int
	}{
		{"17.16.01a", 17, 16},
		{"17.18.2", 17, 18},
		{"17.15.03", 17, 15},
		{"", 0, 0},
		{"17", 0, 0},
		{"17.", 17, 0},
	}
	for _, tc := range cases {
		maj, min := parseVersion(tc.in)
		if maj != tc.wantMajor || min != tc.wantMinor {
			t.Errorf("parseVersion(%q) = (%d, %d), want (%d, %d)",
				tc.in, maj, min, tc.wantMajor, tc.wantMinor)
		}
	}
}

func TestDeviceVersionAtLeast(t *testing.T) {
	t.Parallel()
	SetDeviceVersion("17.16.01a")
	defer SetDeviceVersion("17.18.2") // restore for other tests

	if DeviceVersionAtLeast(17, 18) {
		t.Error("17.16 should not be >= 17.18")
	}
	if !DeviceVersionAtLeast(17, 16) {
		t.Error("17.16 should be >= 17.16")
	}
	if !DeviceVersionAtLeast(17, 15) {
		t.Error("17.16 should be >= 17.15")
	}
	if DeviceVersionAtLeast(18, 0) {
		t.Error("17.16 should not be >= 18.0")
	}
}
