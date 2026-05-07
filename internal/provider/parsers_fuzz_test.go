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

package provider

import "testing"

// Watch-item #6: splitReplayAnnotation feeds operator-supplied
// annotation values into the replay path. The contract is "either
// nil or a [logName, selector] pair" — the fuzz target enforces
// no-panic and shape consistency. Hash selectors include a colon
// (e.g. "sha256:..."), so the parser cannot do a naive split-on-":".

func FuzzSplitReplayAnnotation(f *testing.F) {
	f.Add("edge-01-log:sha256:abc123")
	f.Add("log:0")
	f.Add("")
	f.Add(":")
	f.Add("name:")
	f.Add(":selector")
	f.Add("a:b:c:d:e")
	f.Add("noColon")

	f.Fuzz(func(t *testing.T, raw string) {
		parts := splitReplayAnnotation(raw)
		switch {
		case parts == nil:
			// nil is the documented "malformed" return — accept.
		case len(parts) == 2:
			if parts[0] == "" || parts[1] == "" {
				t.Fatalf("non-nil result must have both halves non-empty, got %q for input %q", parts, raw)
			}
			if parts[0]+":"+parts[1] != raw {
				t.Fatalf("recombined %q != input %q", parts[0]+":"+parts[1], raw)
			}
		default:
			t.Fatalf("unexpected return shape %v for input %q", parts, raw)
		}
	})
}
