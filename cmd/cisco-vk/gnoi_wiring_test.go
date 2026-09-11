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
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestSetupGNOIPropagatesTLSConfigError(t *testing.T) {
	t.Setenv(gNOIDisabledEnv, "")
	t.Setenv(gNOIInsecureEnv, "")
	t.Setenv(gNOIPortEnv, "")

	provider, provisioner, cleanup, err := setupGNOI(context.Background(), configReconcilerOptions{
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
	if provisioner != nil {
		t.Fatalf("setupGNOI provisioner = %#v, want nil after TLS error", provisioner)
	}
	if cleanup != nil {
		t.Fatal("setupGNOI returned cleanup after TLS error, want nil")
	}
}

func TestResolveGNOIConfigTransportTrustAndAuthentication(t *testing.T) {
	tests := []struct {
		name            string
		spec            *ciskov1.DeviceSpec
		password        string
		forceInsecure   bool
		wantPort        int
		wantTLS         bool
		wantRPCAuth     bool
		wantLegacyBasic bool
		wantTrust       gnoiTrustSource
		wantErr         string
	}{
		{
			name:            "legacy plaintext and Basic metadata remain unchanged",
			spec:            &ciskov1.DeviceSpec{Address: "192.0.2.1", Port: 443, Username: "admin"},
			password:        "secret",
			wantPort:        50052,
			wantLegacyBasic: true,
			wantTrust:       gnoiTrustSourcePlaintext,
		},
		{
			name: "legacy shared unverified TLS remains unchanged",
			spec: &ciskov1.DeviceSpec{
				Address: "192.0.2.1", Port: 443, Username: "admin",
				TLS: &ciskov1.TLSConfig{Enabled: true, InsecureSkipVerify: true}, //nolint:gosec // verifies legacy compatibility
			},
			password:        "secret",
			wantPort:        9339,
			wantTLS:         true,
			wantLegacyBasic: true,
			wantTrust:       gnoiTrustSourceLegacyShared,
		},
		{
			name: "explicit TLS uses verified shared trust and per-RPC credentials",
			spec: &ciskov1.DeviceSpec{
				Driver: ciskov1.DeviceDriverXE, Address: "192.0.2.1", Port: 10443, Username: "admin",
				TLS:  &ciskov1.TLSConfig{Enabled: true},
				GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
			},
			password:    "secret",
			wantPort:    9339,
			wantTLS:     true,
			wantRPCAuth: true,
			wantTrust:   gnoiTrustSourceSystem,
		},
		{
			name: "non-XE explicit TLS without password authentication remains verified",
			spec: &ciskov1.DeviceSpec{
				Driver:  ciskov1.DeviceDriverNXOS,
				Address: "192.0.2.1",
				GNOI:    &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
			},
			wantPort:  9339,
			wantTLS:   true,
			wantTrust: gnoiTrustSourceSystem,
		},
		{
			name: "non-XE explicit TLS rejects IOS XE password metadata",
			spec: &ciskov1.DeviceSpec{
				Driver: ciskov1.DeviceDriverNXOS, Address: "192.0.2.1", Username: "admin",
				GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
			},
			password: "secret",
			wantErr:  "password authentication is supported only for driver XE",
		},
		{
			name: "non-XE explicit TLS rejects IOS XE certificate provisioning",
			spec: &ciskov1.DeviceSpec{
				Driver:  ciskov1.DeviceDriverNXOS,
				Address: "192.0.2.1",
				GNOI:    &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
				XE: &ciskov1.XEConfig{GNOI: &ciskov1.XEGNOIConfig{
					CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
						CertificateID:         "cvk-gnoi",
						SecretRef:             ciskov1.XEGNOIProvisioningSecretReference{Name: "gnoi-provisioning"},
						ReplaceTargetCABundle: true,
					},
				}},
			},
			wantErr: "certificate provisioning is supported only for driver XE",
		},
		{
			name: "explicit TLS rejects inherited unverified shared trust",
			spec: &ciskov1.DeviceSpec{
				Driver: ciskov1.DeviceDriverXE, Address: "192.0.2.1", Username: "admin",
				TLS:  &ciskov1.TLSConfig{InsecureSkipVerify: true}, //nolint:gosec // verifies fail-closed behavior
				GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
			},
			password: "secret",
			wantErr:  "cannot be inherited",
		},
		{
			name: "explicit TLS rejects incomplete password authentication",
			spec: &ciskov1.DeviceSpec{
				Driver: ciskov1.DeviceDriverXE, Address: "192.0.2.1", Username: "admin",
				GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
			},
			wantErr: "requires both username and password",
		},
		{
			name: "insecure environment cannot override explicit TLS",
			spec: &ciskov1.DeviceSpec{
				Address: "192.0.2.1",
				GNOI:    &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS},
			},
			forceInsecure: true,
			wantErr:       "cannot override explicit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(gNOIPortEnv, "")
			resolved, err := resolveGNOIConfig(tt.spec, tt.password, tt.forceInsecure, t.TempDir(), false)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveGNOIConfig error=%v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveGNOIConfig: %v", err)
			}
			if resolved.port != tt.wantPort {
				t.Errorf("port=%d, want %d", resolved.port, tt.wantPort)
			}
			if got := resolved.dialConfig.TLSConfig != nil; got != tt.wantTLS {
				t.Errorf("TLSConfig present=%v, want %v", got, tt.wantTLS)
			}
			if got := resolved.dialConfig.RPCCredentials != nil; got != tt.wantRPCAuth {
				t.Errorf("RPCCredentials present=%v, want %v", got, tt.wantRPCAuth)
			}
			if got := resolved.dialConfig.Username != ""; got != tt.wantLegacyBasic {
				t.Errorf("legacy Basic credentials present=%v, want %v", got, tt.wantLegacyBasic)
			}
			wantAuthMode := "none"
			if tt.wantRPCAuth {
				wantAuthMode = "iosxe-password-metadata"
			} else if tt.wantLegacyBasic {
				wantAuthMode = "legacy-basic"
			}
			if got := resolved.authMode(); got != wantAuthMode {
				t.Errorf("authMode=%q, want %q", got, wantAuthMode)
			}
			if resolved.trustSource != tt.wantTrust {
				t.Errorf("trustSource=%q, want %q", resolved.trustSource, tt.wantTrust)
			}
		})
	}
}

func TestResolveGNOIConfigDedicatedTrustIsIsolatedFromSharedTLS(t *testing.T) {
	t.Setenv(gNOIPortEnv, "")
	directory, _ := writeGNOIProvisioningFiles(t, "192.0.2.1")
	spec := &ciskov1.DeviceSpec{
		Driver:   ciskov1.DeviceDriverXE,
		Address:  "192.0.2.1",
		Username: "admin",
		TLS: &ciskov1.TLSConfig{
			Enabled:            true,
			InsecureSkipVerify: true, //nolint:gosec // verifies isolation from the legacy RESTCONF setting
		},
		GNOI: &ciskov1.GNOIConfig{
			TransportSecurity: ciskov1.GNOITransportSecurityTLS,
			TLS: &ciskov1.GNOITLSConfig{
				CAFile: filepath.Join(directory, gNOIProvisioningCAFile),
			},
		},
	}

	resolved, err := resolveGNOIConfig(spec, "secret", false, t.TempDir(), false)
	if err != nil {
		t.Fatalf("resolveGNOIConfig: %v", err)
	}
	if resolved.trustSource != gnoiTrustSourceDedicated {
		t.Errorf("trustSource=%q, want %q", resolved.trustSource, gnoiTrustSourceDedicated)
	}
	if resolved.dialConfig.TLSConfig == nil || resolved.dialConfig.TLSConfig.RootCAs == nil {
		t.Fatal("dedicated gNOI CA roots were not loaded")
	}
	if resolved.dialConfig.TLSConfig.InsecureSkipVerify {
		t.Fatal("dedicated gNOI TLS inherited insecureSkipVerify from shared TLS")
	}
	if resolved.dialConfig.RPCCredentials == nil || resolved.dialConfig.Username != "" {
		t.Fatal("explicit secure gNOI did not exclusively use per-RPC credentials")
	}

	spec.GNOI.TLS = nil
	spec.TLS.InsecureSkipVerify = false
	spec.TLS.CAFile = filepath.Join(directory, gNOIProvisioningCAFile)
	resolved, err = resolveGNOIConfig(spec, "secret", false, t.TempDir(), false)
	if err != nil {
		t.Fatalf("resolve shared verified gNOI trust: %v", err)
	}
	if resolved.trustSource != gnoiTrustSourceShared {
		t.Errorf("shared trustSource=%q, want %q", resolved.trustSource, gnoiTrustSourceShared)
	}
}

func TestResolveGNOIConfigRejectsInvalidOrConflictingTrust(t *testing.T) {
	t.Setenv(gNOIPortEnv, "")
	validDirectory, _ := writeGNOIProvisioningFiles(t, "192.0.2.1")
	validCA := filepath.Join(validDirectory, gNOIProvisioningCAFile)
	tests := []struct {
		name    string
		config  *ciskov1.GNOITLSConfig
		mutate  func(*ciskov1.DeviceSpec)
		wantErr string
	}{
		{
			name:    "auto transport with override",
			config:  &ciskov1.GNOITLSConfig{CAFile: validCA},
			wantErr: "requires transportSecurity to be tls",
		},
		{
			name:    "missing CA file",
			config:  &ciskov1.GNOITLSConfig{CAFile: filepath.Join(t.TempDir(), "missing-ca.pem")},
			wantErr: "failed to read CA certificate",
		},
		{
			name:    "missing CA setting",
			config:  &ciskov1.GNOITLSConfig{},
			wantErr: "caFile is required",
		},
		{
			name:    "client certificate without key",
			config:  &ciskov1.GNOITLSConfig{CAFile: validCA, CertFile: "client.crt"},
			wantErr: "certFile and keyFile must be configured together",
		},
		{
			name:    "client key without certificate",
			config:  &ciskov1.GNOITLSConfig{CAFile: validCA, KeyFile: "client.key"},
			wantErr: "certFile and keyFile must be configured together",
		},
		{
			name: "missing shared CA file",
			mutate: func(spec *ciskov1.DeviceSpec) {
				spec.TLS = &ciskov1.TLSConfig{CAFile: filepath.Join(t.TempDir(), "missing-shared-ca.pem")}
			},
			wantErr: "shared TLS: failed to read CA certificate",
		},
		{
			name: "unresolved Kubernetes Secret reference",
			config: &ciskov1.GNOITLSConfig{
				SecretRef: &ciskov1.GNOITLSSecretReference{Name: "router-gnoi-tls"},
			},
			wantErr: "secretRef is supported only in Kubernetes objects",
		},
		{
			name:   "provisioning and dedicated override",
			config: &ciskov1.GNOITLSConfig{CAFile: validCA},
			mutate: func(spec *ciskov1.DeviceSpec) {
				spec.Driver = ciskov1.DeviceDriverXE
				spec.XE = provisioningDeviceSpec(spec.Address).XE
			},
			wantErr: "mutually exclusive trust sources",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := ciskov1.GNOITransportSecurityTLS
			if tt.name == "auto transport with override" {
				mode = ciskov1.GNOITransportSecurityAuto
			}
			spec := &ciskov1.DeviceSpec{
				Address: "192.0.2.1",
				GNOI: &ciskov1.GNOIConfig{
					TransportSecurity: mode,
					TLS:               tt.config,
				},
			}
			if tt.mutate != nil {
				tt.mutate(spec)
			}
			_, err := resolveGNOIConfig(spec, "", false, t.TempDir(), false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("resolveGNOIConfig error=%v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolveGNOIConfigDerivesIsolatedTrustFromProvisioning(t *testing.T) {
	t.Setenv(gNOIPortEnv, "")
	const address = "192.0.2.10"
	provisioningDirectory, leaf := writeGNOIProvisioningFiles(t, address)
	spec := provisioningDeviceSpec(address)
	spec.Username = "admin"
	spec.TLS.InsecureSkipVerify = true //nolint:gosec // verifies provisioning does not inherit RESTCONF policy

	resolved, err := resolveGNOIConfig(spec, "secret", false, provisioningDirectory, false)
	if err != nil {
		t.Fatalf("resolveGNOIConfig: %v", err)
	}
	if resolved.trustSource != gnoiTrustSourceProvisioning {
		t.Errorf("trustSource=%q, want %q", resolved.trustSource, gnoiTrustSourceProvisioning)
	}
	if resolved.provisioningBundle == nil {
		t.Fatal("provisioning trust did not produce a validated bundle")
	}
	if resolved.dialConfig.RPCCredentials == nil || resolved.dialConfig.Username != "" {
		t.Fatal("provisioned secure gNOI did not exclusively use per-RPC credentials")
	}
	if got := len(resolved.dialConfig.TLSConfig.RootCAs.Subjects()); got != 1 {
		t.Fatalf("provisioning RootCAs subjects=%d, want exactly the isolated provisioning root", got)
	}
	if err := resolved.dialConfig.TLSConfig.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err != nil {
		t.Fatalf("provisioning-derived TLS verification: %v", err)
	}
}

func TestUnavailableGNOIProviderPreservesSetupError(t *testing.T) {
	provider := unavailableGNOIProvider{cause: errors.New("invalid provisioning material")}
	if _, err := provider.GNOIClient(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid provisioning material") {
		t.Fatalf("GNOIClient error=%v, want original setup failure", err)
	}
}

func TestResolveGNOIConfigPortSelection(t *testing.T) {
	tests := []struct {
		name          string
		spec          func() *ciskov1.DeviceSpec
		forceInsecure bool
		portEnv       string
		wantPort      int
		wantErr       string
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
		},
		{
			name: "legacy nonstandard device port is unchanged",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{Address: "192.0.2.1", Port: 10443, TLS: &ciskov1.TLSConfig{Enabled: true}}
			},
			wantPort: 10443,
		},
		{
			name: "zero-value block preserves legacy nonstandard port",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1", Port: 10443,
					TLS:  &ciskov1.TLSConfig{Enabled: true},
					GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityAuto},
				}
			},
			wantPort: 10443,
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
		},
		{
			name: "insecure environment cannot override explicit TLS",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
					GNOI: &ciskov1.GNOIConfig{
						Port: 19339, TransportSecurity: ciskov1.GNOITransportSecurityTLS,
					},
				}
			},
			forceInsecure: true,
			wantErr:       "cannot override explicit",
		},
		{
			name: "XE certificate provisioning requires explicit TLS",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Driver:  ciskov1.DeviceDriverXE,
					Address: "192.0.2.1",
					TLS:     &ciskov1.TLSConfig{Enabled: true},
					GNOI:    &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityAuto},
					XE: &ciskov1.XEConfig{GNOI: &ciskov1.XEGNOIConfig{
						CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{},
					}},
				}
			},
			wantErr: "requires spec.gnoi.transportSecurity to be tls",
		},
		{
			name: "port environment override wins while insecure",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
				}
			},
			forceInsecure: true,
			portEnv:       "29339",
			wantPort:      29339,
		},
		{
			name: "invalid port environment fails closed",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{Address: "192.0.2.1"}
			},
			portEnv: "not-a-port",
			wantErr: gNOIPortEnv,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(gNOIPortEnv, tt.portEnv)
			resolved, err := resolveGNOIConfig(tt.spec(), "", tt.forceInsecure, t.TempDir(), false)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveGNOIConfig error=%v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveGNOIConfig: %v", err)
			}
			if resolved.port != tt.wantPort {
				t.Errorf("port=%d, want %d", resolved.port, tt.wantPort)
			}
		})
	}
}

func TestSetupGNOIRejectsInvalidPerDeviceTransport(t *testing.T) {
	t.Setenv(gNOIDisabledEnv, "")
	t.Setenv(gNOIInsecureEnv, "")
	t.Setenv(gNOIPortEnv, "")

	provider, provisioner, cleanup, err := setupGNOI(context.Background(), configReconcilerOptions{
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
	if provider != nil || provisioner != nil || cleanup != nil {
		t.Fatalf("setupGNOI returned provider=%T provisioner=%T cleanup=%v after validation error", provider, provisioner, cleanup != nil)
	}
}

func TestLoadGNOIProvisioningBundleIsOptInAndScopesTrustToGNOI(t *testing.T) {
	directory, leaf := writeGNOIProvisioningFiles(t, "router.example.test")
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		GNOI: &ciskov1.GNOIConfig{
			TransportSecurity: ciskov1.GNOITransportSecurityTLS,
		},
		XE: &ciskov1.XEConfig{
			GNOI: &ciskov1.XEGNOIConfig{
				CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
					CertificateID:         "cvk-gnoi",
					ReplaceTargetCABundle: true,
				},
			},
		},
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	bundle, err := loadGNOIProvisioningBundle(spec, tlsCfg, directory, true)
	if err != nil {
		t.Fatalf("loadGNOIProvisioningBundle: %v", err)
	}
	if bundle == nil || bundle.CertificateID() != "cvk-gnoi" {
		t.Fatalf("bundle=%v certificateID=%q", bundle, bundle.CertificateID())
	}
	signer, err := loadGNOILocalCertificateSigner(bundle, directory)
	if err != nil {
		t.Fatalf("loadGNOILocalCertificateSigner: %v", err)
	}
	if signer == nil {
		t.Fatal("write-enabled provisioning did not construct a signer")
	}
	if tlsCfg.RootCAs == nil {
		t.Fatal("gNOI TLS config did not receive provisioning CA roots")
	}
	if err := tlsCfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err != nil {
		t.Fatalf("provisioned leaf-only TLS verification: %v", err)
	}

	// Absence of the block must neither read files nor mutate TLS roots.
	legacyTLS := &tls.Config{MinVersion: tls.VersionTLS12}
	got, err := loadGNOIProvisioningBundle(
		&ciskov1.DeviceSpec{Address: "router.example.test"},
		legacyTLS,
		filepath.Join(t.TempDir(), "does-not-exist"),
		false,
	)
	if err != nil || got != nil {
		t.Fatalf("omitted provisioning block returned bundle=%v err=%v", got, err)
	}
	if legacyTLS.RootCAs != nil {
		t.Fatal("omitted provisioning block mutated the legacy TLS config")
	}
}

func TestLoadGNOIProvisioningBundleBootstrapMaterialIsWriteScoped(t *testing.T) {
	directory, _ := writeGNOIProvisioningFiles(t, "router.example.test")
	if err := os.Remove(filepath.Join(directory, gNOIProvisioningCAKeyFile)); err != nil {
		t.Fatalf("remove CA key: %v", err)
	}
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		GNOI: &ciskov1.GNOIConfig{
			TransportSecurity: ciskov1.GNOITransportSecurityTLS,
		},
		XE: &ciskov1.XEConfig{
			GNOI: &ciskov1.XEGNOIConfig{
				CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
					CertificateID:         "cvk-gnoi",
					ReplaceTargetCABundle: true,
				},
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(spec, &tls.Config{}, directory, true)
	if err != nil || bundle == nil {
		t.Fatalf("missing optional ca.key returned bundle=%v err=%v", bundle, err)
	}
	signer, err := loadGNOILocalCertificateSigner(bundle, directory)
	if err != nil || signer != nil {
		t.Fatalf("missing optional ca.key returned signer=%T err=%v", signer, err)
	}
	if err := os.WriteFile(filepath.Join(directory, gNOIProvisioningCAKeyFile), []byte("not PEM"), 0o600); err != nil {
		t.Fatalf("write malformed CA key: %v", err)
	}
	if bundle, err = loadGNOIProvisioningBundle(spec, &tls.Config{}, directory, true); err != nil || bundle == nil {
		t.Fatalf("malformed private signer affected public bundle: bundle=%v err=%v", bundle, err)
	}
	if signer, err = loadGNOILocalCertificateSigner(bundle, directory); err == nil || !strings.Contains(err.Error(), "private key is not PEM encoded") {
		t.Fatalf("local signer load returned signer=%T err=%v, want malformed CA-key error", signer, err)
	}
	if err := os.WriteFile(filepath.Join(directory, gNOIProvisioningBootstrapFile), []byte("not PEM"), 0o600); err != nil {
		t.Fatalf("write malformed bootstrap certificate: %v", err)
	}
	if bundle, err = loadGNOIProvisioningBundle(spec, &tls.Config{}, directory, false); err != nil || bundle == nil {
		t.Fatalf("read-only load touched write-scoped material: bundle=%v err=%v", bundle, err)
	}
}

func TestLocalSignerFailurePreservesPublicProvisioningTrust(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, string)
		wantErr string
	}{
		{
			name: "malformed ca.key",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, gNOIProvisioningCAKeyFile), []byte("not PEM"), 0o600); err != nil {
					t.Fatalf("write malformed ca.key: %v", err)
				}
			},
			wantErr: "private key is not PEM encoded",
		},
		{
			name: "unreadable ca.key",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				path := filepath.Join(directory, gNOIProvisioningCAKeyFile)
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove ca.key: %v", err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("replace ca.key with directory: %v", err)
				}
			},
			wantErr: "read ca.key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory, leaf := writeGNOIProvisioningFiles(t, "router.example.test")
			spec := provisioningDeviceSpec("router.example.test")
			tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
			bundle, err := loadGNOIProvisioningBundle(spec, tlsCfg, directory, true)
			if err != nil || bundle == nil {
				t.Fatalf("load public provisioning bundle: bundle=%v err=%v", bundle, err)
			}

			tt.mutate(t, directory)
			signer, err := loadGNOILocalCertificateSigner(bundle, directory)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("load signer returned signer=%T err=%v, want %q", signer, err, tt.wantErr)
			}
			if signer != nil {
				t.Fatalf("failed signer load returned signer=%T", signer)
			}
			if tlsCfg.RootCAs == nil || tlsCfg.VerifyConnection == nil {
				t.Fatal("signer failure removed configured public trust")
			}
			if err := tlsCfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}); err != nil {
				t.Fatalf("public provisioning trust after signer failure: %v", err)
			}
		})
	}
}

func TestSetupGNOISignerFailureKeepsBaseProvider(t *testing.T) {
	t.Setenv(gNOIDisabledEnv, "")
	t.Setenv(gNOIInsecureEnv, "")
	t.Setenv(gNOIPortEnv, "")

	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "missing ca.key",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				if err := os.Remove(filepath.Join(directory, gNOIProvisioningCAKeyFile)); err != nil {
					t.Fatalf("remove ca.key: %v", err)
				}
			},
		},
		{
			name: "malformed ca.key",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, gNOIProvisioningCAKeyFile), []byte("not PEM"), 0o600); err != nil {
					t.Fatalf("write malformed ca.key: %v", err)
				}
			},
		},
		{
			name: "unreadable ca.key",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				path := filepath.Join(directory, gNOIProvisioningCAKeyFile)
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove ca.key: %v", err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("replace ca.key with directory: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const address = "192.0.2.10"
			directory, _ := writeGNOIProvisioningFiles(t, address)
			tt.mutate(t, directory)
			spec := provisioningDeviceSpec(address)
			spec.Username = "cisco"

			provider, provisioner, cleanup, err := setupGNOIWithProvisioningDirectory(
				context.Background(),
				configReconcilerOptions{
					Spec:                 spec,
					Password:             "cisco",
					EnableWriteClassGNOI: true,
				},
				directory,
			)
			if err != nil {
				t.Fatalf("setupGNOIWithProvisioningDirectory: %v", err)
			}
			if provider == nil || cleanup == nil {
				t.Fatalf("signer failure disabled base provider=%T cleanup=%v", provider, cleanup != nil)
			}
			t.Cleanup(cleanup)
			if provisioner != nil {
				t.Fatalf("signer failure returned provisioner=%T", provisioner)
			}
			client, err := provider.GNOIClient(context.Background())
			if err != nil || client == nil {
				t.Fatalf("base provider client=%v err=%v", client, err)
			}
		})
	}
}

func provisioningDeviceSpec(address string) *ciskov1.DeviceSpec {
	return &ciskov1.DeviceSpec{
		Driver:  ciskov1.DeviceDriverXE,
		Address: address,
		TLS:     &ciskov1.TLSConfig{Enabled: true},
		GNOI: &ciskov1.GNOIConfig{
			TransportSecurity: ciskov1.GNOITransportSecurityTLS,
		},
		XE: &ciskov1.XEConfig{
			GNOI: &ciskov1.XEGNOIConfig{
				CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
					CertificateID:         "cvk-gnoi",
					SecretRef:             ciskov1.XEGNOIProvisioningSecretReference{Name: "gnoi-provisioning"},
					ReplaceTargetCABundle: true,
				},
			},
		},
	}
}

func TestLoadGNOIProvisioningBundleRejectsPlaintext(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		XE: &ciskov1.XEConfig{
			GNOI: &ciskov1.XEGNOIConfig{
				CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
					CertificateID:         "cvk-gnoi",
					ReplaceTargetCABundle: true,
				},
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(spec, nil, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "TLS transport is required") {
		t.Fatalf("bundle=%v err=%v, want TLS-required error", bundle, err)
	}
}

func TestLoadGNOIProvisioningBundleRejectsUnverifiedTLS(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		XE: &ciskov1.XEConfig{
			GNOI: &ciskov1.XEGNOIConfig{
				CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
					CertificateID:         "cvk-gnoi",
					ReplaceTargetCABundle: true,
				},
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(
		spec,
		&tls.Config{InsecureSkipVerify: true}, //nolint:gosec // verifies fail-closed validation
		t.TempDir(),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "verified TLS is required") {
		t.Fatalf("bundle=%v err=%v, want verified-TLS error", bundle, err)
	}
}

func TestLoadGNOIProvisioningBundleRequiresTargetCABundleAcknowledgement(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		GNOI: &ciskov1.GNOIConfig{
			TransportSecurity: ciskov1.GNOITransportSecurityTLS,
		},
		XE: &ciskov1.XEConfig{
			GNOI: &ciskov1.XEGNOIConfig{
				CertificateProvisioning: &ciskov1.XEGNOICertificateProvisioning{
					CertificateID: "cvk-gnoi",
				},
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(spec, &tls.Config{}, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "replaceTargetCABundle must be true") {
		t.Fatalf("bundle=%v err=%v, want CA-bundle acknowledgement error", bundle, err)
	}
}

func writeGNOIProvisioningFiles(t *testing.T, serverName string) (string, *x509.Certificate) {
	t.Helper()
	now := time.Now()
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate root key: %v", err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CVK test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root certificate: %v", err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatalf("parse root certificate: %v", err)
	}

	issuerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate issuer key: %v", err)
	}
	issuerTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "CVK test gNOI issuer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(12 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	issuerDER, err := x509.CreateCertificate(rand.Reader, issuerTemplate, root, &issuerKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create issuer certificate: %v", err)
	}
	issuer, err := x509.ParseCertificate(issuerDER)
	if err != nil {
		t.Fatalf("parse issuer certificate: %v", err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			CommonName:         serverName,
			Country:            []string{"US"},
			Province:           []string{"California"},
			Organization:       []string{"Cisco"},
			OrganizationalUnit: []string{"gNOI"},
		},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(serverName); ip != nil {
		leafTemplate.IPAddresses = []net.IP{ip}
	} else {
		leafTemplate.DNSNames = []string{serverName}
		leafTemplate.IPAddresses = []net.IP{net.ParseIP("192.0.2.10")}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, issuer, &leafKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}

	directory := t.TempDir()
	files := map[string][]byte{
		gNOIProvisioningCertFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		gNOIProvisioningCAFile: append(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuerDER}),
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})...,
		),
		gNOIProvisioningCAKeyFile: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(issuerKey)}),
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return directory, leaf
}
