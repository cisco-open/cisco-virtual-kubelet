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

package diagnostic

import (
	"strings"
	"testing"
)

func TestRedactCommonShapes(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		expectHide []string // substrings the output must NOT contain
		expectKeep []string // substrings the output MUST contain
		wantDid    bool
	}{
		{
			name: "enable secret hashed",
			input: `interface Loopback0
 ip address 10.0.0.1 255.255.255.255
enable secret 5 $1$abcd$xyz
hostname cat9k`,
			expectHide: []string{"$1$abcd$xyz"},
			expectKeep: []string{"interface Loopback0", "hostname cat9k"},
			wantDid:    true,
		},
		{
			name:       "username password line",
			input:      "username admin password 7 12345abcde",
			expectHide: []string{"12345abcde"},
			wantDid:    true,
		},
		{
			name:       "snmp community string",
			input:      "snmp-server community public RO\nshow whatever",
			expectHide: []string{"public RO"},
			expectKeep: []string{"show whatever"},
			wantDid:    true,
		},
		{
			name:       "radius shared key",
			input:      "radius-server key 7 abcdef\nradius-server host 10.0.0.5",
			expectHide: []string{"abcdef"},
			wantDid:    true,
		},
		{
			name:       "tacacs key + host",
			input:      "tacacs-server key supersecret\ntacacs-server host 10.0.0.6",
			expectHide: []string{"supersecret", "10.0.0.6"},
			wantDid:    true,
		},
		{
			name:       "no secrets — no redaction",
			input:      "interface Loopback0\n description telemetry\n",
			expectKeep: []string{"interface Loopback0", "description telemetry"},
			wantDid:    false,
		},
		{
			name:       "leading whitespace preserved",
			input:      "  enable secret 5 $1$xx$yy",
			expectHide: []string{"$1$xx$yy"},
			expectKeep: []string{"  <redacted"},
			wantDid:    true,
		},
		{
			name:       "case insensitive",
			input:      "ENABLE SECRET 5 $1$X$Y",
			expectHide: []string{"$1$X$Y"},
			wantDid:    true,
		},
		{
			// Cisco IOS-XE inserts `privilege <N>` between username
			// and secret/password — caught by the live-device retest
			// of test 2.
			name:       "username with privilege then secret",
			input:      "username cisco privilege 15 secret 9 $14$abcd$xyz",
			expectHide: []string{"$14$abcd$xyz"},
			wantDid:    true,
		},
		{
			name:       "username with privilege then password",
			input:      "username admin privilege 15 password 7 deadbeef",
			expectHide: []string{"deadbeef"},
			wantDid:    true,
		},
		{
			// Indented `key <token>` line under a `tacacs server`
			// stanza — also caught by the test 2 retest.
			name:       "indented key line",
			input:      "tacacs server ux-kgs\n key tacacs123",
			expectHide: []string{"tacacs123"},
			expectKeep: []string{"<redacted"},
			wantDid:    true,
		},
		{
			name:       "key 7 hex form",
			input:      " key 7 020005f1abcd",
			expectHide: []string{"020005f1abcd"},
			wantDid:    true,
		},
		// Wave 10 release-readiness P1 fixes (2026-04-28).
		{
			name:       "line-vty-bare-password",
			input:      "line vty 0 4\n password ww\n transport input ssh",
			expectHide: []string{" password ww"},
			expectKeep: []string{"line vty 0 4", "transport input ssh"},
			wantDid:    true,
		},
		{
			name:       "line-console-bare-password",
			input:      "line con 0\n password 7 094F471A1A0A\n logging synchronous",
			expectHide: []string{"094F471A1A0A"},
			wantDid:    true,
		},
		{
			name:       "isis-domain-password",
			input:      "router isis\n net 49.0000.0001.0100.1001.00\n domain-password cisco\n metric-style transition",
			expectHide: []string{"domain-password cisco"},
			expectKeep: []string{"router isis", "metric-style transition"},
			wantDid:    true,
		},
		{
			name:       "isis-area-password",
			input:      "router isis\n area-password areakey42\n is-type level-2-only",
			expectHide: []string{"areakey42"},
			wantDid:    true,
		},
		{
			name:       "ospf-message-digest-key",
			input:      "interface GigabitEthernet1/0/1\n ip ospf message-digest-key 1 md5 ospfsecret\n ip ospf authentication message-digest",
			expectHide: []string{"ospfsecret"},
			wantDid:    true,
		},
		{
			name:       "ospf-authentication-key",
			input:      "interface GigabitEthernet1/0/1\n ip ospf authentication-key plainospfkey",
			expectHide: []string{"plainospfkey"},
			wantDid:    true,
		},
		{
			name:       "bgp-neighbor-password",
			input:      "router bgp 65000\n neighbor 192.168.1.1 password 7 020005f1abcd\n neighbor 192.168.1.1 remote-as 65001",
			expectHide: []string{"020005f1abcd"},
			expectKeep: []string{"router bgp 65000", "remote-as 65001"},
			wantDid:    true,
		},
		{
			name:       "narrative-password-not-redacted",
			input:      `! comment about the password rotation policy below`,
			expectKeep: []string{"comment about"},
			wantDid:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, did := Redact(tc.input)
			if did != tc.wantDid {
				t.Errorf("didRedact=%v want %v", did, tc.wantDid)
			}
			for _, h := range tc.expectHide {
				if strings.Contains(got, h) {
					t.Errorf("expected %q to be redacted; got:\n%s", h, got)
				}
			}
			for _, k := range tc.expectKeep {
				if !strings.Contains(got, k) {
					t.Errorf("expected %q to remain; got:\n%s", k, got)
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("a-very-long-line\n", 200)
	out, truncated := Truncate(long, 1024)
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if len(out) > 1024 {
		t.Errorf("clipped length %d exceeds budget %d", len(out), 1024)
	}
	if !strings.Contains(out, "<truncated by cisco-vk") {
		t.Errorf("expected truncation marker in output")
	}

	short := "hello\nworld\n"
	out, truncated = Truncate(short, 1024)
	if truncated {
		t.Error("expected truncated=false for short input")
	}
	if out != short {
		t.Error("short input mutated unexpectedly")
	}

	out, truncated = Truncate(short, 0)
	if truncated || out != short {
		t.Error("maxBytes=0 should disable truncation")
	}
}

func TestTruncatePreservesLineBoundary(t *testing.T) {
	// Each line is 11 bytes including \n. Asking for 100 bytes means
	// we should clip at the last \n on or before byte 100 minus the
	// trailer length.
	src := strings.Repeat("0123456789\n", 50) // 550 bytes, perfectly line-aligned
	out, truncated := Truncate(src, 100)
	if !truncated {
		t.Fatal("expected truncation")
	}
	// The body before the trailer must end at a \n boundary (no
	// mid-line cuts).
	body := strings.SplitN(out, "\n... <truncated", 2)[0]
	if !strings.HasSuffix(body, "9") {
		// "9" is the last char before each \n in our fixture; the cut
		// should land exactly there.
		t.Errorf("expected line-boundary cut; got body ending: %q",
			body[len(body)-3:])
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"  ", 0},
		{"100", 100},
		{"100B", 100},
		{"64KiB", 64 * 1024},
		{"1MiB", 1024 * 1024},
		{"oops", -1},
		{"100Q", -1},
	}
	for _, tc := range cases {
		got := ParseSize(tc.in)
		if got != tc.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
