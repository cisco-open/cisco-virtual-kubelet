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

package config

import (
	"path/filepath"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/spf13/viper"
)

func TestLoad_FullSchema(t *testing.T) {
	viper.Reset()
	fixturePath := filepath.Join("testdata", "valid_config.yaml")

	_, err := Load(fixturePath)
	if err != nil {
		t.Errorf("Error loading full config schema: %v", err)
	}
}

func TestLoad_ConditionalDefaults(t *testing.T) {
	tests := []struct {
		name         string
		fixture      string
		expectedPort int
	}{
		{
			name:         "Default to 80 for HTTP",
			fixture:      "valid_http.yaml",
			expectedPort: 80,
		},
		{
			name:         "Default to 443 for HTTPS",
			fixture:      "valid_https.yaml",
			expectedPort: 443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset Viper for each sub-test to avoid pollution
			viper.Reset()
			fixturePath := filepath.Join("testdata", tt.fixture)
			// Point to our specific test fixture

			cfg, err := Load(fixturePath)
			if err != nil {
				t.Fatalf("Load() failed: %v", err)
			}

			actualPort := cfg.Device.Port
			if actualPort != tt.expectedPort {
				t.Errorf("Expected port %d, got %d", tt.expectedPort, actualPort)
			}
		})
	}
}

// TestSetDeviceDefaults_TransportAwarePort is a regression test for
// finding #6(a). Pre-fix, SetDeviceDefaults blindly assigned port 443
// when TLS.Enabled and 80 otherwise — a RESTCONF assumption that
// silently poisoned NETCONF and gNMI transports. The cisco-vk pod
// then dialed :443 expecting an SSH banner and read a TLS
// ServerHello, producing `ssh: overflow reading version string`.
func TestSetDeviceDefaults_TransportAwarePort(t *testing.T) {
	cases := []struct {
		name      string
		transport string
		tlsEnabled bool
		wantPort  int
	}{
		{"netconf — port 830 regardless of TLS", "netconf", false, 830},
		{"netconf — port 830 when TLS enabled too", "netconf", true, 830},
		{"NETCONF case-insensitive", "NETCONF", false, 830},
		{"gnmi — port 50052 (gnxi insecure default)", "gnmi", false, 50052},
		{"restconf — TLS off → 80", "restconf", false, 80},
		{"restconf — TLS on → 443", "restconf", true, 443},
		{"empty transport — historical RESTCONF default 80", "", false, 80},
		{"empty transport TLS — historical 443", "", true, 443},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &v1alpha1.DeviceSpec{Transport: tc.transport}
			if tc.tlsEnabled {
				spec.TLS = &v1alpha1.TLSConfig{Enabled: true}
			}
			if err := SetDeviceDefaults(spec); err != nil {
				t.Fatalf("SetDeviceDefaults: %v", err)
			}
			if spec.Port != tc.wantPort {
				t.Fatalf("transport=%q TLS=%v: port=%d, want %d",
					tc.transport, tc.tlsEnabled, spec.Port, tc.wantPort)
			}
		})
	}
}

// TestSetDeviceDefaults_PortRespected checks the explicit-port path
// still wins — operators with non-default ports keep that override.
func TestSetDeviceDefaults_PortRespected(t *testing.T) {
	spec := &v1alpha1.DeviceSpec{Transport: "netconf", Port: 8830}
	if err := SetDeviceDefaults(spec); err != nil {
		t.Fatalf("SetDeviceDefaults: %v", err)
	}
	if spec.Port != 8830 {
		t.Fatalf("explicit port overridden: got %d, want 8830", spec.Port)
	}
}

func TestLoad_StrictLoading(t *testing.T) {
	viper.Reset()
	fixturePath := filepath.Join("testdata", "strict_fail.yaml")

	_, err := Load(fixturePath)
	if err == nil {
		t.Error("Expected error for unknown fields (strict loading), but got nil")
	}
}

func TestLoad_ExplicitPort(t *testing.T) {
	// Verify that an explicitly set port is NOT overwritten by defaults
	viper.Reset()

	// We can set values directly in Viper to simulate env/args
	tls := v1alpha1.TLSConfig{
		Enabled: false,
	}
	viper.Set("device", map[string]interface{}{
		"address": "1.1.1.1",
		"port":    8080,
		"tls":     tls,
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Device.Port != 8080 {
		t.Errorf("Expected explicit port 8080 to be preserved, got %d", cfg.Device.Port)
	}
}

func TestLoad_InterfaceConfigValidation(t *testing.T) {
	viper.Reset()

	viper.Set("device", map[string]interface{}{
		"address": "1.2.3.4",
		"driver":  "XE",
		"xe": map[string]interface{}{
			"networking": map[string]interface{}{
				"interface": map[string]interface{}{
					"type": "AppGigabitEthernet",
					"virtualPortGroup": map[string]interface{}{
						"interface": "0",
					},
				},
			},
		},
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid interface config, got nil")
	}
}
