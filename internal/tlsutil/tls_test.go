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

package tlsutil

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tlsConfigValid is a minimal sanity check shared by multiple tests.
func tlsConfigValid(t *testing.T, cfg *tls.Config) {
	t.Helper()
	if cfg == nil {
		t.Fatal("got nil tls.Config")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", cfg.MinVersion, tls.VersionTLS12)
	}
}

// TestEnsureTLSConfig_Generate verifies that when neither file exists a
// self-signed cert is generated, the PEM files are written to disk, and the
// returned TLS config is fully valid.
func TestEnsureTLSConfig_Generate(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	cfg, err := EnsureTLSConfig(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("EnsureTLSConfig() error = %v", err)
	}
	tlsConfigValid(t, cfg)

	if _, err := os.Stat(certFile); err != nil {
		t.Errorf("cert file not written: %v", err)
	}
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("key file not written: %v", err)
	}
}

// TestEnsureTLSConfig_IPAddressSAN verifies that when the device address is an
// IP it is included in the certificate's IPAddresses SANs.
func TestEnsureTLSConfig_IPAddressSAN(t *testing.T) {
	dir := t.TempDir()
	cfg, err := EnsureTLSConfig(
		filepath.Join(dir, "tls.crt"),
		filepath.Join(dir, "tls.key"),
		"192.168.1.1",
	)
	if err != nil {
		t.Fatalf("EnsureTLSConfig() error = %v", err)
	}

	cert, err := parseCertFromConfig(cfg)
	if err != nil {
		t.Fatalf("parseCertFromConfig() error = %v", err)
	}

	want := net.ParseIP("192.168.1.1")
	for _, ip := range cert.IPAddresses {
		if ip.Equal(want) {
			return
		}
	}
	t.Errorf("IP SAN 192.168.1.1 not found in cert; got %v", cert.IPAddresses)
}

// TestEnsureTLSConfig_DNSSANHostname verifies that a non-IP device address is
// added to the certificate's DNSNames SANs.
func TestEnsureTLSConfig_DNSSANHostname(t *testing.T) {
	dir := t.TempDir()
	cfg, err := EnsureTLSConfig(
		filepath.Join(dir, "tls.crt"),
		filepath.Join(dir, "tls.key"),
		"device.example.com",
	)
	if err != nil {
		t.Fatalf("EnsureTLSConfig() error = %v", err)
	}

	cert, err := parseCertFromConfig(cfg)
	if err != nil {
		t.Fatalf("parseCertFromConfig() error = %v", err)
	}

	for _, name := range cert.DNSNames {
		if name == "device.example.com" {
			return
		}
	}
	t.Errorf("DNS SAN device.example.com not found; got %v", cert.DNSNames)
}

// TestEnsureTLSConfig_LoadFromDisk verifies that when both PEM files already
// exist on disk EnsureTLSConfig loads them (the load path) and returns a valid
// config. Calling it twice also proves that generated certs survive a restart.
func TestEnsureTLSConfig_LoadFromDisk(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	// First call generates and writes the files.
	first, err := EnsureTLSConfig(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("first EnsureTLSConfig() error = %v", err)
	}

	// Second call must load from disk — same cert bytes.
	second, err := EnsureTLSConfig(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("second EnsureTLSConfig() error = %v", err)
	}
	tlsConfigValid(t, second)

	c1 := first.Certificates[0].Certificate[0]
	c2 := second.Certificates[0].Certificate[0]
	if string(c1) != string(c2) {
		t.Error("second call returned a different certificate; expected same cert loaded from disk")
	}
}

// TestEnsureTLSConfig_MisconfigOnlyOnefile verifies that having exactly one of
// the two files present returns an actionable error.
func TestEnsureTLSConfig_MisconfigOnlyOneFile(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	// Write only the cert file.
	if err := os.WriteFile(certFile, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := EnsureTLSConfig(certFile, keyFile, "")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "tls misconfiguration") {
		t.Errorf("error %q does not mention tls misconfiguration", err.Error())
	}
}
