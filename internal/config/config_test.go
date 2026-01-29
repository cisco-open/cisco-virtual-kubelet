// Copyright © 2026 Cisco Systems, Inc.
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

	"github.com/spf13/viper"
)

func TestLoad_FullSchema(t *testing.T) {
	viper.Reset()
	fixturePath := filepath.Join("testdata", "valid_config.yaml")

	_, err := Load(fixturePath)
	if err != nil {
		t.Error("Error for loading full config schema")
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
	tls := TLSConfig{
		Enabled: false,
	}
	viper.Set("device", map[string]interface{}{
		"name":    "manual-node",
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
