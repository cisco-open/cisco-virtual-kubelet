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
