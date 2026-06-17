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

// Package writers exposes the neutral per-family writer contract. The current
// implementation aliases the IOS-XE writer set while other platforms get their
// own registries behind the same interface.
package writers

import iosxewriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"

type (
	SectionWriter    = iosxewriters.SectionWriter
	PruneCapable     = iosxewriters.PruneCapable
	KeyExtractable   = iosxewriters.KeyExtractable
	FamilySchema     = iosxewriters.FamilySchema
	VersionOverride  = iosxewriters.VersionOverride
	OverrideResolver = iosxewriters.OverrideResolver

	ErrMalformedDeviceVersion   = iosxewriters.ErrMalformedDeviceVersion
	ErrUnsupportedDeviceVersion = iosxewriters.ErrUnsupportedDeviceVersion
)

var ErrNotImplemented = iosxewriters.ErrNotImplemented

func Register(w SectionWriter) {
	iosxewriters.Register(w)
}

func Override(w SectionWriter) {
	iosxewriters.Override(w)
}

func Get(family string) SectionWriter {
	return iosxewriters.Get(family)
}

func GetForRelease(family, release string) SectionWriter {
	return iosxewriters.GetForRelease(family, release)
}

func Families() []string {
	return iosxewriters.Families()
}

func Schema(family string) (FamilySchema, bool) {
	return iosxewriters.Schema(family)
}

func AllSchemas() map[string]FamilySchema {
	return iosxewriters.AllSchemas()
}

func Len() int {
	return iosxewriters.Len()
}

func SetDeviceVersion(ver string) error {
	return iosxewriters.SetDeviceVersion(ver)
}

func ReleaseTagForDeviceVersion(major, minor int) (string, bool) {
	return iosxewriters.ReleaseTagForDeviceVersion(major, minor)
}

func ReleaseTagForDeviceVersionString(ver string) (string, bool) {
	return iosxewriters.ReleaseTagForDeviceVersionString(ver)
}

func SupportedDeviceVersions() []string {
	return iosxewriters.SupportedDeviceVersions()
}

func ExemplarDeviceVersionForReleaseTag(tag string) (string, bool) {
	return iosxewriters.ExemplarDeviceVersionForReleaseTag(tag)
}

func IsMalformedDeviceVersion(err error) bool {
	return iosxewriters.IsMalformedDeviceVersion(err)
}

func IsUnsupportedDeviceVersion(err error) bool {
	return iosxewriters.IsUnsupportedDeviceVersion(err)
}

func NewOverrideResolver(version string) (*OverrideResolver, error) {
	return iosxewriters.NewOverrideResolver(version)
}

func NewOverrideResolverForMajorMinor(major, minor int) *OverrideResolver {
	return iosxewriters.NewOverrideResolverForMajorMinor(major, minor)
}

func ApplyElementMap(body map[string]any, emap map[string]string) map[string]any {
	return iosxewriters.ApplyElementMap(body, emap)
}

func ApplyEmptyLeaves(body map[string]any, leaves []string) map[string]any {
	return iosxewriters.ApplyEmptyLeaves(body, leaves)
}

func ApplyOverrideToBody(body map[string]any, o *VersionOverride) map[string]any {
	return iosxewriters.ApplyOverrideToBody(body, o)
}

func ReverseElementMap(body map[string]any, emap map[string]string) map[string]any {
	return iosxewriters.ReverseElementMap(body, emap)
}

func ReverseOverrideFromBody(body map[string]any, o *VersionOverride) map[string]any {
	return iosxewriters.ReverseOverrideFromBody(body, o)
}

func DecodeEmptyLeaves(body map[string]any, leaves []string) map[string]any {
	return iosxewriters.DecodeEmptyLeaves(body, leaves)
}
