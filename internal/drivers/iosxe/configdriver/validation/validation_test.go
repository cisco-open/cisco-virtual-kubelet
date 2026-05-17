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

package validation

import (
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Mode
	}{
		{"", ModeDisabled},
		{"disabled", ModeDisabled},
		{"off", ModeDisabled},
		{"warn", ModeWarn},
		{"warning", ModeWarn},
		{"strict", ModeStrict},
		{"true", ModeStrict},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseMode(tc.in)
			if err != nil {
				t.Fatalf("ParseMode(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseMode(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if _, err := ParseMode("maybe"); err == nil {
		t.Fatal("ParseMode(maybe) returned nil error")
	}
}

func TestStructuralValidatorAllowsIOSXE2601IPDomainShape(t *testing.T) {
	v := NewStructuralValidator()
	err := v.ValidateOperation(Context{
		Family:       "ip_domain",
		ReleaseTag:   "2601",
		AllowedPaths: []string{"/Cisco-IOS-XE-native:native/ip/domain"},
	}, transport.Op{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/ip/domain",
		Body: []byte(`{"Cisco-IOS-XE-native:domain":{"name-container":{"name-no-vrf":"example.com"},"lookup":true}}`),
	})
	if err != nil {
		t.Fatalf("ValidateOperation: %v", err)
	}
}

func TestStructuralValidatorRejectsCanonicalIPDomainNameOnIOSXE2601(t *testing.T) {
	v := NewStructuralValidator()
	err := v.ValidateOperation(Context{
		Family:       "ip_domain",
		ReleaseTag:   "2601",
		AllowedPaths: []string{"/Cisco-IOS-XE-native:native/ip/domain"},
	}, transport.Op{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/ip/domain",
		Body: []byte(`{"Cisco-IOS-XE-native:domain":{"name":"example.com"}}`),
	})
	if err == nil {
		t.Fatal("ValidateOperation returned nil; expected canonical name rejection")
	}
}

func TestStructuralValidatorAllowsCanonicalIPDomainNameOnIOSXE1718(t *testing.T) {
	v := NewStructuralValidator()
	err := v.ValidateOperation(Context{
		Family:       "ip_domain",
		ReleaseTag:   "1718",
		AllowedPaths: []string{"/Cisco-IOS-XE-native:native/ip/domain"},
	}, transport.Op{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/ip/domain",
		Body: []byte(`{"Cisco-IOS-XE-native:domain":{"name":"example.com"}}`),
	})
	if err != nil {
		t.Fatalf("ValidateOperation: %v", err)
	}
}

func TestStructuralValidatorRejectsPathOutsideWriterScope(t *testing.T) {
	v := NewStructuralValidator()
	err := v.ValidateOperation(Context{
		Family:       "ip_domain",
		ReleaseTag:   "1718",
		AllowedPaths: []string{"/Cisco-IOS-XE-native:native/ip/domain"},
	}, transport.Op{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/hostname",
		Body: []byte(`{"Cisco-IOS-XE-native:hostname":"edge-01"}`),
	})
	if err == nil {
		t.Fatal("ValidateOperation returned nil; expected path-scope rejection")
	}
}
