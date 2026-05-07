// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package diagnostic

import (
	"errors"
	"fmt"
	"strings"
)

// ErrCommandDisallowed is returned by ValidateCommands when one or
// more requested commands fall outside the read-only allowlist.
// Callers (reconciler, admin server) propagate this as a user-facing
// validation failure — the device is never contacted.
var ErrCommandDisallowed = errors.New("diagnostic command not allowed")

// ValidateCommands enforces the read-only allowlist on every command
// in cmds. Returns the first ErrCommandDisallowed-wrapping error, or
// nil if every command passes.
//
// The diagnostic subsystem (IOSXEDiagnostic CRD + admin /v1/exec
// endpoint + kubectl-ciscovk plugin) is read-only by design: the
// transport's `DiagnosticExec` lands at IOS-XE's `cli-exec` RPC, which
// the device CLI parser routes only to user-mode show / monitor /
// ping / traceroute commands.
//
// Pre-this-fix the reconciler + admin server forwarded
// spec.commands directly to the transport with NO server-side
// validation. A user with create-IOSXEDiagnostic RBAC could bypass
// the kubectl plugin's denylist and submit `configure terminal …`
// or destructive CLI through the same device credentials. The
// allowlist closes that bypass.
//
// Defense-in-depth: kubectl-ciscovk still keeps its own denylist;
// admission controller (kubebuilder pattern marker on
// IOSXEDiagnostic.spec.commands) provides a third layer if cluster
// admins enable it.
//
// Wave 10 release-readiness P0 fix (2026-04-28).
func ValidateCommands(cmds []string) error {
	for i, raw := range cmds {
		if err := validateOne(raw); err != nil {
			return fmt.Errorf("commands[%d] %q: %w", i, raw, err)
		}
	}
	return nil
}

// validateOne returns nil if `raw` is on the read-only allowlist;
// ErrCommandDisallowed otherwise. The check is conservative — when in
// doubt, deny and surface the user-friendly error to the operator.
func validateOne(raw string) error {
	cmd := strings.TrimSpace(raw)
	if cmd == "" {
		return fmt.Errorf("%w: empty command", ErrCommandDisallowed)
	}
	// Reject embedded shell / multi-statement separators outright.
	// CLI-injection vectors that survive the device parser would be
	// rare but not impossible; refuse syntactically rather than
	// trying to enumerate every device-specific delimiter.
	for _, banned := range []string{"\n", "\r", ";", "&&", "||", "|sh", "| sh"} {
		if strings.Contains(cmd, banned) {
			return fmt.Errorf("%w: contains separator/pipe %q", ErrCommandDisallowed, banned)
		}
	}
	// Lowercase head-word for the allowlist check.
	lower := strings.ToLower(cmd)
	for _, ok := range allowedHeads {
		if lower == ok || strings.HasPrefix(lower, ok+" ") {
			// Defense-in-depth denylist on substrings inside
			// otherwise-allowed commands (e.g. `show ip route` is
			// fine; `show running-config | redirect tftp:…` would
			// exfiltrate config). Reject pipe/redirect chains.
			for _, denied := range deniedSubstrings {
				if strings.Contains(lower, denied) {
					return fmt.Errorf("%w: contains denylisted substring %q", ErrCommandDisallowed, denied)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("%w: head-word not in read-only allowlist", ErrCommandDisallowed)
}

// allowedHeads is the closed set of CLI head-words the diagnostic
// subsystem accepts. Every entry must be a user-mode read-only
// command on Cisco IOS-XE; adding a new entry requires explicit
// review. Sorted by frequency-of-use.
var allowedHeads = []string{
	"show",
	"more",
	"dir",
	"ping",
	"ping6",
	"traceroute",
	"traceroute6",
	"monitor",
	"test",      // narrow; allowlisted because `test cable-diagnostics` etc. are read-only diagnostic surfaces
	"verify",    // image / file integrity checks
	"calendar",  // `calendar` (read-only date display)
	"terminal",  // `terminal length 0` / `terminal no monitor` — pager + session controls only
	"namespace", // ip vrf-name display
}

// deniedSubstrings catches device-side I/O redirection and pipe
// chains that could exfiltrate config or trigger writes even from
// an otherwise-allowed head-word. The match is on the lowercased
// command body.
var deniedSubstrings = []string{
	"| redirect ",
	"|redirect ",
	"| append ",
	"|append ",
	"| tee ",
	"|tee ",
	"copy ",   // `copy running-config startup-config`, `copy tftp …`
	"erase ",  // `erase startup-config`
	"format ", // `format flash:`
	"reload",
	"write ",   // `write memory` / `write erase`
	"delete ",  // `delete flash:`
	"archive ", // `archive download-sw`, `archive upload-sw`
}
