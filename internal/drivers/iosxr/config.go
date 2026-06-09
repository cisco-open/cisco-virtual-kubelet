// Copyright (c) 2026 Cisco Systems Inc.
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

package iosxr

import (
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func xrDefaultSource(spec *v1alpha1.DeviceSpec) string {
	if spec != nil && spec.XR != nil && spec.XR.AppHosting != nil {
		return strings.TrimSpace(spec.XR.AppHosting.DefaultSource)
	}
	return ""
}

func xrDefaultRunOptions(spec *v1alpha1.DeviceSpec) []string {
	if spec != nil && spec.XR != nil && spec.XR.AppHosting != nil && len(spec.XR.AppHosting.DefaultRunOptions) > 0 {
		out := make([]string, 0, len(spec.XR.AppHosting.DefaultRunOptions))
		for _, opt := range spec.XR.AppHosting.DefaultRunOptions {
			if opt = strings.TrimSpace(opt); opt != "" {
				out = append(out, opt)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"--network host"}
}

func xrPackageInstallPath(spec *v1alpha1.DeviceSpec) string {
	if spec != nil && spec.XR != nil && spec.XR.AppHosting != nil {
		if path := strings.TrimSpace(spec.XR.AppHosting.PackageInstallPath); path != "" {
			return path
		}
	}
	return "/harddisk:"
}

func xrEnableDockerCommand(spec *v1alpha1.DeviceSpec) string {
	if spec != nil && spec.XR != nil && spec.XR.AppHosting != nil {
		if spec.XR.AppHosting.EnableDockerCommand != "" {
			return strings.TrimSpace(spec.XR.AppHosting.EnableDockerCommand)
		}
	}
	return "run systemctl start docker"
}
