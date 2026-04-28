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

package transport

import "testing"

// TestIsCiscoPrompt covers the prompt patterns the SSH-CLI helper
// matches against to decide where one command's output ends.
func TestIsCiscoPrompt(t *testing.T) {
	yes := []string{
		"Edge-9K#",
		"Edge-9K# ",
		"Edge-9K(config)#",
		"Edge-9K(config-if)#",
		"cat9k-smoke>",
		"R1>",
		"router-9300#",
		"r1.lab-1#",
	}
	no := []string{
		"",
		"random text",
		"show ip route",
		"Cisco IOS XE Software, Version 17.18.2",
		"  Edge-9K#",   // leading whitespace — caller trims first
		"E*dge#",       // illegal char
	}
	for _, line := range yes {
		if !isCiscoPrompt(line) {
			t.Errorf("expected prompt match: %q", line)
		}
	}
	for _, line := range no {
		if isCiscoPrompt(line) {
			t.Errorf("expected non-prompt: %q", line)
		}
	}
}

// TestTrimEchoAndPrompt verifies the post-processing strips the
// command echo (first line) and the trailing prompt while keeping
// the show output intact.
func TestTrimEchoAndPrompt(t *testing.T) {
	cases := []struct {
		name string
		body string
		cmd  string
		want string
	}{
		{
			name: "echo-then-output-then-prompt",
			body: "show version\r\nCisco IOS XE Software, Version 17.18.2\r\nROM: ...\r\nEdge-9K#",
			cmd:  "show version",
			want: "Cisco IOS XE Software, Version 17.18.2\nROM: ...",
		},
		{
			name: "no echo (some servers don't echo)",
			body: "Cisco IOS XE Software, Version 17.18.2\nEdge-9K#",
			cmd:  "show version",
			want: "Cisco IOS XE Software, Version 17.18.2",
		},
		{
			name: "no trailing prompt",
			body: "show version\nCisco IOS XE Software, Version 17.18.2\n",
			cmd:  "show version",
			want: "Cisco IOS XE Software, Version 17.18.2",
		},
		{
			name: "multi-line output preserved",
			body: "show ip ospf neighbor\nNeighbor ID  Pri  State    Dead Time  Address\n10.0.0.2     1    FULL/DR  00:00:38   10.1.1.2\nEdge-9K#",
			cmd:  "show ip ospf neighbor",
			want: "Neighbor ID  Pri  State    Dead Time  Address\n10.0.0.2     1    FULL/DR  00:00:38   10.1.1.2",
		},
		{
			name: "empty output",
			body: "show running-config | section nothing\nEdge-9K#",
			cmd:  "show running-config | section nothing",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trimEchoAndPrompt(tc.body, tc.cmd)
			if got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}
