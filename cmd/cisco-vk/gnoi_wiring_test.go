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
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
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

func TestLoadGNOIProvisioningBundleIsOptInAndScopesTrustToGNOI(t *testing.T) {
	directory, leaf, _ := writeGNOIProvisioningFiles(t, "router.example.test")
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

	bundle, err := loadGNOIProvisioningBundle(spec, true, tlsCfg, directory)
	if err != nil {
		t.Fatalf("loadGNOIProvisioningBundle: %v", err)
	}
	if bundle == nil || bundle.CertificateID() != "cvk-gnoi" {
		t.Fatalf("bundle=%v certificateID=%q", bundle, bundle.CertificateID())
	}
	if tlsCfg.RootCAs == nil {
		t.Fatal("gNOI TLS config did not receive provisioning CA roots")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: "router.example.test",
		Roots:   tlsCfg.RootCAs,
		KeyUsages: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}); err != nil {
		t.Fatalf("provisioned leaf does not verify against gNOI roots: %v", err)
	}

	// Absence of the block must neither read files nor mutate TLS roots.
	legacyTLS := &tls.Config{MinVersion: tls.VersionTLS12}
	got, err := loadGNOIProvisioningBundle(
		&ciskov1.DeviceSpec{Address: "router.example.test"},
		true,
		legacyTLS,
		filepath.Join(t.TempDir(), "does-not-exist"),
	)
	if err != nil || got != nil {
		t.Fatalf("omitted provisioning block returned bundle=%v err=%v", got, err)
	}
	if legacyTLS.RootCAs != nil {
		t.Fatal("omitted provisioning block mutated the legacy TLS config")
	}
}

func TestLoadGNOIProvisioningBundleTargetCSRDoesNotRequireLeafPrivateKey(t *testing.T) {
	directory, _, caKeyPEM := writeGNOIProvisioningFiles(t, "router.example.test")
	if err := os.WriteFile(filepath.Join(directory, gNOIProvisioningCAKeyFile), caKeyPEM, 0o600); err != nil {
		t.Fatalf("write %s: %v", gNOIProvisioningCAKeyFile, err)
	}
	if err := os.Remove(filepath.Join(directory, gNOIProvisioningKeyFile)); err != nil {
		t.Fatalf("remove external leaf key: %v", err)
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
	bundle, err := loadGNOIProvisioningBundle(spec, true, &tls.Config{}, directory)
	if err != nil {
		t.Fatalf("load target-CSR provisioning bundle without tls.key: %v", err)
	}
	if bundle == nil {
		t.Fatal("target-CSR provisioning bundle is nil")
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
	bundle, err := loadGNOIProvisioningBundle(spec, false, nil, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "TLS transport is required") {
		t.Fatalf("bundle=%v err=%v, want TLS-required error", bundle, err)
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
	bundle, err := loadGNOIProvisioningBundle(spec, true, &tls.Config{}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "replaceTargetCABundle must be true") {
		t.Fatalf("bundle=%v err=%v, want CA-bundle acknowledgement error", bundle, err)
	}
}

func TestPooledGNOIProviderSerializesCertificateInstallAndResetsClient(t *testing.T) {
	bundle := testGNOIProvisioningBundle(t)
	current := &gnoi.Client{}
	var stateCalls atomic.Int32
	var installCalls atomic.Int32
	p := &pooledGNOIProvider{
		client:             current,
		provisioningBundle: bundle,
		provisioningState: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error) {
			stateCalls.Add(1)
			return gnoi.ProvisioningCertificateMissing, nil
		},
		installProvisioningCertificate: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) error {
			installCalls.Add(1)
			return nil
		},
	}

	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- p.provisionGNOICertificate(context.Background(), current)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		var progress *gnoi.ErrProvisioningInProgress
		if !errors.As(err, &progress) {
			t.Fatalf("provision error=%T %v, want ErrProvisioningInProgress", err, err)
		}
	}
	if got := stateCalls.Load(); got != 1 {
		t.Fatalf("certificate state calls=%d, want 1", got)
	}
	if got := installCalls.Load(); got != 1 {
		t.Fatalf("certificate install calls=%d, want 1", got)
	}
	if p.client != nil {
		t.Fatal("provider retained stale client after certificate install")
	}
}

func TestPooledGNOIProviderStaleCallbackPreservesProvisioningState(t *testing.T) {
	bundle := testGNOIProvisioningBundle(t)
	stale := &gnoi.Client{}
	current := &gnoi.Client{}

	for _, active := range []bool{false, true} {
		p := &pooledGNOIProvider{
			client:             current,
			provisioningBundle: bundle,
			provisioningState: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error) {
				t.Fatal("stale callback must not inspect certificate state")
				return gnoi.ProvisioningCertificateMissing, nil
			},
		}
		p.provisioningActive.Store(active)

		err := p.provisionGNOICertificate(context.Background(), stale)
		var progress *gnoi.ErrProvisioningInProgress
		if !errors.As(err, &progress) {
			t.Fatalf("active=%v: error=%T %v, want ErrProvisioningInProgress", active, err, err)
		}
		if got := p.GNOICertificateProvisioningInProgress(); got != active {
			t.Fatalf("active=%v: stale callback changed provisioning state to %v", active, got)
		}
	}
}

func TestPooledGNOIProviderMatchingCertificateGetsOneRestartGrace(t *testing.T) {
	bundle := testGNOIProvisioningBundle(t)
	first := &gnoi.Client{}
	p := &pooledGNOIProvider{
		client:             first,
		provisioningBundle: bundle,
		provisioningState: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error) {
			return gnoi.ProvisioningCertificateMatching, nil
		},
		installProvisioningCertificate: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) error {
			t.Fatal("matching certificate must not be reinstalled")
			return nil
		},
	}

	err := p.provisionGNOICertificate(context.Background(), first)
	var progress *gnoi.ErrProvisioningInProgress
	if !errors.As(err, &progress) {
		t.Fatalf("first matching observation error=%T %v, want progress", err, err)
	}
	if p.client != nil {
		t.Fatal("provider retained stale client after first matching observation")
	}

	second := &gnoi.Client{}
	p.client = second
	err = p.provisionGNOICertificate(context.Background(), second)
	if err == nil || !strings.Contains(err.Error(), "secure trustpoint binding") {
		t.Fatalf("second matching observation err=%v, want binding error", err)
	}
	if errors.As(err, &progress) {
		t.Fatalf("second matching observation remained transient: %v", err)
	}
}

func TestPooledGNOIProviderIndeterminateInstallForcesReconnect(t *testing.T) {
	bundle := testGNOIProvisioningBundle(t)
	current := &gnoi.Client{}
	indeterminate := &gnoi.ErrCertificateInstallIndeterminate{
		CertificateID: bundle.CertificateID(),
		Cause:         io.EOF,
	}
	p := &pooledGNOIProvider{
		client:             current,
		provisioningBundle: bundle,
		provisioningState: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error) {
			return gnoi.ProvisioningCertificateMissing, nil
		},
		installProvisioningCertificate: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) error {
			return indeterminate
		},
	}

	err := p.provisionGNOICertificate(context.Background(), current)
	var progress *gnoi.ErrProvisioningInProgress
	if !errors.As(err, &progress) {
		t.Fatalf("error=%T %v, want ErrProvisioningInProgress", err, err)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("progress error does not preserve indeterminate cause: %v", err)
	}
	if p.client != nil {
		t.Fatal("provider retained stale client after indeterminate install")
	}
}

func TestPooledGNOIProviderAlreadyExistingUnlistedIDIsNotResubmitted(t *testing.T) {
	bundle := testGNOIProvisioningBundle(t)
	first := &gnoi.Client{}
	alreadyExists := &gnoi.ErrCertificateInstallIndeterminate{
		CertificateID: bundle.CertificateID(),
		Cause:         status.Error(codes.AlreadyExists, "trustpoint already exists"),
	}
	installCalls := 0
	p := &pooledGNOIProvider{
		client:             first,
		provisioningBundle: bundle,
		provisioningState: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) (gnoi.ProvisioningCertificateState, error) {
			return gnoi.ProvisioningCertificateMissing, nil
		},
		installProvisioningCertificate: func(context.Context, *gnoi.Client, *gnoi.ProvisioningBundle) error {
			installCalls++
			return alreadyExists
		},
	}

	err := p.provisionGNOICertificate(context.Background(), first)
	var progress *gnoi.ErrProvisioningInProgress
	if !errors.As(err, &progress) {
		t.Fatalf("first error=%T %v, want ErrProvisioningInProgress", err, err)
	}
	if installCalls != 1 || p.client != nil {
		t.Fatalf("after first attempt installCalls=%d client=%p, want one attempt and reset client", installCalls, p.client)
	}

	second := &gnoi.Client{}
	p.client = second
	err = p.provisionGNOICertificate(context.Background(), second)
	if err == nil || !strings.Contains(err.Error(), "reported as already existing") || !strings.Contains(err.Error(), "new unused certificateID") {
		t.Fatalf("second error=%v, want stale/reserved certificate ID guidance", err)
	}
	if installCalls != 1 {
		t.Fatalf("Install was resubmitted %d times after AlreadyExists, want one total attempt", installCalls)
	}
	if p.GNOICertificateProvisioningInProgress() {
		t.Fatal("provider remained transient after the AlreadyExists state recheck")
	}

	// Keep the condition sticky so another OS.Verify cannot restart the loop.
	err = p.provisionGNOICertificate(context.Background(), second)
	if err == nil || installCalls != 1 {
		t.Fatalf("later retry error=%v installCalls=%d, want no resubmission", err, installCalls)
	}
}

func TestTransientGNOIProvisioningErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "unavailable", err: status.Error(codes.Unavailable, "listener restarting"), want: true},
		{name: "aborted", err: status.Error(codes.Aborted, "IOS XE certificate event busy"), want: true},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "authentication", err: status.Error(codes.Unauthenticated, "bad credentials")},
		{name: "plain error", err: errors.New("bad certificate")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientGNOIProvisioningError(tt.err); got != tt.want {
				t.Fatalf("isTransientGNOIProvisioningError(%v)=%v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func testGNOIProvisioningBundle(t *testing.T) *gnoi.ProvisioningBundle {
	t.Helper()
	directory, _, _ := writeGNOIProvisioningFiles(t, "router.example.test")
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
	bundle, err := loadGNOIProvisioningBundle(spec, true, &tls.Config{}, directory)
	if err != nil {
		t.Fatalf("load test provisioning bundle: %v", err)
	}
	return bundle
}

func writeGNOIProvisioningFiles(t *testing.T, serverName string) (string, *x509.Certificate, []byte) {
	t.Helper()
	now := time.Now()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CVK test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
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
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
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
		gNOIProvisioningKeyFile:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}),
		gNOIProvisioningCAFile:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), contents, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return directory, leaf, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)})
}
