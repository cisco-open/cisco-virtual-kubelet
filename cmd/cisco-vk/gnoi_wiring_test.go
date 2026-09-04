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
	"context"
	"path/filepath"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestSetupGNOIPropagatesTLSConfigError(t *testing.T) {
	t.Setenv(gNOIDisabledEnv, "")
	t.Setenv(gNOIInsecureEnv, "")
	t.Setenv(gNOIPortEnv, "")

	provider, cleanup, err := setupGNOI(context.Background(), configReconcilerOptions{
		Spec: &ciskov1.DeviceSpec{
			Address: "192.0.2.1",
			TLS: &ciskov1.TLSConfig{
				Enabled: true,
				CAFile:  filepath.Join(t.TempDir(), "missing-ca.pem"),
			},
		},
	})
	if err == nil {
		t.Fatal("setupGNOI returned nil error for an unreadable CA file")
	}
	if provider != nil {
		t.Fatalf("setupGNOI provider = %#v, want nil after TLS error", provider)
	}
	if cleanup != nil {
		t.Fatal("setupGNOI returned cleanup after TLS error, want nil")
	}
}

func TestGNOIDialConfigAttachesCredentialsOnlyToTLS(t *testing.T) {
	tests := []struct {
		name        string
		tlsEnabled  bool
		username    string
		wantTLS     bool
		wantRPCAuth bool
	}{
		{name: "TLS with username", tlsEnabled: true, username: "admin", wantTLS: true, wantRPCAuth: true},
		{name: "TLS without username", tlsEnabled: true, wantTLS: true},
		{name: "plaintext with username", username: "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := gnoiDialConfig(&ciskov1.DeviceSpec{Username: tt.username}, "s3cret", tt.tlsEnabled)
			if err != nil {
				t.Fatalf("gnoiDialConfig: %v", err)
			}
			if got := cfg.TLSConfig != nil; got != tt.wantTLS {
				t.Errorf("TLSConfig present=%v, want %v", got, tt.wantTLS)
			}
			if got := cfg.RPCCredentials != nil; got != tt.wantRPCAuth {
				t.Errorf("RPCCredentials present=%v, want %v", got, tt.wantRPCAuth)
			}
		})
	}
}

func TestSetupGNOIResolvesPerDeviceTransport(t *testing.T) {
	tests := []struct {
		name        string
		spec        func() *ciskov1.DeviceSpec
		insecureEnv string
		portEnv     string
		wantPort    int
		wantTLS     bool
	}{
		{
			name: "legacy plaintext default is unchanged",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{Address: "192.0.2.1", Port: 443}
			},
			wantPort: 50052,
		},
		{
			name: "legacy TLS default is unchanged",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{Address: "192.0.2.1", Port: 443, TLS: &ciskov1.TLSConfig{Enabled: true}}
			},
			wantPort: 9339,
			wantTLS:  true,
		},
		{
			name: "legacy nonstandard device port is unchanged",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{Address: "192.0.2.1", Port: 10443, TLS: &ciskov1.TLSConfig{Enabled: true}}
			},
			wantPort: 10443,
			wantTLS:  true,
		},
		{
			name: "present auto block does not inherit RESTCONF port",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1", Port: 10443,
					TLS:  &ciskov1.TLSConfig{Enabled: true},
					GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityAuto},
				}
			},
			wantPort: 9339,
			wantTLS:  true,
		},
		{
			name: "TLS mode overrides shared TLS disabled",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1", TLS: &ciskov1.TLSConfig{Enabled: false},
					GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
				}
			},
			wantPort: 9339,
			wantTLS:  true,
		},
		{
			name: "plaintext mode overrides shared TLS enabled",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1", TLS: &ciskov1.TLSConfig{Enabled: true},
					GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityPlaintext},
				}
			},
			wantPort: 50052,
		},
		{
			name: "per-device port is honored",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
					GNOI: &ciskov1.GNOIConfig{
						Port: 19339, TransportSecurity: ciskov1.GNOITransportSecurityTLS,
					},
				}
			},
			wantPort: 19339,
			wantTLS:  true,
		},
		{
			name: "insecure environment override wins",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
					GNOI:    &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
				}
			},
			insecureEnv: "true",
			wantPort:    50052,
		},
		{
			name: "port environment override wins",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
					GNOI: &ciskov1.GNOIConfig{
						Port: 19339, TransportSecurity: ciskov1.GNOITransportSecurityTLS,
					},
				}
			},
			portEnv:  "29339",
			wantPort: 29339,
			wantTLS:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(gNOIDisabledEnv, "")
			t.Setenv(gNOIInsecureEnv, tt.insecureEnv)
			t.Setenv(gNOIPortEnv, tt.portEnv)

			provider, cleanup, err := setupGNOI(context.Background(), configReconcilerOptions{Spec: tt.spec()})
			if err != nil {
				t.Fatalf("setupGNOI: %v", err)
			}
			if cleanup == nil {
				t.Fatal("setupGNOI returned nil cleanup")
			}
			t.Cleanup(cleanup)
			pooled, ok := provider.(*pooledGNOIProvider)
			if !ok {
				t.Fatalf("provider type=%T, want *pooledGNOIProvider", provider)
			}
			if pooled.port != tt.wantPort {
				t.Errorf("port=%d, want %d", pooled.port, tt.wantPort)
			}
			if pooled.tls != tt.wantTLS {
				t.Errorf("tls=%v, want %v", pooled.tls, tt.wantTLS)
			}
		})
	}
}

func TestSetupGNOIRejectsInvalidPerDeviceTransport(t *testing.T) {
	t.Setenv(gNOIDisabledEnv, "")
	t.Setenv(gNOIInsecureEnv, "")
	t.Setenv(gNOIPortEnv, "")

	provider, cleanup, err := setupGNOI(context.Background(), configReconcilerOptions{
		Spec: &ciskov1.DeviceSpec{
			Address: "192.0.2.1",
			GNOI: &ciskov1.GNOIConfig{
				TransportSecurity: ciskov1.GNOITransportSecurity("unknown"),
			},
		},
	})
	if err == nil {
		t.Fatal("setupGNOI succeeded for invalid transportSecurity")
	}
	if provider != nil || cleanup != nil {
		t.Fatalf("setupGNOI returned provider=%T cleanup=%v after validation error", provider, cleanup != nil)
	}
}
