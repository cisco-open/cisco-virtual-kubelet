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

package main

import (
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestSupportsIOSXEMutationControllers(t *testing.T) {
	tests := []struct {
		name string
		spec *ciskov1.DeviceSpec
		want bool
	}{
		{name: "nil", want: false},
		{name: "IOS XE", spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE}, want: true},
		{name: "IOS XR", spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXR}, want: false},
		{name: "NX-OS", spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverNXOS}, want: false},
		{name: "OpenConfig", spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverOPENCONFIG}, want: false},
		{name: "fake", spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverFAKE}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportsIOSXEMutationControllers(tt.spec); got != tt.want {
				t.Fatalf("supportsIOSXEMutationControllers(%v) = %t, want %t", tt.spec, got, tt.want)
			}
		})
	}
}
