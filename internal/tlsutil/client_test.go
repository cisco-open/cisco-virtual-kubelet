// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// writeTestCA writes a freshly generated self-signed CA certificate PEM to
// dir and returns its path.
func writeTestCA(t *testing.T, dir string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cvk-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write CA pem: %v", err)
	}
	return path
}

func TestClientTLSFromDeviceTLS(t *testing.T) {
	dir := t.TempDir()
	caPath := writeTestCA(t, dir)
	badPEM := filepath.Join(dir, "not-a-cert.pem")
	if err := os.WriteFile(badPEM, []byte("this is not PEM"), 0o600); err != nil {
		t.Fatalf("write bad pem: %v", err)
	}

	tests := []struct {
		name        string
		in          *ciskov1.TLSConfig
		wantErr     bool
		wantRootCAs bool
		wantSkip    bool
	}{
		{
			name: "nil spec yields TLS 1.2 default",
			in:   nil,
		},
		{
			name:     "insecureSkipVerify is copied",
			in:       &ciskov1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
			wantSkip: true,
		},
		{
			name:        "caFile is loaded into RootCAs",
			in:          &ciskov1.TLSConfig{Enabled: true, CAFile: caPath},
			wantRootCAs: true,
		},
		{
			name:    "missing caFile is an error",
			in:      &ciskov1.TLSConfig{Enabled: true, CAFile: filepath.Join(dir, "absent.pem")},
			wantErr: true,
		},
		{
			name:    "unparseable caFile is an error",
			in:      &ciskov1.TLSConfig{Enabled: true, CAFile: badPEM},
			wantErr: true,
		},
		{
			name:    "mismatched client pair is an error",
			in:      &ciskov1.TLSConfig{Enabled: true, CertFile: caPath, KeyFile: badPEM},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ClientTLSFromDeviceTLS(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got config %#v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Errorf("MinVersion = %d, want TLS 1.2", cfg.MinVersion)
			}
			if got := cfg.RootCAs != nil; got != tc.wantRootCAs {
				t.Errorf("RootCAs set = %v, want %v", got, tc.wantRootCAs)
			}
			if cfg.InsecureSkipVerify != tc.wantSkip {
				t.Errorf("InsecureSkipVerify = %v, want %v", cfg.InsecureSkipVerify, tc.wantSkip)
			}
		})
	}
}
