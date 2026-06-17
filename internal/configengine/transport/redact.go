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

import "regexp"

// credentialPatterns lists regular expressions whose match groups
// carry credential material in NETCONF / RESTCONF / CLI error
// messages. RedactCredentials replaces match group 1 with a
// fixed `***REDACTED***` token while preserving the surrounding
// element / attribute / keyword so operators can still see WHERE
// the credential was without leaking its value.
//
// The patterns intentionally match the YANG element shapes that
// the IOS-XE-native + IOS-XE-aaa modules expose for credential-
// bearing fields, plus the CLI keyword shapes that appear in
// `<error-info><bad-element>...</bad-element></error-info>`
// responses when the device echoes a chunk of the failing
// edit-config request body.
//
// Wave 10 release-readiness fix (2026-04-28). Pre-fix a NETCONF
// Mutate failure on a family carrying `<password>` could surface
// the plaintext credential in the engine's ApplyError message,
// the controller's status writeback, the Kubernetes event, and
// the pod log. Defense-in-depth: the redact step runs at the
// engine boundary AND the transport boundary so a leak in either
// layer is caught.
var credentialPatterns = []*regexp.Regexp{
	// NETCONF / RESTCONF body shapes.
	regexp.MustCompile(`(?i)<password[^>]*>([^<]*)</password>`),
	regexp.MustCompile(`(?i)<encrypted-password[^>]*>([^<]*)</encrypted-password>`),
	regexp.MustCompile(`(?i)<secret[^>]*>([^<]*)</secret>`),
	regexp.MustCompile(`(?i)<key[^>]*>([^<]*)</key>`),
	regexp.MustCompile(`(?i)<key-string[^>]*>([^<]*)</key-string>`),
	regexp.MustCompile(`(?i)<shared-secret[^>]*>([^<]*)</shared-secret>`),
	regexp.MustCompile(`(?i)<rsa-key[^>]*>([^<]*)</rsa-key>`),
	regexp.MustCompile(`(?i)<pre-shared-key[^>]*>([^<]*)</pre-shared-key>`),
	// JSON shapes (RFC 7951 RESTCONF).
	regexp.MustCompile(`(?i)"password"\s*:\s*"([^"]*)"`),
	regexp.MustCompile(`(?i)"secret"\s*:\s*"([^"]*)"`),
	regexp.MustCompile(`(?i)"key"\s*:\s*"([^"]*)"`),
	regexp.MustCompile(`(?i)"shared-secret"\s*:\s*"([^"]*)"`),
	regexp.MustCompile(`(?i)"pre-shared-key"\s*:\s*"([^"]*)"`),
	// CLI keyword shapes that show up in echoed bad-element bodies.
	regexp.MustCompile(`(?i)password\s+\d+\s+(\S+)`),
	regexp.MustCompile(`(?i)secret\s+\d+\s+(\S+)`),
	regexp.MustCompile(`(?i)key-string\s+\S+\s+(\S+)`),
	regexp.MustCompile(`(?i)key\s+\d+\s+(\S+)`),
}

// RedactCredentials returns s with credential material replaced by
// `***REDACTED***`. Empty input is returned unchanged. Safe to call
// on any string — the regexes match only when a credential-bearing
// element / attribute / keyword is present.
//
// Order of precedence: each pattern runs in order, so `<password>`
// is redacted before any keyword-shaped fallback. The keyword
// fallbacks are conservative — they require the keyword AND a
// numeric type-byte (e.g. `password 0 cleartext` or `password 7
// encrypted`) to avoid stripping unrelated `password` substrings
// that happen to appear in error narrative.
func RedactCredentials(s string) string {
	if s == "" {
		return s
	}
	for _, re := range credentialPatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			groups := re.FindStringSubmatch(match)
			if len(groups) < 2 {
				return match
			}
			return regexReplaceFirst(match, groups[1], "***REDACTED***")
		})
	}
	return s
}

// regexReplaceFirst replaces the first occurrence of `old` in `s`
// with `new`. Used by RedactCredentials to swap the captured
// group's value while preserving the surrounding markup.
func regexReplaceFirst(s, old, new string) string {
	if old == "" {
		return s
	}
	idx := substringIndex(s, old)
	if idx < 0 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

// substringIndex is a hand-rolled `strings.Index` to keep redact.go
// free of `strings` imports for clarity. Returns -1 if `sub` not
// found in `s`.
func substringIndex(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
