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
	"strings"
	"sync"
)

// Device version support for YANG version-conditional writers.
//
// Each VK process handles exactly one device, so a package-level
// singleton is appropriate. The factory (register.go) calls
// SetDeviceVersion once during ConfigDriverContext construction.
// Writers call DeviceVersion() or the convenience helpers to branch.

var (
	deviceVersionMu   sync.RWMutex
	deviceVersionStr  string
	deviceVersionMajor int
	deviceVersionMinor int
)

// SetDeviceVersion records the IOS-XE software version string
// reported by the device (e.g. "17.16.01a", "17.15.03", "17.18.2").
// The string is parsed into major.minor for comparison helpers.
func SetDeviceVersion(ver string) {
	deviceVersionMu.Lock()
	defer deviceVersionMu.Unlock()
	deviceVersionStr = ver
	deviceVersionMajor, deviceVersionMinor = parseVersion(ver)
}

// DeviceVersion returns the raw version string set by
// SetDeviceVersion. Empty if not yet set.
func DeviceVersion() string {
	deviceVersionMu.RLock()
	defer deviceVersionMu.RUnlock()
	return deviceVersionStr
}

// DeviceVersionAtLeast returns true if the device version is ≥
// the given major.minor. Returns false if no version has been set.
func DeviceVersionAtLeast(major, minor int) bool {
	deviceVersionMu.RLock()
	defer deviceVersionMu.RUnlock()
	if deviceVersionStr == "" {
		return false
	}
	if deviceVersionMajor != major {
		return deviceVersionMajor > major
	}
	return deviceVersionMinor >= minor
}

// parseVersion extracts the first two numeric segments from a
// Cisco IOS-XE version string. Examples:
//
//	"17.16.01a" → (17, 16)
//	"17.18.2"   → (17, 18)
//	"17.15.03"  → (17, 15)
//	""          → (0, 0)
func parseVersion(s string) (major, minor int) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0
	}
	major = parseIntPrefix(parts[0])
	minor = parseIntPrefix(parts[1])
	return major, minor
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
