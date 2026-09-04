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

func TestGNOIDialConfigAttachesCredentialsOnlyToTLS(t *testing.T) {
	tests := []struct {
		name        string
		tlsEnabled  bool
		username    string
		password    string
		skipVerify  bool
		explicitTLS bool
		wantTLS     bool
		wantRPCAuth bool
		wantBasic   bool
		wantErr     string
	}{
		{name: "explicit secure TLS with credentials", tlsEnabled: true, explicitTLS: true, username: "admin", password: "s3cret", wantTLS: true, wantRPCAuth: true},
		{name: "legacy TLS keeps context auth", tlsEnabled: true, username: "admin", password: "s3cret", wantTLS: true, wantBasic: true},
		{name: "legacy TLS without username", tlsEnabled: true, password: "s3cret", wantTLS: true},
		{name: "explicit TLS without username", tlsEnabled: true, explicitTLS: true, password: "s3cret", wantErr: "requires both username and password"},
		{name: "explicit TLS without password", tlsEnabled: true, explicitTLS: true, username: "admin", wantErr: "requires both username and password"},
		{name: "explicit TLS without password authentication", tlsEnabled: true, explicitTLS: true, wantTLS: true},
		{name: "unverified explicit TLS with credentials", tlsEnabled: true, explicitTLS: true, username: "admin", password: "s3cret", skipVerify: true, wantErr: "verified TLS is required"},
		{name: "unverified explicit TLS without credentials", tlsEnabled: true, explicitTLS: true, skipVerify: true, wantErr: "verified TLS is required"},
		{name: "plaintext with credentials", username: "admin", password: "s3cret", wantBasic: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ciskov1.DeviceSpec{
				Username: tt.username,
				TLS:      &ciskov1.TLSConfig{InsecureSkipVerify: tt.skipVerify},
			}
			if tt.explicitTLS {
				spec.GNOI = &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityTLS}
			}
			cfg, err := gnoiDialConfig(spec, tt.password, tt.tlsEnabled)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("gnoiDialConfig error=%v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("gnoiDialConfig: %v", err)
			}
			if got := cfg.TLSConfig != nil; got != tt.wantTLS {
				t.Errorf("TLSConfig present=%v, want %v", got, tt.wantTLS)
			}
			if got := cfg.RPCCredentials != nil; got != tt.wantRPCAuth {
				t.Errorf("RPCCredentials present=%v, want %v", got, tt.wantRPCAuth)
			}
			if got := cfg.Username != ""; got != tt.wantBasic {
				t.Errorf("legacy Basic credentials present=%v, want %v", got, tt.wantBasic)
			}
		})
	}
}

func TestUnavailableGNOIProviderPreservesSetupError(t *testing.T) {
	provider := unavailableGNOIProvider{cause: errors.New("invalid provisioning material")}
	if _, err := provider.GNOIClient(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid provisioning material") {
		t.Fatalf("GNOIClient error=%v, want original setup failure", err)
	}
}

func TestGNOITransportForSpec(t *testing.T) {
	tests := []struct {
		name        string
		spec        func() *ciskov1.DeviceSpec
		insecureEnv string
		portEnv     string
		wantPort    int
		wantTLS     bool
		wantErr     string
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
			name: "zero-value block preserves legacy nonstandard port",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1", Port: 10443,
					TLS:  &ciskov1.TLSConfig{Enabled: true},
					GNOI: &ciskov1.GNOIConfig{TransportSecurity: ciskov1.GNOITransportSecurityAuto},
				}
			},
			wantPort: 10443,
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
			name: "insecure environment cannot override explicit TLS",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
					GNOI: &ciskov1.GNOIConfig{
						Port: 19339, TransportSecurity: ciskov1.GNOITransportSecurityTLS,
					},
				}
			},
			insecureEnv: "true",
			wantErr:     "cannot override explicit",
		},
		{
			name: "port environment override wins while insecure",
			spec: func() *ciskov1.DeviceSpec {
				return &ciskov1.DeviceSpec{
					Address: "192.0.2.1",
				}
			},
			insecureEnv: "true",
			portEnv:     "29339",
			wantPort:    29339,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(gNOIPortEnv, tt.portEnv)
			forceInsecure := tt.insecureEnv != ""
			port, tlsEnabled, err := gnoiTransportForSpec(tt.spec(), forceInsecure)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("gnoiTransportForSpec error=%v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("gnoiTransportForSpec: %v", err)
			}
			if port != tt.wantPort {
				t.Errorf("port=%d, want %d", port, tt.wantPort)
			}
			if tlsEnabled != tt.wantTLS {
				t.Errorf("tls=%v, want %v", tlsEnabled, tt.wantTLS)
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
			CertificateProvisioning: &ciskov1.GNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi",
				ReplaceTargetCABundle: true,
			},
		},
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	bundle, err := loadGNOIProvisioningBundle(spec, true, tlsCfg, directory, true)
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
		true,
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
			CertificateProvisioning: &ciskov1.GNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi",
				ReplaceTargetCABundle: true,
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(spec, true, &tls.Config{}, directory, true)
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
	if bundle, err = loadGNOIProvisioningBundle(spec, true, &tls.Config{}, directory, true); err != nil || bundle == nil {
		t.Fatalf("malformed private signer affected public bundle: bundle=%v err=%v", bundle, err)
	}
	if signer, err = loadGNOILocalCertificateSigner(bundle, directory); err == nil || !strings.Contains(err.Error(), "private key is not PEM encoded") {
		t.Fatalf("local signer load returned signer=%T err=%v, want malformed CA-key error", signer, err)
	}
	if err := os.WriteFile(filepath.Join(directory, gNOIProvisioningBootstrapFile), []byte("not PEM"), 0o600); err != nil {
		t.Fatalf("write malformed bootstrap certificate: %v", err)
	}
	if bundle, err = loadGNOIProvisioningBundle(spec, true, &tls.Config{}, directory, false); err != nil || bundle == nil {
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
			bundle, err := loadGNOIProvisioningBundle(spec, true, tlsCfg, directory, true)
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
		Address: address,
		TLS:     &ciskov1.TLSConfig{Enabled: true},
		GNOI: &ciskov1.GNOIConfig{
			TransportSecurity: ciskov1.GNOITransportSecurityTLS,
			CertificateProvisioning: &ciskov1.GNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi",
				SecretRef:             ciskov1.GNOIProvisioningSecretReference{Name: "gnoi-provisioning"},
				ReplaceTargetCABundle: true,
			},
		},
	}
}

func TestLoadGNOIProvisioningBundleRejectsPlaintext(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		GNOI: &ciskov1.GNOIConfig{
			CertificateProvisioning: &ciskov1.GNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi",
				ReplaceTargetCABundle: true,
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(spec, false, nil, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "TLS transport is required") {
		t.Fatalf("bundle=%v err=%v, want TLS-required error", bundle, err)
	}
}

func TestLoadGNOIProvisioningBundleRejectsUnverifiedTLS(t *testing.T) {
	spec := &ciskov1.DeviceSpec{
		Address: "router.example.test",
		GNOI: &ciskov1.GNOIConfig{
			CertificateProvisioning: &ciskov1.GNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi",
				ReplaceTargetCABundle: true,
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(
		spec,
		true,
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
			CertificateProvisioning: &ciskov1.GNOICertificateProvisioning{
				CertificateID: "cvk-gnoi",
			},
		},
	}
	bundle, err := loadGNOIProvisioningBundle(spec, true, &tls.Config{}, t.TempDir(), false)
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
