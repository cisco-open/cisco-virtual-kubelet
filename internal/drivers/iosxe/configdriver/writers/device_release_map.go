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

import (
	"errors"
	"fmt"
)

// ──────────────────────────────────────────────────────────────────
// Device-version → YANG-release-tag mapping
//
// The string the device reports ("17.16.01a", "17.18.2") is not the
// same as the YANG release tag declared in
// internal/drivers/iosxe/configdriver/schema/yang-versions.yaml
// ("1716", "1718"). This mapping is the explicit policy contract:
//   "device software version (major,minor) X is served by writers
//    against YANG release tag Y."
//
// It is the single point where product policy lives. New IOS-XE
// minor releases ship by:
//   1. Adding an entry to deviceVersionToReleaseTag below.
//   2. Vendoring the matching YANG modules under schema/yang/<tag>/.
//   3. Adding the release entry to yang-versions.yaml.
//   4. Adding fixture coverage for the families that diverge.
//
// Removing an entry here marks a device-version as unsupported. The
// reconciler then surfaces an UnsupportedDevice condition on the
// CR rather than silently running baseline shapes against a device
// whose schema diverges.
// ──────────────────────────────────────────────────────────────────

// releaseKey is the (major, minor) of the device software version.
type releaseKey struct {
	major int
	minor int
}

// deviceVersionToReleaseTag is the canonical mapping. The keys are
// the (major, minor) pair the device reports; the value is the
// schema/yang-versions.yaml tag for the writers' shape.
//
// Patch differences (17.16.01a vs 17.16.02) are not represented —
// patch-level YANG schema changes have not occurred in the releases
// we have validated against. If a patch release introduces a YANG
// shape change, this map's key type must expand to include patch.
var deviceVersionToReleaseTag = map[releaseKey]string{
	{17, 9}:  "1791", // Phase-1 baseline
	{17, 15}: "1715", // C9KV legacy ('ace-rule' wrapper exists, mode leaf differs)
	{17, 16}: "1716", // C8000V Phase-2 (custom legacy writers for bgp/prefix-list/etc.)
	{17, 18}: "1718", // C9300-24P baseline
}

// ReleaseTagForDeviceVersion returns the YANG release tag matching
// the device-reported major.minor. Returns ok=false when the device
// version has no mapping — callers must surface that as an
// "unsupported device version" rather than silently fall back.
func ReleaseTagForDeviceVersion(major, minor int) (string, bool) {
	tag, ok := deviceVersionToReleaseTag[releaseKey{major, minor}]
	return tag, ok
}

// ReleaseTagForDeviceVersionString is the string-input form. It
// parses the version with the strict parser; an unparseable version
// returns ok=false.
func ReleaseTagForDeviceVersionString(ver string) (string, bool) {
	major, minor, ok := parseVersionStrict(ver)
	if !ok {
		return "", false
	}
	return ReleaseTagForDeviceVersion(major, minor)
}

// SupportedDeviceVersions returns the set of (major, minor) pairs the
// writers currently know how to serve. Useful for status messages
// ("device reports X; we support: ...").
func SupportedDeviceVersions() []string {
	out := make([]string, 0, len(deviceVersionToReleaseTag))
	for k := range deviceVersionToReleaseTag {
		out = append(out, fmt.Sprintf("%d.%d", k.major, k.minor))
	}
	return out
}

// ExemplarDeviceVersionForReleaseTag returns one canonical device-
// version string that maps to the given release tag. It is the
// inverse of ReleaseTagForDeviceVersionString and is used by the
// fixture harness to pick a concrete SetDeviceVersion argument when
// iterating fixtures keyed by release tag. Returns ok=false when the
// tag has no mapping.
//
// When multiple device-version (major, minor) pairs map to the same
// tag, the first one encountered wins. Tests should not depend on
// which one — the only contract is that the returned string parses
// and resolves to the requested tag.
func ExemplarDeviceVersionForReleaseTag(tag string) (string, bool) {
	for k, v := range deviceVersionToReleaseTag {
		if v == tag {
			return fmt.Sprintf("%d.%d.0", k.major, k.minor), true
		}
	}
	return "", false
}

// ErrMalformedDeviceVersion is returned when the device version does
// not contain a parseable major.minor prefix.
type ErrMalformedDeviceVersion struct {
	Version string
}

func (e *ErrMalformedDeviceVersion) Error() string {
	return fmt.Sprintf("writers: malformed device version %q (expected major.minor[.patch])", e.Version)
}

// IsMalformedDeviceVersion reports whether err is the malformed
// device-version sentinel.
func IsMalformedDeviceVersion(err error) bool {
	var target *ErrMalformedDeviceVersion
	return errors.As(err, &target)
}

// ErrUnsupportedDeviceVersion is returned by SetDeviceVersion when
// the device's reported version does not appear in
// deviceVersionToReleaseTag. The error wraps the version string so
// callers can surface it on the CR status.
type ErrUnsupportedDeviceVersion struct {
	Version string
}

func (e *ErrUnsupportedDeviceVersion) Error() string {
	return fmt.Sprintf("writers: unsupported device version %q (supported: %v)", e.Version, SupportedDeviceVersions())
}

// IsUnsupportedDeviceVersion reports whether err is the
// ErrUnsupportedDeviceVersion error. Used by cmd/cisco-vk and the
// aggregator to surface the condition differently from a generic
// SetDeviceVersion failure.
func IsUnsupportedDeviceVersion(err error) bool {
	var target *ErrUnsupportedDeviceVersion
	return errors.As(err, &target)
}
