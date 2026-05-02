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

package main

import (
	"strings"
	"testing"
)

func TestParseExecArgs(t *testing.T) {
	cases := []struct {
		name      string
		argv      []string
		wantErr   string
		wantDev   string
		wantNS    string
		wantAllow bool
		wantTrunc int
		wantCmds  []string
	}{
		{
			name:      "minimal",
			argv:      []string{"cat9k-smoke", "--", "show", "ip", "route"},
			wantDev:   "cat9k-smoke",
			wantCmds:  []string{"show ip route"},
			wantTrunc: 64 * 1024,
		},
		{
			name:      "with namespace",
			argv:      []string{"cat9k-smoke", "-n", "cisco-vk-smoke", "--", "show", "version"},
			wantDev:   "cat9k-smoke",
			wantNS:    "cisco-vk-smoke",
			wantCmds:  []string{"show version"},
			wantTrunc: 64 * 1024,
		},
		{
			name:      "allow secrets + truncate",
			argv:      []string{"cat9k-smoke", "--allow-secrets", "--truncate-bytes", "1024", "--", "show", "running-config"},
			wantDev:   "cat9k-smoke",
			wantAllow: true,
			wantTrunc: 1024,
			wantCmds:  []string{"show running-config"},
		},
		{
			name:    "no command",
			argv:    []string{"cat9k-smoke"},
			wantErr: "missing command",
		},
		{
			name:    "no device",
			argv:    []string{"--", "show", "version"},
			wantErr: "missing device",
		},
		{
			name:    "rejects reload",
			argv:    []string{"cat9k-smoke", "--", "reload"},
			wantErr: "destructive command",
		},
		{
			name:    "rejects clear",
			argv:    []string{"cat9k-smoke", "--", "clear", "ip", "ospf", "process"},
			wantErr: "destructive command",
		},
		{
			name:    "rejects write erase",
			argv:    []string{"cat9k-smoke", "--", "write", "erase"},
			wantErr: "destructive command",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parseExecArgs(tc.argv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if f.device != tc.wantDev {
				t.Errorf("device=%q want %q", f.device, tc.wantDev)
			}
			if f.namespace != tc.wantNS {
				t.Errorf("namespace=%q want %q", f.namespace, tc.wantNS)
			}
			if f.allowSecrets != tc.wantAllow {
				t.Errorf("allowSecrets=%v want %v", f.allowSecrets, tc.wantAllow)
			}
			if f.truncateB != tc.wantTrunc {
				t.Errorf("truncateB=%d want %d", f.truncateB, tc.wantTrunc)
			}
			if !equalStrings(f.commands, tc.wantCmds) {
				t.Errorf("commands=%v want %v", f.commands, tc.wantCmds)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseForwardingPort(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"hello": 0,
		"Forwarding from 127.0.0.1:51234 -> 8082": 51234,
		"Forwarding from [::1]:51235 -> 8082":     0, // we only match IPv4
		"127.0.0.1:9999":                          9999,
		"Forwarding from 127.0.0.1:0 -> 8082":     0, // 0 is a sentinel
	}
	for in, want := range cases {
		got := parseForwardingPort(in)
		if got != want {
			t.Errorf("parseForwardingPort(%q)=%d want %d", in, got, want)
		}
	}
}
