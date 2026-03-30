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

// Package tlsutil provides TLS certificate helpers for the kubelet HTTPS listener.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultCertFile is the preferred path for the kubelet TLS certificate.
	// Mount a Kubernetes Secret of type kubernetes.io/tls here to have it
	// take precedence over any generated certificate.
	DefaultCertFile = "/etc/virtual-kubelet/tls/tls.crt"

	// DefaultKeyFile is the preferred path for the kubelet TLS private key.
	DefaultKeyFile = "/etc/virtual-kubelet/tls/tls.key"

	// DefaultGenCertFile is the writable fallback path where a self-signed
	// certificate is written when the preferred path does not exist.
	// /var/lib is writable in containers even when /etc is read-only.
	DefaultGenCertFile = "/var/lib/virtual-kubelet/tls/tls.crt"

	// DefaultGenKeyFile is the writable fallback path for the generated key.
	DefaultGenKeyFile = "/var/lib/virtual-kubelet/tls/tls.key"
)

// EnsureTLSConfig returns a *tls.Config for the kubelet HTTPS listener.
//
// Behaviour:
//   - If both certFile and keyFile exist on disk, they are loaded and returned.
//     This is the expected path when a Kubernetes Secret of type
//     kubernetes.io/tls is mounted at the certFile/keyFile location.
//   - If neither certFile nor keyFile exists, a self-signed ECDSA certificate
//     is generated and written to genCertFile/genKeyFile (parent directories
//     are created as needed). Using a separate writable path for generation
//     allows certFile/keyFile to live under a read-only ConfigMap mount.
//     Persisting the generated files keeps the fingerprint stable across
//     restarts.
//   - If exactly one of certFile/keyFile is present, an error is returned;
//     this typically indicates a partial or misconfigured Secret mount.
//
// deviceAddr is added as a Subject Alternative Name when generating a
// self-signed certificate so that both local and remote health checks pass.
func EnsureTLSConfig(certFile, keyFile, genCertFile, genKeyFile, deviceAddr string) (*tls.Config, error) {
	certExists := fileExists(certFile)
	keyExists := fileExists(keyFile)

	switch {
	case certExists && keyExists:
		return loadTLSConfig(certFile, keyFile)
	case !certExists && !keyExists:
		return generateAndWrite(genCertFile, genKeyFile, deviceAddr)
	default:
		return nil, fmt.Errorf(
			"tls misconfiguration: only one of %q / %q is present; provide both or neither",
			certFile, keyFile,
		)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS key pair from %q / %q: %w", certFile, keyFile, err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func generateAndWrite(certFile, keyFile, deviceAddr string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ECDSA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial number: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Cisco Virtual Kubelet"},
			CommonName:   "cisco-virtual-kubelet",
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	if ip := net.ParseIP(deviceAddr); ip != nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	} else if deviceAddr != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, deviceAddr)
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal EC private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("assemble generated key pair: %w", err)
	}

	if err := writePEMFiles(certFile, keyFile, certPEM, keyPEM); err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func writePEMFiles(certFile, keyFile string, certPEM, keyPEM []byte) error {
	for _, dir := range []string{filepath.Dir(certFile), filepath.Dir(keyFile)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create tls directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		return fmt.Errorf("write cert file %q: %w", certFile, err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key file %q: %w", keyFile, err)
	}
	return nil
}

// parseCertFromConfig extracts and parses the leaf certificate from a tls.Config
// for inspection in tests.
func parseCertFromConfig(cfg *tls.Config) (*x509.Certificate, error) {
	if len(cfg.Certificates) == 0 {
		return nil, fmt.Errorf("no certificates in config")
	}
	return x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
}
