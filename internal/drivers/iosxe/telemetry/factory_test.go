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

package telemetry

import (
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestGNMIConfigFromDeviceSpecTLSDefaults(t *testing.T) {
	tests := []struct {
		name       string
		port       int
		tlsEnabled bool
		wantPort   int
		wantTLS    bool
	}{
		{
			name:       "tls enabled restconf secure port defaults to gnmi secure",
			port:       443,
			tlsEnabled: true,
			wantPort:   9339,
			wantTLS:    true,
		},
		{
			name:       "tls disabled restconf secure port defaults to gnxi insecure",
			port:       443,
			tlsEnabled: false,
			wantPort:   50052,
			wantTLS:    false,
		},
		{
			name:       "tls enabled explicit gnxi insecure port is respected with tls",
			port:       50052,
			tlsEnabled: true,
			wantPort:   50052,
			wantTLS:    true,
		},
		{
			name:       "tls disabled unset port defaults to gnxi insecure",
			port:       0,
			tlsEnabled: false,
			wantPort:   50052,
			wantTLS:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := &ciskov1.DeviceSpec{
				Address:  "10.0.0.1",
				Port:     tc.port,
				Username: "admin",
				TLS:      &ciskov1.TLSConfig{Enabled: tc.tlsEnabled, InsecureSkipVerify: true},
			}
			cfg, err := GNMIConfigFromDeviceSpec(spec, "pw")
			if err != nil {
				t.Fatalf("GNMIConfigFromDeviceSpec: %v", err)
			}
			if cfg.Port != tc.wantPort {
				t.Fatalf("port=%d, want %d", cfg.Port, tc.wantPort)
			}
			gotTLS := cfg.TLSConfig != nil
			if gotTLS != tc.wantTLS {
				t.Fatalf("TLSConfig set=%v, want %v", gotTLS, tc.wantTLS)
			}
			if cfg.TLSConfig != nil && !cfg.TLSConfig.InsecureSkipVerify {
				t.Fatal("TLSConfig did not carry InsecureSkipVerify")
			}
		})
	}
}
