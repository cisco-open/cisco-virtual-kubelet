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

// TestMain validates the default device version used by the writer
// test suite. Version-specific state is per resolver, not global.
func TestMain(m *testing.M) {
	if err := SetDeviceVersion("17.18.2"); err != nil {
		panic("TestMain: SetDeviceVersion: " + err.Error())
	}
	os.Exit(m.Run())
}

// mustSetDeviceVersion is a test helper that fails the test on
// SetDeviceVersion error. Centralises the boilerplate so individual
// tests stay readable.
func mustSetDeviceVersion(t *testing.T, ver string) {
	t.Helper()
	if err := SetDeviceVersion(ver); err != nil {
		t.Fatalf("SetDeviceVersion(%q): %v", ver, err)
	}
}

func TestParseVersionStrict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in        string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{"17.16.01a", 17, 16, true},
		{"17.18.2", 17, 18, true},
		{"17.15", 17, 15, true},
		{"", 0, 0, false},
		{"17", 0, 0, false},
		{"17.", 0, 0, false},
		{".17", 0, 0, false},
		{"abc.def", 0, 0, false},
	}
	for _, tc := range cases {
		maj, min, ok := parseVersionStrict(tc.in)
		if ok != tc.wantOK || (ok && (maj != tc.wantMajor || min != tc.wantMinor)) {
			t.Errorf("parseVersionStrict(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tc.in, maj, min, ok, tc.wantMajor, tc.wantMinor, tc.wantOK)
		}
	}
}

func TestSetDeviceVersionRejectsMalformed(t *testing.T) {
	// Cannot t.Parallel — touches the package global on success and
	// must restore via TestMain's default of 17.18.2.
	defer func() {
		if err := SetDeviceVersion("17.18.2"); err != nil {
			t.Fatalf("restore: %v", err)
		}
	}()
	for _, in := range []string{"", "17", "17.", "abc", ".17"} {
		if err := SetDeviceVersion(in); err == nil {
			t.Errorf("SetDeviceVersion(%q) returned nil; expected error", in)
		}
	}
}

func TestSetDeviceVersionRejectsUnsupported(t *testing.T) {
	defer func() {
		if err := SetDeviceVersion("17.18.2"); err != nil {
			t.Fatalf("restore: %v", err)
		}
	}()
	// Versions that parse cleanly but are not in the supported map.
	// 17.21 / 17.24 are real Cisco releases we have not validated yet.
	cases := []string{"17.21.1", "17.24.0", "18.0", "16.12.5"}
	for _, in := range cases {
		err := SetDeviceVersion(in)
		if err == nil {
			t.Errorf("SetDeviceVersion(%q) returned nil; expected ErrUnsupportedDeviceVersion", in)
			continue
		}
		if !IsUnsupportedDeviceVersion(err) {
			t.Errorf("SetDeviceVersion(%q) = %v (%T); expected ErrUnsupportedDeviceVersion", in, err, err)
		}
	}
}

func TestReleaseTagForDeviceVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ver  string
		tag  string
		want bool
	}{
		{"17.9.1", "1791", true},
		{"17.15.03", "1715", true},
		{"17.16.01a", "1716", true},
		{"17.18.2", "1718", true},
		{"17.21.0", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		tag, ok := ReleaseTagForDeviceVersionString(tc.ver)
		if ok != tc.want || tag != tc.tag {
			t.Errorf("ReleaseTagForDeviceVersionString(%q) = (%q, %v), want (%q, %v)",
				tc.ver, tag, ok, tc.tag, tc.want)
		}
	}
}

func TestDeviceVersionAtLeast(t *testing.T) {
	t.Parallel()
	r, err := NewOverrideResolver("17.16.01a")
	if err != nil {
		t.Fatalf("NewOverrideResolver: %v", err)
	}
	if r.DeviceVersionAtLeast(17, 18) {
		t.Error("17.16 should not be >= 17.18")
	}
	if !r.DeviceVersionAtLeast(17, 16) {
		t.Error("17.16 should be >= 17.16")
	}
	if !r.DeviceVersionAtLeast(17, 15) {
		t.Error("17.16 should be >= 17.15")
	}
	if r.DeviceVersionAtLeast(18, 0) {
		t.Error("17.16 should not be >= 18.0")
	}
}
