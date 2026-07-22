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

package schema

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const EnvAllowExperimentalReleases = "CVK_NXOS_ALLOW_EXPERIMENTAL_RELEASES"

// DeviceProfile is a tested NX-OS software/model combination. Support is
// explicit and fail-closed so a new train cannot silently inherit DME shapes
// validated against another release.
type DeviceProfile struct {
	Release      string
	ModelVersion string
	Evidence     string
	Experimental bool
}

var deviceProfiles = []struct {
	pattern *regexp.Regexp
	profile DeviceProfile
}{
	{
		pattern: regexp.MustCompile(`^10\.3\(9\)[A-Za-z0-9._-]*$`),
		profile: DeviceProfile{
			Release: "10.3(9)", ModelVersion: "0.3.0",
			Evidence: "CVK Nexus 9000v lab",
		},
	},
	{
		pattern: regexp.MustCompile(`^10\.5\(4\)[A-Za-z0-9._-]*$`),
		profile: DeviceProfile{
			Release: "10.5(4)", ModelVersion: "0.3.0",
			Evidence:     "NetAsCode NX-OS 0.3.0 tested-version matrix; CVK DME qualification pending",
			Experimental: true,
		},
	},
}

type ErrMalformedDeviceVersion struct{ Version string }

func (e *ErrMalformedDeviceVersion) Error() string {
	return fmt.Sprintf("malformed NX-OS device version %q", e.Version)
}

type ErrUnsupportedDeviceVersion struct{ Version string }

func (e *ErrUnsupportedDeviceVersion) Error() string {
	return fmt.Sprintf("unsupported NX-OS device version %q (supported: %s)", e.Version, strings.Join(SupportedDeviceVersions(), ", "))
}

type ErrExperimentalDeviceVersion struct {
	Version string
	Profile DeviceProfile
}

func (e *ErrExperimentalDeviceVersion) Error() string {
	return fmt.Sprintf("NX-OS device version %q uses experimental profile %q; set %s=true to opt in",
		e.Version, e.Profile.Release, EnvAllowExperimentalReleases)
}

func ProfileForDeviceVersion(version string) (DeviceProfile, error) {
	version = strings.TrimSpace(version)
	if version == "" || !regexp.MustCompile(`^\d+\.\d+\([^)]+\)`).MatchString(version) {
		return DeviceProfile{}, &ErrMalformedDeviceVersion{Version: version}
	}
	for _, candidate := range deviceProfiles {
		if candidate.pattern.MatchString(version) {
			return candidate.profile, nil
		}
	}
	return DeviceProfile{}, &ErrUnsupportedDeviceVersion{Version: version}
}

func ValidateDeviceVersion(version string) error {
	profile, err := ProfileForDeviceVersion(version)
	if err != nil {
		return err
	}
	if profile.Experimental && !allowExperimentalReleases() {
		return &ErrExperimentalDeviceVersion{Version: version, Profile: profile}
	}
	return nil
}

func IsMalformedDeviceVersion(err error) bool {
	var target *ErrMalformedDeviceVersion
	return errors.As(err, &target)
}

func IsUnsupportedDeviceVersion(err error) bool {
	var target *ErrUnsupportedDeviceVersion
	var experimental *ErrExperimentalDeviceVersion
	return errors.As(err, &target) || errors.As(err, &experimental)
}

func ReleaseTagForDeviceVersionString(version string) (string, bool) {
	if err := ValidateDeviceVersion(version); err != nil {
		return "", false
	}
	profile, err := ProfileForDeviceVersion(version)
	return profile.Release, err == nil
}

func SupportedDeviceVersions() []string {
	out := make([]string, 0, len(deviceProfiles))
	for _, candidate := range deviceProfiles {
		if candidate.profile.Experimental && !allowExperimentalReleases() {
			continue
		}
		out = append(out, candidate.profile.Release)
	}
	sort.Strings(out)
	return out
}

func allowExperimentalReleases() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvAllowExperimentalReleases))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func SupportedDeviceVersionSet() map[string]struct{} {
	out := make(map[string]struct{}, len(deviceProfiles))
	for _, version := range SupportedDeviceVersions() {
		out[version] = struct{}{}
	}
	return out
}
