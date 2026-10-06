// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package tlsutil

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// ValidatePublicCABundle rejects mixed certificate/private-key bundles before
// projecting a CA into a different worker plane. It does not replace the TLS
// handshake's certificate validity, chain or server-name checks.
func ValidatePublicCABundle(material []byte) error {
	remaining := bytes.TrimSpace(material)
	if len(remaining) == 0 {
		return fmt.Errorf("public CA bundle is empty")
	}
	for len(remaining) > 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return fmt.Errorf("public CA bundle may contain only CERTIFICATE blocks")
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return fmt.Errorf("public CA bundle contains invalid PEM")
		}
		// pem.Decode may skip a malformed opening block to find a later valid
		// one. Reject that skipped content rather than projecting it verbatim.
		if bytes.Count(remaining[:len(remaining)-len(rest)], []byte("-----BEGIN ")) != 1 {
			return fmt.Errorf("public CA bundle contains skipped PEM content")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA {
			return fmt.Errorf("public CA bundle contains an invalid or non-CA certificate")
		}
		remaining = bytes.TrimSpace(rest)
	}
	return nil
}
