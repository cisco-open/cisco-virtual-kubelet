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
	"os"
	"path/filepath"
	"strings"
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

func TestLoad_GNOITransportConfig(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("device", map[string]interface{}{
		"driver":   "XE",
		"address":  "192.0.2.10",
		"username": "admin",
		"xe": map[string]interface{}{
			"networking": map[string]interface{}{
				"interface": map[string]interface{}{
					"type":             "VirtualPortGroup",
					"virtualPortGroup": map[string]interface{}{"dhcp": true},
				},
			},
			"gnoi": map[string]interface{}{
				"certificateProvisioning": map[string]interface{}{
					"certificateID":         "cvk-gnoi-os",
					"replaceTargetCABundle": true,
					"secretRef": map[string]interface{}{
						"name": "router-gnoi-identity",
					},
				},
			},
		},
		"gnoi": map[string]interface{}{
			"port":              19339,
			"transportSecurity": "tls",
		},
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Device.GNOI == nil {
		t.Fatal("device.gnoi was not decoded")
	}
	if got := cfg.Device.GNOI.Port; got != 19339 {
		t.Fatalf("device.gnoi.port=%d, want 19339", got)
	}
	if got := cfg.Device.GNOI.TransportSecurity; got != v1alpha1.GNOITransportSecurityTLS {
		t.Fatalf("device.gnoi.transportSecurity=%q, want tls", got)
	}
	if got := cfg.Device.XE.GNOI.CertificateProvisioning; got == nil || got.CertificateID != "cvk-gnoi-os" || got.SecretRef.Name != "router-gnoi-identity" || !got.ReplaceTargetCABundle {
		t.Fatalf("device.xe.gnoi.certificateProvisioning=%+v, want decoded certificate ID and Secret reference", got)
	}
}

func TestLoad_GNOIDedicatedTLS(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("device", map[string]interface{}{
		"driver":   "FAKE",
		"address":  "192.0.2.10",
		"username": "admin",
		"tls": map[string]interface{}{
			"enabled":            true,
			"insecureSkipVerify": true,
		},
		"gnoi": map[string]interface{}{
			"transportSecurity": "tls",
			"tls": map[string]interface{}{
				"caFile":   "/run/gnoi/ca.crt",
				"certFile": "/run/gnoi/tls.crt",
				"keyFile":  "/run/gnoi/tls.key",
			},
		},
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if got := cfg.Device.GNOI.TLS; got == nil || got.CAFile != "/run/gnoi/ca.crt" || got.CertFile != "/run/gnoi/tls.crt" || got.KeyFile != "/run/gnoi/tls.key" {
		t.Fatalf("device.gnoi.tls=%+v, want dedicated verified TLS paths", got)
	}
}

func TestLoadRejectsKubernetesGNOITLSSecretRef(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("device", map[string]interface{}{
		"driver":   "FAKE",
		"address":  "192.0.2.10",
		"username": "admin",
		"gnoi": map[string]interface{}{
			"transportSecurity": "tls",
			"tls": map[string]interface{}{
				"secretRef": map[string]interface{}{"name": "router-gnoi-tls"},
			},
		},
	})

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "secretRef is supported only in Kubernetes objects") {
		t.Fatalf("Load() error=%v, want local Secret reference error", err)
	}
}

func TestLoadRejectsSharedTLSControlsUnderGNOITLS(t *testing.T) {
	for _, field := range []string{"enabled", "insecureSkipVerify"} {
		t.Run(field, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set("device", map[string]interface{}{
				"driver":   "FAKE",
				"address":  "192.0.2.10",
				"username": "admin",
				"gnoi": map[string]interface{}{
					"transportSecurity": "tls",
					"tls": map[string]interface{}{
						"caFile": "/run/gnoi/ca.crt",
						field:    true,
					},
				},
			})

			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted superfluous gnoi.tls.%s", field)
			}
		})
	}
}

func TestLoad_RejectsGNOIProvisioningForOtherDrivers(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("device", map[string]interface{}{
		"driver":   "FAKE",
		"address":  "192.0.2.10",
		"username": "admin",
		"gnoi": map[string]interface{}{
			"transportSecurity": "tls",
		},
		"xe": map[string]interface{}{
			"gnoi": map[string]interface{}{
				"certificateProvisioning": map[string]interface{}{
					"certificateID":         "cvk-gnoi-os",
					"replaceTargetCABundle": true,
					"secretRef":             map[string]interface{}{"name": "router-gnoi-identity"},
				},
			},
		},
	})

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "only for driver XE") {
		t.Fatalf("Load() error=%v, want IOS XE scope error", err)
	}
}

func TestLoadRejectsCertificateProvisioningAtGenericGNOIPath(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("device", map[string]interface{}{
		"driver":   "XE",
		"address":  "192.0.2.10",
		"username": "admin",
		"xe":       map[string]interface{}{"networking": map[string]interface{}{}},
		"gnoi": map[string]interface{}{
			"transportSecurity": "tls",
			"certificateProvisioning": map[string]interface{}{
				"certificateID":         "cvk-gnoi-os",
				"replaceTargetCABundle": true,
				"secretRef":             map[string]interface{}{"name": "router-gnoi-identity"},
			},
		},
	})

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted certificateProvisioning under generic device.gnoi")
	}
}

func TestValidateDeviceSpecGNOITrustSources(t *testing.T) {
	provisioning := func() *v1alpha1.XEGNOIConfig {
		return &v1alpha1.XEGNOIConfig{
			CertificateProvisioning: &v1alpha1.XEGNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi-os",
				SecretRef:             v1alpha1.XEGNOIProvisioningSecretReference{Name: "router-gnoi-identity"},
				ReplaceTargetCABundle: true,
			},
		}
	}
	tests := []struct {
		name             string
		sharedTLS        *v1alpha1.TLSConfig
		dedicatedTLS     *v1alpha1.GNOITLSConfig
		xeGNOI           *v1alpha1.XEGNOIConfig
		wantErrSubstring string
	}{
		{
			name:      "verified shared TLS",
			sharedTLS: &v1alpha1.TLSConfig{Enabled: true, CAFile: "/run/shared/ca.crt"},
		},
		{
			name:             "unverified shared TLS",
			sharedTLS:        &v1alpha1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
			wantErrSubstring: "requires system or verified shared TLS",
		},
		{
			name:         "dedicated TLS isolates unverified shared transport",
			sharedTLS:    &v1alpha1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
			dedicatedTLS: &v1alpha1.GNOITLSConfig{CAFile: "/run/gnoi/ca.crt"},
		},
		{
			name:      "provisioning derives verified trust",
			sharedTLS: &v1alpha1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
			xeGNOI:    provisioning(),
		},
		{
			name:             "dedicated TLS conflicts with provisioning trust",
			sharedTLS:        &v1alpha1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
			dedicatedTLS:     &v1alpha1.GNOITLSConfig{CAFile: "/run/gnoi/ca.crt"},
			xeGNOI:           provisioning(),
			wantErrSubstring: "cannot both be configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &v1alpha1.DeviceSpec{
				Driver: v1alpha1.DeviceDriverXE,
				XE:     &v1alpha1.XEConfig{GNOI: tt.xeGNOI},
				TLS:    tt.sharedTLS,
				GNOI: &v1alpha1.GNOIConfig{
					TransportSecurity: v1alpha1.GNOITransportSecurityTLS,
					TLS:               tt.dedicatedTLS,
				},
			}
			err := validateDeviceSpec(spec)
			if tt.wantErrSubstring == "" {
				if err != nil {
					t.Fatalf("validateDeviceSpec() error=%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstring) {
				t.Fatalf("validateDeviceSpec() error=%v, want substring %q", err, tt.wantErrSubstring)
			}
		})
	}
}

func TestValidateDeviceSpecRequiresExplicitTLSForXEGNOIProvisioning(t *testing.T) {
	spec := &v1alpha1.DeviceSpec{
		Driver: v1alpha1.DeviceDriverXE,
		XE: &v1alpha1.XEConfig{
			GNOI: &v1alpha1.XEGNOIConfig{
				CertificateProvisioning: &v1alpha1.XEGNOICertificateProvisioning{},
			},
		},
		TLS:  &v1alpha1.TLSConfig{Enabled: true},
		GNOI: &v1alpha1.GNOIConfig{TransportSecurity: v1alpha1.GNOITransportSecurityAuto},
	}
	if err := validateDeviceSpec(spec); err == nil || !strings.Contains(err.Error(), "requires spec.gnoi.transportSecurity to be tls") {
		t.Fatalf("validateDeviceSpec() error=%v, want explicit gNOI TLS error", err)
	}
}

func TestLoad_RejectsPlaintextGNOIOverride(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("device", map[string]interface{}{
		"driver":   "FAKE",
		"address":  "192.0.2.10",
		"username": "admin",
		"gnoi":     map[string]interface{}{"transportSecurity": "plaintext"},
	})

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted plaintext device.gnoi transport")
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

func TestLoad_PreservesDottedDeviceLabelKeys(t *testing.T) {
	viper.Reset()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := []byte(`
device:
  address: 1.1.1.1
  driver: XE
  labels:
    cisco.io/device-type: iosxe-apphosting
    workload: edge
  xe:
    networking:
      interface:
        type: AppGigabitEthernet
        appGigabitEthernet:
          mode: trunk
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if got := cfg.Device.Labels["cisco.io/device-type"]; got != "iosxe-apphosting" {
		t.Fatalf("expected dotted label key to be preserved, got %q", got)
	}
	if got := cfg.Device.Labels["workload"]; got != "edge" {
		t.Fatalf("expected workload label to be preserved, got %q", got)
	}
}
