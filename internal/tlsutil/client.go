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

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ClientTLSFromDeviceTLS builds the *tls.Config used by the shared
// device-facing config, telemetry, gNOI, and NX-API clients from a
// CiscoDevice spec.tls block:
//   - MinVersion is always TLS 1.2.
//   - InsecureSkipVerify is copied verbatim (operator-controlled).
//   - CAFile, when set, is loaded into RootCAs so devices with private-CA
//     certificates get verified TLS instead of forcing skip-verify.
//   - CertFile/KeyFile must either both be set or both be empty. When set,
//     they are loaded as the client pair (device-side mTLS).
//
// A nil block returns the TLS 1.2 default config. Errors are returned for
// unreadable or unparseable files; callers should fail fast rather than
// silently downgrade to an unverified connection.
func ClientTLSFromDeviceTLS(t *ciskov1.TLSConfig) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if t == nil {
		return cfg, nil
	}

	cfg.InsecureSkipVerify = t.InsecureSkipVerify

	hasCertFile := t.CertFile != ""
	hasKeyFile := t.KeyFile != ""
	if hasCertFile != hasKeyFile {
		return nil, fmt.Errorf("client certFile and keyFile must be configured together")
	}

	if hasCertFile {
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	if t.CAFile != "" {
		caCert, err := os.ReadFile(t.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %q", t.CAFile)
		}
		cfg.RootCAs = caCertPool
	}

	return cfg, nil
}
