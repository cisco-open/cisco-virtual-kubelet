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

package writers

import "strings"

// Device version support for YANG version-conditional writers.
//
// Writer instances capture an OverrideResolver per device. The helper in
// this file validates a reported version before the reconciler allows
// writes to run; it does not mutate process-global writer state.

// SetDeviceVersion validates the IOS-XE software version string reported
// by the device (e.g. "17.16.01a", "17.15.03", "17.18.2"). Historical
// name aside, it no longer records process-global writer state.
//
// Returns an error if ver is empty or has no parseable major.minor
// prefix. Callers in production MUST propagate this error rather than
// silently letting the writers fall through to baseline behavior on an
// unknown device.
func SetDeviceVersion(ver string) error {
	if ver == "" {
		return &ErrMalformedDeviceVersion{Version: ver}
	}
	major, minor, ok := parseVersionStrict(ver)
	if !ok {
		return &ErrMalformedDeviceVersion{Version: ver}
	}
	// Reject device versions that aren't in the explicit
	// device-version → release-tag mapping. The reconciler surfaces
	// this as an UnsupportedDevice condition on the CR rather than
	// silently running baseline-shape writers against a device whose
	// schema diverges. Tests can opt out by calling SetDeviceVersion
	// with a known version (17.18.2 by default) and using
	// ResolveForVersion directly when they need a manually picked
	// (major, minor) outside the supported set.
	if _, mapped := ReleaseTagForDeviceVersion(major, minor); !mapped {
		return &ErrUnsupportedDeviceVersion{Version: ver}
	}
	return nil
}

// parseVersionStrict is the strict form: returns ok=false if the
// string has fewer than two numeric segments or either of the first
// two segments has no leading digits. Empty string is rejected.
func parseVersionStrict(s string) (major, minor int, ok bool) {
	if s == "" {
		return 0, 0, false
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	majorN, majorOK := parseIntPrefixStrict(parts[0])
	minorN, minorOK := parseIntPrefixStrict(parts[1])
	if !majorOK || !minorOK {
		return 0, 0, false
	}
	return majorN, minorN, true
}

// parseIntPrefixStrict is parseIntPrefix but reports whether at least
// one leading digit was consumed. The empty string and a string with
// no leading digit return ok=false.
func parseIntPrefixStrict(s string) (int, bool) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, false
	}
	return parseIntPrefix(s), true
}

// parseIntPrefix reads the leading decimal digits of s. Stops at
// the first non-digit. Returns 0 for empty or non-numeric strings.
func parseIntPrefix(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
