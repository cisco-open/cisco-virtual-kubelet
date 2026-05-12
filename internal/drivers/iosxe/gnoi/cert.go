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

package gnoi

import (
	"context"
	"fmt"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
)

// CertificateInfo is the structured form of one gNOI certificate
// record on the device.
type CertificateInfo struct {
	CertificateID    string
	Type             string
	Certificate      []byte
	Endpoints        []string
	ModificationTime time.Time
}

// GetCertificates returns the certificates installed on the device.
// Read-only — safe to use as a Cert service capability probe.
func (c *Client) GetCertificates(ctx context.Context) ([]CertificateInfo, error) {
	if err := c.cap.ensureSupported(ServiceCert); err != nil {
		return nil, err
	}
	resp, err := c.cert.GetCertificates(c.authCtx(ctx), &certpb.GetCertificatesRequest{})
	c.cap.Observe(ServiceCert, err)
	if err != nil {
		return nil, fmt.Errorf("gnoi Cert.GetCertificates: %w", err)
	}
	out := make([]CertificateInfo, 0, len(resp.CertificateInfo))
	for _, ci := range resp.CertificateInfo {
		info := CertificateInfo{
			CertificateID: ci.CertificateId,
		}
		if ci.Certificate != nil {
			info.Type = ci.Certificate.Type.String()
			info.Certificate = ci.Certificate.Certificate
		}
		if ci.ModificationTime != 0 {
			info.ModificationTime = time.Unix(0, ci.ModificationTime)
		}
		for _, ep := range ci.Endpoints {
			info.Endpoints = append(info.Endpoints, ep.String())
		}
		out = append(out, info)
	}
	return out, nil
}

// CanGenerateCSROpts mirrors the CanGenerateCSR request shape.
type CanGenerateCSROpts struct {
	KeyType         string // RT_RSA, RT_X25519 (defaults to RT_RSA when empty)
	CertificateType string // CT_X509 (defaults to CT_X509)
	KeySize         uint32 // bits; 2048 default for RSA
}

// CanGenerateCSR asks the device whether it can produce a CSR with the
// requested parameters. Read-only — safe as a Cert capability probe.
func (c *Client) CanGenerateCSR(ctx context.Context, opts CanGenerateCSROpts) (bool, error) {
	if err := c.cap.ensureSupported(ServiceCert); err != nil {
		return false, err
	}
	req := &certpb.CanGenerateCSRRequest{
		KeyType:         keyTypeFromString(opts.KeyType),
		CertificateType: certTypeFromString(opts.CertificateType),
		KeySize:         opts.KeySize,
	}
	resp, err := c.cert.CanGenerateCSR(c.authCtx(ctx), req)
	c.cap.Observe(ServiceCert, err)
	if err != nil {
		return false, fmt.Errorf("gnoi Cert.CanGenerateCSR: %w", err)
	}
	return resp.CanGenerate, nil
}

func keyTypeFromString(s string) certpb.KeyType {
	switch s {
	case "RT_RSA", "RSA", "rsa", "":
		return certpb.KeyType_KT_RSA
	}
	return certpb.KeyType_KT_UNKNOWN
}

func certTypeFromString(s string) certpb.CertificateType {
	switch s {
	case "CT_X509", "X509", "x509", "":
		return certpb.CertificateType_CT_X509
	}
	return certpb.CertificateType_CT_UNKNOWN
}
