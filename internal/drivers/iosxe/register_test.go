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

package iosxe

import (
	"context"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	iosxevalidation "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/validation"
)

func TestConfigDriverFactoryRetainsIOSXEReleaseValidation(t *testing.T) {
	ctx, err := buildXEConfigDriverContext(context.Background(), &ciskov1.DeviceSpec{
		Driver:    ciskov1.DeviceDriverXE,
		Transport: "unsupported-test-transport",
	}, "", drivers.ConfigDriverOptions{})
	if err == nil {
		t.Fatal("buildXEConfigDriverContext error=nil, want transport construction error")
	}
	if ctx == nil {
		t.Fatal("buildXEConfigDriverContext returned nil context")
	}
	if _, ok := ctx.OperationValidator.(*iosxevalidation.StructuralValidator); !ok {
		t.Fatalf("OperationValidator=%T, want IOS XE release-aware validator", ctx.OperationValidator)
	}
}
