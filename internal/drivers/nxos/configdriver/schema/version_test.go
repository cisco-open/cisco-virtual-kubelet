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

package schema

import (
	"strings"
	"testing"
)

func TestProfileForDeviceVersion(t *testing.T) {
	for _, tc := range []struct {
		version      string
		want         string
		experimental bool
	}{
		{version: "10.3(9)", want: "10.3(9)"},
		{version: "10.3(9)M", want: "10.3(9)"},
		{version: "10.5(4)", want: "10.5(4)", experimental: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			profile, err := ProfileForDeviceVersion(tc.version)
			if err != nil {
				t.Fatalf("ProfileForDeviceVersion: %v", err)
			}
			if profile.Release != tc.want || profile.ModelVersion != "0.3.0" || profile.Experimental != tc.experimental {
				t.Fatalf("profile=%+v", profile)
			}
		})
	}
}

func TestExperimentalDeviceVersionRequiresOptIn(t *testing.T) {
	t.Setenv(EnvAllowExperimentalReleases, "")
	err := ValidateDeviceVersion("10.5(4)")
	if !IsUnsupportedDeviceVersion(err) || !strings.Contains(err.Error(), EnvAllowExperimentalReleases) {
		t.Fatalf("disabled experimental error=%v", err)
	}
	if _, ok := SupportedDeviceVersionSet()["10.5(4)"]; ok {
		t.Fatal("experimental release present in default supported set")
	}

	t.Setenv(EnvAllowExperimentalReleases, "true")
	if err := ValidateDeviceVersion("10.5(4)"); err != nil {
		t.Fatalf("opted-in experimental version: %v", err)
	}
	if _, ok := SupportedDeviceVersionSet()["10.5(4)"]; !ok {
		t.Fatal("opted-in experimental release missing from supported set")
	}
}

func TestProfileForDeviceVersionFailsClosed(t *testing.T) {
	if _, err := ProfileForDeviceVersion("10.6(1)"); !IsUnsupportedDeviceVersion(err) {
		t.Fatalf("unsupported error=%v", err)
	}
	if _, err := ProfileForDeviceVersion("not-a-version"); !IsMalformedDeviceVersion(err) {
		t.Fatalf("malformed error=%v", err)
	}
}
