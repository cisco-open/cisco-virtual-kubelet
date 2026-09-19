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
	"fmt"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ─────────────────────────────────────────────────────────────────────────────
// parseVersionNumber
// ─────────────────────────────────────────────────────────────────────────────

func TestParseVersionNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard IOS-XE format",
			input:    "Cisco IOS XE Software, Version 17.18.2",
			expected: "17.18.2",
		},
		{
			name:     "version with build suffix",
			input:    "Cisco IOS XE Software, Version 17.12.1a, RELEASE SOFTWARE (fc5)",
			expected: "17.12.1",
		},
		{
			name:     "version only",
			input:    "Version 16.9.3",
			expected: "16.9.3",
		},
		{
			name:     "no version keyword returns raw",
			input:    "some-other-string",
			expected: "some-other-string",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersionNumber(tt.input)
			if got != tt.expected {
				t.Errorf("parseVersionNumber(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetDeviceInfo
// ─────────────────────────────────────────────────────────────────────────────

func TestGetDeviceInfo_Nil(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	info, err := d.GetDeviceInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil DeviceInfo")
	}
	// All fields should be zero-value since deviceInfo was never set
	if info.SerialNumber != "" || info.SoftwareVersion != "" || info.ProductID != "" {
		t.Errorf("expected empty DeviceInfo, got %+v", info)
	}
}

func TestGetDeviceInfo_Populated(t *testing.T) {
	d := &XEDriver{
		config: &v1alpha1.DeviceSpec{},
		deviceInfo: &common.DeviceInfo{
			SerialNumber:    "FCW2345L0AB",
			SoftwareVersion: "17.18.2",
			ProductID:       "C8200L-1N-4T",
		},
	}
	info, err := d.GetDeviceInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SerialNumber != "FCW2345L0AB" {
		t.Errorf("SerialNumber = %q, want FCW2345L0AB", info.SerialNumber)
	}
	if info.SoftwareVersion != "17.18.2" {
		t.Errorf("SoftwareVersion = %q, want 17.18.2", info.SoftwareVersion)
	}
	if info.ProductID != "C8200L-1N-4T" {
		t.Errorf("ProductID = %q, want C8200L-1N-4T", info.ProductID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ConfigureSignVerification
// ─────────────────────────────────────────────────────────────────────────────

func TestConfigureSignVerification_Disabled(t *testing.T) {
	var capturedPath string
	var capturedPayload any
	var capturedRPC any
	fc := &fakeNetworkClient{
		putHook: func(path string, payload any) error {
			capturedPath = path
			capturedPayload = payload
			return nil
		},
		postWithResultHook: func(path string, payload, result any) error {
			if path != appHostingRPCPath {
				t.Fatalf("RPC path=%q, want %q", path, appHostingRPCPath)
			}
			capturedRPC = payload
			response := result.(*appHostingRPCOutput)
			response.Result = "Application signature verification disabled"
			response.Present = true
			return nil
		},
	}
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}, client: fc}

	if err := d.ConfigureSignVerification(testCtx(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPath != signVerificationPath {
		t.Errorf("PUT path = %q, want %q", capturedPath, signVerificationPath)
	}
	ctrl, ok := capturedPayload.(appHostingControls)
	if !ok {
		t.Fatalf("payload type = %T, want appHostingControls", capturedPayload)
	}
	if ctrl.Controls.SignVerification != false {
		t.Error("expected sign-verification=false for allowUnsignedApps path")
	}
	outer, ok := capturedRPC.(map[string]any)
	if !ok {
		t.Fatalf("RPC payload type=%T", capturedRPC)
	}
	input := outer["Cisco-IOS-XE-rpc:app-hosting"].(map[string]any)
	verification := input["verification"].(map[string]any)
	disable, ok := verification["disable"].([]any)
	if !ok || len(disable) != 1 || disable[0] != nil {
		t.Fatalf("verification disable encoding=%#v, want [null]", verification["disable"])
	}
}

func TestConfigureSignVerification_Enabled(t *testing.T) {
	var capturedPayload any
	fc := &fakeNetworkClient{
		putHook: func(_ string, payload any) error {
			capturedPayload = payload
			return nil
		},
		postWithResultHook: func(_ string, _, result any) error {
			response := result.(*appHostingRPCOutput)
			response.Result = "Application signature verification enabled"
			response.Present = true
			return nil
		},
	}
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}, client: fc}

	if err := d.ConfigureSignVerification(testCtx(), true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctrl, ok := capturedPayload.(appHostingControls)
	if !ok {
		t.Fatalf("payload type = %T, want appHostingControls", capturedPayload)
	}
	if ctrl.Controls.SignVerification != true {
		t.Error("expected sign-verification=true")
	}
}

func TestConfigureSignVerification_PutError(t *testing.T) {
	fc := &fakeNetworkClient{
		putHook: func(_ string, _ any) error {
			return fmt.Errorf("request failed with status 500 Internal Server Error")
		},
	}
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}, client: fc}

	err := d.ConfigureSignVerification(testCtx(), false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sign-verification") {
		t.Errorf("error %q does not mention sign-verification", err.Error())
	}
}

func TestConfigureSignVerification_RuntimeMismatch(t *testing.T) {
	const sentinel = "runtime-result-secret-sentinel"
	fc := &fakeNetworkClient{
		putHook: func(_ string, _ any) error { return nil },
		postWithResultHook: func(_ string, _, result any) error {
			response := result.(*appHostingRPCOutput)
			response.Result = "Application signature verification is enabled while using bootflash " + sentinel
			response.Present = true
			return nil
		},
	}
	err := (&XEDriver{config: &v1alpha1.DeviceSpec{}, client: fc}).ConfigureSignVerification(testCtx(), false)
	if err == nil {
		t.Fatal("ConfigureSignVerification succeeded despite runtime mismatch")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error exposed device result: %v", err)
	}
}

func TestConfigureSignVerification_UnknownRuntimeResultFailsClosed(t *testing.T) {
	fc := &fakeNetworkClient{
		putHook: func(_ string, _ any) error { return nil },
		postWithResultHook: func(_ string, _, result any) error {
			response := result.(*appHostingRPCOutput)
			response.Result = "Unable to disable application signature verification"
			response.Present = true
			return nil
		},
	}
	if err := (&XEDriver{config: &v1alpha1.DeviceSpec{}, client: fc}).ConfigureSignVerification(testCtx(), false); err == nil {
		t.Fatal("ConfigureSignVerification accepted an unknown runtime result")
	}
}

func TestConfigureSignVerification_SuccessPrefixWithFailureSuffixFailsClosed(t *testing.T) {
	fc := &fakeNetworkClient{
		putHook: func(_ string, _ any) error { return nil },
		postWithResultHook: func(_ string, _, result any) error {
			response := result.(*appHostingRPCOutput)
			response.Result = "Application signature verification disabled failed to apply"
			response.Present = true
			return nil
		},
	}
	if err := (&XEDriver{config: &v1alpha1.DeviceSpec{}, client: fc}).ConfigureSignVerification(testCtx(), false); err == nil {
		t.Fatal("ConfigureSignVerification accepted a success prefix with a failure suffix")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetDeviceResources
// ─────────────────────────────────────────────────────────────────────────────

func TestGetDeviceResources(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{}}
	res, err := d.GetDeviceResources(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil ResourceList")
	}

	checks := map[v1.ResourceName]string{
		v1.ResourceCPU:     "8",
		v1.ResourceMemory:  "16Gi",
		v1.ResourceStorage: "100Gi",
		v1.ResourcePods:    "16",
	}

	for name, expected := range checks {
		actual := (*res)[name]
		expectedQ := resource.MustParse(expected)
		if actual.Cmp(expectedQ) != 0 {
			t.Errorf("%s = %s, want %s", name, actual.String(), expected)
		}
	}
}
