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

// Package diagnostic implements the IOSXEDiagnostic CRD reconciler
// (Phase B of the diagnostics RFC) and its supporting helpers.
//
// The redaction filter, output truncation, and result-history
// trimming are split into stand-alone functions because they are
// cleanly testable without spinning up a controller-runtime test
// fixture.
package diagnostic

import (
	"regexp"
	"strings"
)

// secretLineRe matches IOS-XE config-style lines that frequently
// appear in `show running-config` output and carry secret material.
// Conservative by design — false positives drop a line operators
// could see anyway via SSH; false negatives leak credentials into
// etcd-backed CR status. The list is anchored to the line start
// after optional leading whitespace and is case-insensitive.
//
// Operators with elevated audit rights can disable redaction via
// IOSXEDiagnostic.spec.allowSecrets: true.
var secretLineRe = regexp.MustCompile(`(?i)^[\t ]*(` +
	`enable\s+secret\b|` +
	`enable\s+password\b|` +
	// `username <name> [privilege <N>] [secret|password] ...` —
	// Cisco optionally inserts `privilege <N>` BEFORE the secret/
	// password token, so the older `username \S+ (secret|password)`
	// regex missed `username admin privilege 15 secret 9 $14$…`.
	// Match any username line that contains secret or password.
	`username\s+\S+\b.*\b(secret|password)\b|` +
	`snmp-server\s+community\b|` +
	`snmp-server\s+(host|user)\b|` +
	// `tacacs-server key …` AND the indented `key …` line under a
	// `tacacs server <name>` / `radius server <name>` stanza.
	`tacacs-server\s+(key|host)\b|` +
	`radius-server\s+(key|host)\b|` +
	`tacacs\s+server\s+\S+|` +   // standalone `tacacs server <name>` header
	`radius\s+server\s+\S+|` +   // standalone `radius server <name>` header
	`key\s+(string|chain|7|6|0)\b|` +    // `key 7 …`, `key 0 …`, `key chain`
	`key\s+\S+\s*$|` +           // bare `key <token>` line (indented under server stanza)
	`server-private\b|` +        // `server-private … key 7 …` AAA blocks
	`ip\s+ftp\s+password\b|` +
	`ip\s+ssh\s+pubkey-chain\b|` +
	`crypto\s+(isakmp|ipsec|key)\b|` +
	`pre-shared-key\b|` +
	`shared-secret\b|` +
	`peer\s+default\s+ip\s+address\s+pool\b|` +
	`ppp\s+chap\s+(password|hostname)\b|` +
	// Wave 10 release-readiness P1 fix (2026-04-28): line-mode
	// password — the bare `password <type?> <token>` line indented
	// under a `line vty`, `line console`, or `line aux` stanza.
	// Caught against committed t5 evidence that contained
	// `line vty 0 4\n password ww\n`. The optional `\d+\s+` allows
	// `password 7 094F471A1A0A` (Cisco type-7 reversible) as well
	// as the bare `password ww` shape. Pattern matches indented
	// `password …` lines that aren't already covered by the
	// `username` / `enable` clauses above.
	`password\s+(\d+\s+)?\S+\s*$|` +
	// Routing-protocol passwords — caught against the same t5
	// evidence's `domain-password cisco` (ISIS). Covers ISIS
	// `domain-password` + `area-password`, OSPF + RIP/EIGRP
	// `authentication-key` + `message-digest-key` (with optional
	// `ip ospf` / `ipv6 ospf` / `ip rip` / `ip eigrp` prefix), BGP
	// `neighbor … password …`, and generic `isis password` shapes.
	`(domain|area)-password\s+\S+|` +
	`(ip\s+ospf\s+|ipv6\s+ospf\s+|ip\s+rip\s+|ip\s+eigrp\s+\d+\s+)?authentication-key\s+(\d+\s+)?\S+|` +
	`(ip\s+ospf\s+)?message-digest-key\s+\d+\s+md5\s+\S+|` +
	`neighbor\s+\S+\s+password\b|` +
	`isis\s+password\s+\S+` +
	`)`)

// Redact replaces lines in `output` that match secretLineRe with a
// fixed redaction marker. Returns the redacted text and a boolean
// indicating whether any line was redacted (so the caller can set
// CommandOutput.Redacted appropriately).
//
// The line is replaced wholesale rather than just the secret token,
// because IOS-XE encodes secrets in many shapes (`5 $1$...`,
// `7 abcdef`, hashed digests) and a per-token regex would inevitably
// drift behind format changes. Operators see "<redacted by cisco-vk
// — set spec.allowSecrets: true to disable>" instead of the line.
func Redact(output string) (redacted string, didRedact bool) {
	if output == "" {
		return output, false
	}
	const marker = "<redacted by cisco-vk — set spec.allowSecrets: true to disable>"
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if secretLineRe.MatchString(line) {
			// Preserve the line's leading whitespace so the
			// surrounding output's indentation isn't disturbed.
			leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = leading + marker
			didRedact = true
		}
	}
	if !didRedact {
		return output, false
	}
	return strings.Join(lines, "\n"), true
}

// Truncate clips s at maxBytes if it's longer, returning the clipped
// string and a flag indicating whether truncation occurred. A
// truncation marker is appended so consumers don't mistake a
// silently-clipped output for a complete one.
//
// maxBytes <= 0 disables truncation. The truncation point is the
// last newline boundary on or before maxBytes — preserving line
// integrity matters because show output is line-oriented.
func Truncate(s string, maxBytes int) (clipped string, truncated bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	const trailer = "\n... <truncated by cisco-vk — increase spec.retention.truncateAt to retain more>"
	budget := maxBytes - len(trailer)
	if budget <= 0 {
		// maxBytes smaller than the trailer itself — fall back to
		// hard byte clip.
		return s[:maxBytes], true
	}
	cut := budget
	// Find the last newline at or before `cut` to preserve line
	// integrity. If none exists in the budget window, hard-clip.
	if idx := strings.LastIndex(s[:cut], "\n"); idx > 0 {
		cut = idx
	}
	return s[:cut] + trailer, true
}

// ParseSize converts a kubebuilder-shaped size string ("64KiB",
// "1MiB", "1024B") to a byte count. Returns 0 (truncation disabled)
// on empty input; returns -1 on parse failure so the caller can
// distinguish "operator wrote a typo" from "operator opted out".
func ParseSize(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mul := 1
	switch {
	case strings.HasSuffix(s, "MiB"):
		mul = 1024 * 1024
		s = strings.TrimSuffix(s, "MiB")
	case strings.HasSuffix(s, "KiB"):
		mul = 1024
		s = strings.TrimSuffix(s, "KiB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n * mul
}
