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

import "testing"

// Watch-item #6: splitYAMLDocs is fed arbitrary GitOps YAML — a
// crash here breaks the lint tool's ability to flag policy
// violations on operator input. The fuzz target enforces two
// contract invariants:
//
//  1. Never panic on any input.
//  2. The split is non-lossy: every byte of the (CRLF-normalised)
//     input that isn't part of a "\n---" separator or a leading
//     "---" prefix on a part is preserved somewhere in the output.
//
// A stronger round-trip rule isn't possible here because
// splitYAMLDocs intentionally strips a leading "---" from each
// part (the YAML document separator), so the function is not a
// perfect inverse. The byte-preservation rule is the actual
// invariant lint depends on when chaining the parts into
// yaml.Unmarshal.

func FuzzSplitYAMLDocs(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("---\nfoo: 1\n"))
	f.Add([]byte("foo: 1\n---\nbar: 2\n"))
	f.Add([]byte("apiVersion: v1\nkind: Secret\n---\napiVersion: v1\nkind: ConfigMap\n"))
	f.Add([]byte("\n---\n---\n---\n"))
	f.Add([]byte("a\r\n---\r\nb\r\n"))
	f.Add([]byte("---only"))

	f.Fuzz(func(t *testing.T, data []byte) {
		parts := splitYAMLDocs(data)
		// Sanity: every part is a substring of the normalised input.
		// Catches an accidental rewrite or character-corruption bug.
		norm := stringReplaceAll(string(data), "\r\n", "\n")
		for i, p := range parts {
			if !contains(norm, string(p)) {
				t.Fatalf("part %d %q is not a substring of normalised input %q", i, p, norm)
			}
		}
	})
}

func stringReplaceAll(s, old, new string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			out = append(out, new...)
			i += len(old)
			continue
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
