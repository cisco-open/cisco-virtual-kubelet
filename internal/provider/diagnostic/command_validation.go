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
type CommandPlatform string

const (
	CommandPlatformIOSXE CommandPlatform = "iosxe"
	CommandPlatformNXOS  CommandPlatform = "nxos"
)

func ValidateCommands(cmds []string) error {
	return ValidateCommandsForPlatform("", cmds)
}

func ValidateCommandsForPlatform(platform CommandPlatform, cmds []string) error {
	for i, raw := range cmds {
		if err := validateOne(platform, raw); err != nil {
			return fmt.Errorf("commands[%d] %q: %w", i, raw, err)
		}
	}
	return nil
}

// validateOne returns nil if `raw` is on the read-only allowlist;
// ErrCommandDisallowed otherwise. The check is conservative — when in
// doubt, deny and surface the user-friendly error to the operator.
func validateOne(platform CommandPlatform, raw string) error {
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
	for _, h := range allowedHeadsForPlatform(platform) {
		if lower != h.head && !strings.HasPrefix(lower, h.head+" ") {
			continue
		}
		// Per-head subcommand whitelist (adversarial-review Finding #3):
		// `monitor` and `terminal` are no longer wildcard heads.
		// `monitor` allows only `monitor capture <name> ... buffer ...`
		// (read-only buffer inspection); start/stop/clear/export are
		// state-changing and rejected. `terminal` is restricted to
		// pager/session-control invocations.
		if !headAllowsBody(h, lower) {
			return fmt.Errorf("%w: %s subcommand is not read-only", ErrCommandDisallowed, h.head)
		}
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
	return fmt.Errorf("%w: head-word not in read-only allowlist", ErrCommandDisallowed)
}

// allowedHead describes one read-only CLI head-word. When
// allowedSubcommandPatterns is non-empty the body after the head
// must match one of the listed substrings (each substring is
// interpreted as "the body must contain this token sequence"); this
// is how we keep `monitor capture <name> ... buffer ...` while
// rejecting `monitor capture <name> start|stop|clear|export ...`.
type allowedHead struct {
	head string
	// allowedSubcommandPatterns is matched against the body after the
	// head-word (lowercased). Empty list means the head is a wildcard
	// (e.g. `show` accepts any subcommand because the surface is huge
	// and uniformly read-only).
	allowedSubcommandPatterns []string
	// deniedSubcommandTokens is matched against the body after the
	// head-word; if any of these whole-word tokens appear, the
	// command is rejected. Used to keep `terminal length 0` while
	// rejecting `terminal monitor` (enables console log mirror).
	deniedSubcommandTokens []string
}

func allowedHeadsForPlatform(platform CommandPlatform) []allowedHead {
	switch platform {
	case CommandPlatformNXOS:
		return nxosAllowedHeads
	default:
		return iosxeAllowedHeads
	}
}

// iosxeAllowedHeads is the closed set of CLI head-words the diagnostic
// subsystem accepts. Every entry must be a user-mode read-only
// command on Cisco IOS-XE; adding a new entry requires explicit
// review. Sorted by frequency-of-use.
var iosxeAllowedHeads = []allowedHead{
	{head: "show"},
	{head: "more"},
	{head: "dir"},
	{head: "ping"},
	{head: "ping6"},
	{head: "traceroute"},
	{head: "traceroute6"},
	// `monitor capture <name> ... buffer ...` is the only read-only
	// monitor form; start/stop/clear/export change device state and
	// must not flow through a read-only API surface.
	{
		head: "monitor",
		allowedSubcommandPatterns: []string{
			"capture ", // must be capture-related
		},
		deniedSubcommandTokens: []string{
			"start", "stop", "clear", "export", "file", "associate",
			"limit", "match", "buffer-size", "circular", "linear",
		},
	},
	// `test cable-diagnostics ...` etc. are read-only diagnostic
	// surfaces. The deny list rejects the few `test` forms that
	// trigger packet generation or device-side writes.
	{
		head: "test",
		deniedSubcommandTokens: []string{
			"crash", "platform", "flash",
		},
	},
	{head: "verify"},   // image / file integrity checks
	{head: "calendar"}, // `calendar` (read-only date display)
	// `terminal length 0` / `terminal no monitor` are pager / session
	// controls; `terminal monitor` enables console log mirror which
	// is a session-state change that surfaces device logs to the
	// caller's session and must not be invoked through a read-only API.
	{
		head: "terminal",
		allowedSubcommandPatterns: []string{
			"length ", "width ", "no monitor", "exec-timeout ", "history ",
		},
	},
	{head: "namespace"}, // ip vrf-name display
}

// nxosAllowedHeads keeps NX-API CLI diagnostics deliberately narrower
// than the IOS-XE allowlist. NX-API CLI has both show and config
// channels; read-only CRD/admin execution should stay on operational
// commands while write-class changes go through config writers or
// explicit operational APIs.
var nxosAllowedHeads = []allowedHead{
	{head: "show"},
	{head: "more"},
	{head: "dir"},
	{head: "ping"},
	{head: "ping6"},
	{head: "traceroute"},
	{head: "traceroute6"},
	{head: "verify"},
	{
		head: "terminal",
		allowedSubcommandPatterns: []string{
			"length ", "width ",
		},
	},
}

// headAllowsBody returns true if the lowercased command body satisfies
// the per-head subcommand constraints. The full lowercased command
// (head + body) is passed in to keep token-boundary matching simple.
func headAllowsBody(h allowedHead, lower string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(lower, h.head))
	if len(h.allowedSubcommandPatterns) > 0 {
		matched := false
		for _, pat := range h.allowedSubcommandPatterns {
			if strings.HasPrefix(body, pat) || strings.Contains(body, " "+pat) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(h.deniedSubcommandTokens) > 0 {
		// Token-boundary check: reject if any denied token appears as
		// a whole word in the body. Surrounding spaces or end-of-string
		// count as boundaries.
		padded := " " + body + " "
		for _, tok := range h.deniedSubcommandTokens {
			if strings.Contains(padded, " "+tok+" ") {
				return false
			}
		}
	}
	return true
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
