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

package drivers_test

import (
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

func TestConfigDriverContextUsesPlatformVersionPolicy(t *testing.T) {
	ctx := &drivers.ConfigDriverContext{
		PlatformName:  "nxos",
		DeviceVersion: "10.3(9)",
		DeviceVersionPolicy: drivers.DeviceVersionPolicy{
			Validate:      nxosschema.ValidateDeviceVersion,
			IsUnsupported: nxosschema.IsUnsupportedDeviceVersion,
			IsMalformed:   nxosschema.IsMalformedDeviceVersion,
		},
	}
	if err := ctx.ValidateDeviceVersion(); err != nil {
		t.Fatalf("NX-OS version entered a foreign platform policy: %v", err)
	}

	ctx.DeviceVersion = "10.6(1)"
	err := ctx.ValidateDeviceVersion()
	if !ctx.IsUnsupportedDeviceVersionError(err) {
		t.Fatalf("error=%v, want NX-OS unsupported classification", err)
	}
}
