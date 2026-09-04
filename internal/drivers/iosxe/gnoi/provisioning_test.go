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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	provisioningTestServerName = "switch.example.test"
	provisioningTestIPAddress  = "192.0.2.10"
)

var (
	_ fmt.Formatter = ProvisioningBundle{}
	_ fmt.Formatter = (*ProvisioningBundle)(nil)
)

type certificateSignerFunc func(context.Context, []byte) ([]byte, error)

func (f certificateSignerFunc) SignCSR(ctx context.Context, csr []byte) ([]byte, error) {
	return f(ctx, csr)
}

func TestProvisioningBundlePublicMaterialSHA256UsesExactInputBytes(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	leafPEM := append(bytes.Clone(pki.leafPEM), '\n')
	caPEM := append(bytes.Clone(pki.caBundlePEM), '\n')
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, leafPEM, caPEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}

	digest := sha256.New()
	_, _ = digest.Write(leafPEM)
	_, _ = digest.Write(caPEM)
	want := fmt.Sprintf("%x", digest.Sum(nil))
	if got := bundle.PublicMaterialSHA256(); got != want {
		t.Fatalf("PublicMaterialSHA256()=%q, want %q", got, want)
	}
}

type provisioningTestPKI struct {
	leafPEM         []byte
	intermediatePEM []byte
	rootPEM         []byte
	caBundlePEM     []byte
	caKeyPEM        []byte
	leaf            *x509.Certificate
	leafKey         *rsa.PrivateKey
	intermediate    *x509.Certificate
	intermediateKey *rsa.PrivateKey
	root            *x509.Certificate
	rootKey         *rsa.PrivateKey
}

func TestProvisioning(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	unrelated := newProvisioningTestPKI(t, "unrelated.example.test")
	bundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
	targetKey := mustRSAKey(t, iosXEProvisioningRSAKeyBits)

	t.Run("IOS XE CSR parameters", func(t *testing.T) {
		t.Run("complete profile", func(t *testing.T) {
			params, err := iosXECSRParams(provisioningTestServerName, pki.leaf)
			if err != nil {
				t.Fatalf("iosXECSRParams: %v", err)
			}
			if params.GetType() != certpb.CertificateType_CT_X509 ||
				params.GetKeyType() != certpb.KeyType_KT_RSA ||
				params.GetMinKeySize() != iosXEProvisioningRSAKeyBits {
				t.Fatalf("key profile=%s/%s/%d, want CT_X509/KT_RSA/%d", params.GetType(), params.GetKeyType(), params.GetMinKeySize(), iosXEProvisioningRSAKeyBits)
			}
			if params.GetCommonName() != provisioningTestServerName ||
				params.GetCountry() != "US" ||
				params.GetState() != "California" ||
				params.GetOrganization() != "Cisco" ||
				params.GetOrganizationalUnit() != "gNOI" ||
				params.GetIpAddress() != provisioningTestIPAddress {
				t.Fatalf("CSR params=%+v, want complete template identity", params)
			}
			if params.GetCity() != "" || params.GetEmailId() != "" {
				t.Fatalf("CSR params=%+v, city and email must remain unset", params)
			}
		})

		t.Run("required fields", func(t *testing.T) {
			tests := []struct {
				name    string
				mutate  func(*x509.Certificate)
				wantErr string
			}{
				{name: "common name", mutate: func(cert *x509.Certificate) { cert.Subject.CommonName = "" }, wantErr: "common name"},
				{name: "country", mutate: func(cert *x509.Certificate) { cert.Subject.Country = nil }, wantErr: "country"},
				{name: "state", mutate: func(cert *x509.Certificate) { cert.Subject.Province = nil }, wantErr: "state/province"},
				{name: "organization", mutate: func(cert *x509.Certificate) { cert.Subject.Organization = nil }, wantErr: "organization"},
				{name: "organizational unit", mutate: func(cert *x509.Certificate) { cert.Subject.OrganizationalUnit = nil }, wantErr: "organizational unit"},
				{name: "IP address", mutate: func(cert *x509.Certificate) { cert.IPAddresses = nil }, wantErr: "IP address"},
				{name: "country length", mutate: func(cert *x509.Certificate) { cert.Subject.Country = []string{"USA"} }, wantErr: "two-letter"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					leaf := *pki.leaf
					tt.mutate(&leaf)
					_, err := iosXECSRParams(provisioningTestServerName, &leaf)
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("error=%v, want substring %q", err, tt.wantErr)
					}
				})
			}
		})
	})

	t.Run("bundle validation", func(t *testing.T) {
		if bundle.CertificateID() != "cvk-gnoi" {
			t.Fatalf("CertificateID=%q, want cvk-gnoi", bundle.CertificateID())
		}

		expired := reissueProvisioningCertificate(t, pki.leaf, pki.intermediate, &pki.leafKey.PublicKey, pki.intermediateKey, func(cert *x509.Certificate) {
			cert.NotBefore = time.Now().Add(-2 * time.Hour)
			cert.NotAfter = time.Now().Add(-time.Hour)
		})
		tests := []struct {
			name          string
			certificateID string
			serverName    string
			leafPEM       []byte
			caBundlePEM   []byte
			wantErr       string
		}{
			{name: "invalid certificate ID", certificateID: "bad id", serverName: provisioningTestServerName, leafPEM: pki.leafPEM, caBundlePEM: pki.caBundlePEM, wantErr: "certificate ID"},
			{name: "missing server name", certificateID: "cvk-gnoi", leafPEM: pki.leafPEM, caBundlePEM: pki.caBundlePEM, wantErr: "server name is required"},
			{name: "malformed leaf PEM", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: []byte("not PEM"), caBundlePEM: pki.caBundlePEM, wantErr: "leaf certificate"},
			{name: "CA used as leaf", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: pki.intermediatePEM, caBundlePEM: pki.caBundlePEM, wantErr: "must not be a CA"},
			{name: "expired leaf", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: certificatePEM(expired), caBundlePEM: pki.caBundlePEM, wantErr: "expired"},
			{name: "hostname mismatch", certificateID: "cvk-gnoi", serverName: "other.example.test", leafPEM: pki.leafPEM, caBundlePEM: pki.caBundlePEM, wantErr: "verify leaf certificate"},
			{name: "malformed CA PEM", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: pki.leafPEM, caBundlePEM: []byte("not PEM"), wantErr: "CA bundle"},
			{name: "non-CA in bundle", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: pki.leafPEM, caBundlePEM: pki.leafPEM, wantErr: "is not a CA"},
			{name: "missing root", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: pki.leafPEM, caBundlePEM: pki.intermediatePEM, wantErr: "self-signed root"},
			{name: "unrelated CA", certificateID: "cvk-gnoi", serverName: provisioningTestServerName, leafPEM: pki.leafPEM, caBundlePEM: unrelated.caBundlePEM, wantErr: "verify leaf certificate"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := NewProvisioningBundle(tt.certificateID, tt.serverName, tt.leafPEM, tt.caBundlePEM)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewProvisioningBundle error=%v, want substring %q", err, tt.wantErr)
				}
			})
		}

		directRootLeaf := reissueProvisioningCertificate(t, pki.leaf, pki.root, &pki.leafKey.PublicKey, pki.rootKey, func(*x509.Certificate) {})
		if _, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, certificatePEM(directRootLeaf), pki.rootPEM); err == nil || !strings.Contains(err.Error(), "dedicated intermediate CA") {
			t.Fatalf("direct-root bundle error=%v, want dedicated-intermediate rejection", err)
		}

		t.Run("formatting redacts material", func(t *testing.T) {
			const want = "ProvisioningBundle{REDACTED}"
			for _, operand := range []any{*bundle, bundle} {
				for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
					if got := fmt.Sprintf(format, operand); got != want {
						t.Errorf("Sprintf(%q, %T)=%q, want %q", format, operand, got, want)
					}
				}
			}
		})
	})

	t.Run("local signer validation", func(t *testing.T) {
		weakKey := mustRSAKey(t, 1024)
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("ecdsa.GenerateKey: %v", err)
		}
		ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
		if err != nil {
			t.Fatalf("x509.MarshalPKCS8PrivateKey: %v", err)
		}

		tests := []struct {
			name    string
			bundle  *ProvisioningBundle
			keyPEM  []byte
			wantErr string
		}{
			{name: "nil bundle", keyPEM: pki.caKeyPEM, wantErr: "nil bundle"},
			{name: "empty key", bundle: bundle, keyPEM: []byte{}, wantErr: "private key is not PEM encoded"},
			{name: "malformed key", bundle: bundle, keyPEM: []byte("not PEM"), wantErr: "private key is not PEM encoded"},
			{name: "encrypted key", bundle: bundle, keyPEM: pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}}), wantErr: "encrypted private keys"},
			{name: "non-RSA key", bundle: bundle, keyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}), wantErr: "must contain an RSA key"},
			{name: "weak key", bundle: bundle, keyPEM: rsaPrivateKeyPEM(t, weakKey), wantErr: "at least 2048"},
			{name: "wrong issuer key", bundle: bundle, keyPEM: unrelated.caKeyPEM, wantErr: "must match tls.crt's verified intermediate issuer"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				signer, err := NewLocalCertificateSigner(tt.bundle, tt.keyPEM)
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewLocalCertificateSigner signer=%v error=%v, want substring %q", signer, err, tt.wantErr)
				}
			})
		}

		signer, err := NewLocalCertificateSigner(bundle, pki.caKeyPEM)
		if err != nil {
			t.Fatalf("NewLocalCertificateSigner: %v", err)
		}
		const want = "CertificateSigner{REDACTED}"
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if got := fmt.Sprintf(format, signer); got != want {
				t.Errorf("Sprintf(%q, %T)=%q, want %q", format, signer, got, want)
			}
		}
	})

	t.Run("CA replacement and client trust", func(t *testing.T) {
		completeBundle := bytes.Join([][]byte{
			unrelated.intermediatePEM,
			unrelated.rootPEM,
			pki.intermediatePEM,
			pki.rootPEM,
		}, nil)
		complete := mustProvisioningBundle(t, pki, completeBundle)
		wantCAs := [][]byte{unrelated.intermediatePEM, unrelated.rootPEM, pki.intermediatePEM, pki.rootPEM}
		if len(complete.caPEM) != len(wantCAs) {
			t.Fatalf("replacement bundle has %d certificates, want %d", len(complete.caPEM), len(wantCAs))
		}
		for i := range wantCAs {
			if !bytes.Equal(complete.caPEM[i], wantCAs[i]) {
				t.Fatalf("replacement CA %d changed or moved", i)
			}
		}
	})

	t.Run("installed certificate reconciliation", func(t *testing.T) {
		issuedPEM := mustSignCSR(t, bundle, pki.intermediateKey, targetKey, &x509.CertificateRequest{})

		t.Run("missing", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.getResp = &certpb.GetCertificatesResponse{}
			installed, err := ts.client(t).ProvisioningCertificateInstalled(context.Background(), bundle)
			if err != nil || installed {
				t.Fatalf("installed=%v err=%v, want false, nil", installed, err)
			}
		})

		for _, certificateType := range []certpb.CertificateType{certpb.CertificateType_CT_X509, certpb.CertificateType_CT_UNKNOWN} {
			t.Run("matching "+certificateType.String(), func(t *testing.T) {
				ts := newTestServer(t)
				ts.Cert.getResp = certificateInventory("cvk-gnoi", append([]byte("\n"), issuedPEM...))
				ts.Cert.getResp.CertificateInfo[0].Certificate.Type = certificateType
				installed, err := ts.client(t).ProvisioningCertificateInstalled(context.Background(), bundle)
				if err != nil || !installed {
					t.Fatalf("installed=%v err=%v, want true, nil", installed, err)
				}
			})
		}

		t.Run("occupied ID fails closed", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.getResp = certificateInventory("cvk-gnoi", pki.leafPEM)
			installed, err := ts.client(t).ProvisioningCertificateInstalled(context.Background(), bundle)
			if installed || err == nil || !strings.Contains(err.Error(), `certificate ID "cvk-gnoi" already contains a different certificate`) {
				t.Fatalf("installed=%v error=%T %v, want cvk-gnoi conflict", installed, err, err)
			}
		})

		t.Run("unsupported certificate type fails closed", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.getResp = certificateInventory("cvk-gnoi", issuedPEM)
			ts.Cert.getResp.CertificateInfo[0].Certificate.Type = certpb.CertificateType(99)
			installed, err := ts.client(t).ProvisioningCertificateInstalled(context.Background(), bundle)
			if installed || err == nil || !strings.Contains(err.Error(), `certificate ID "cvk-gnoi" already contains a different certificate`) {
				t.Fatalf("installed=%v error=%T %v, want certificate ID conflict", installed, err, err)
			}
		})
	})

	t.Run("CSR signing uses local certificate policy", func(t *testing.T) {
		attackerURI, err := url.Parse("spiffe://attacker.example.test/device")
		if err != nil {
			t.Fatalf("url.Parse: %v", err)
		}
		csr := &x509.CertificateRequest{
			Subject:        pkix.Name{CommonName: "attacker.example.test", Organization: []string{"Attacker"}},
			DNSNames:       []string{"attacker.example.test"},
			IPAddresses:    []net.IP{net.ParseIP("203.0.113.20")},
			EmailAddresses: []string{"attacker@example.test"},
			URIs:           []*url.URL{attackerURI},
		}
		issuedPEM := mustSignCSR(t, bundle, pki.intermediateKey, targetKey, csr)
		issued, err := parseSingleCertificatePEM(issuedPEM, "issued certificate")
		if err != nil {
			t.Fatalf("parse issued certificate: %v", err)
		}
		issuedKey, ok := issued.PublicKey.(*rsa.PublicKey)
		if !ok || !rsaPublicKeysEqual(issuedKey, &targetKey.PublicKey) {
			t.Fatal("issued certificate does not contain the target-generated key")
		}
		if err := issued.CheckSignatureFrom(pki.intermediate); err != nil {
			t.Fatalf("issued certificate signature: %v", err)
		}
		if !bytes.Equal(issued.RawSubject, bundle.expectedSubjectDER) ||
			!slices.Equal(issued.DNSNames, bundle.leafTemplate.DNSNames) ||
			!equalIPAddresses(issued.IPAddresses, bundle.leafTemplate.IPAddresses) {
			t.Fatalf("issued identity escaped local profile: subject=%+v DNS=%v IP=%v", issued.Subject, issued.DNSNames, issued.IPAddresses)
		}
		if len(issued.EmailAddresses) != 0 || len(issued.URIs) != 0 {
			t.Fatalf("issued certificate copied unsupported SANs: email=%v URIs=%v", issued.EmailAddresses, issued.URIs)
		}
		if issued.KeyUsage != x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment ||
			!slices.Equal(issued.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
			t.Fatalf("issued usages=%v/%v, want digital-signature+key-encipherment/serverAuth", issued.KeyUsage, issued.ExtKeyUsage)
		}
		if !issued.NotBefore.Equal(bundle.leafTemplate.NotBefore) || !issued.NotAfter.Equal(bundle.leafTemplate.NotAfter) {
			t.Fatal("issued validity did not come from the local tls.crt profile")
		}
	})

	t.Run("invalid target CSR", func(t *testing.T) {
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("ecdsa.GenerateKey: %v", err)
		}
		valid := certificateRequestPEM(t, targetKey, &x509.CertificateRequest{})
		block, _ := pem.Decode(valid)
		badSignature := bytes.Clone(block.Bytes)
		badSignature[len(badSignature)-1] ^= 0xff

		tests := []struct {
			name string
			csr  []byte
		}{
			{name: "not PEM", csr: []byte("not PEM")},
			{name: "wrong PEM type", csr: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})},
			{name: "trailing data", csr: append(bytes.Clone(valid), []byte("trailing")...)},
			{name: "invalid signature", csr: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: badSignature})},
			{name: "ECDSA key", csr: certificateRequestPEM(t, ecKey, &x509.CertificateRequest{})},
			{name: "weak RSA key", csr: certificateRequestPEM(t, mustRSAKey(t, 1024), &x509.CertificateRequest{})},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if _, err := bundle.signCSR(tt.csr, pki.intermediateKey); err == nil {
					t.Fatal("signCSR accepted an invalid target CSR")
				}
			})
		}
	})

	t.Run("Install CSR protocol", func(t *testing.T) {
		completeBundle := bytes.Join([][]byte{
			unrelated.intermediatePEM,
			unrelated.rootPEM,
			pki.intermediatePEM,
			pki.rootPEM,
		}, nil)
		validCSR := certificateRequestPEM(t, targetKey, &x509.CertificateRequest{})

		t.Run("GenerateCSR then Load and return after acknowledgement", func(t *testing.T) {
			installBundle := mustProvisioningBundle(t, pki, completeBundle)
			signer := mustLocalCertificateSigner(t, installBundle, pki.caKeyPEM)
			ts := newTestServer(t)
			ts.Cert.installCSR = validCSR
			ts.Cert.installResponseSent = make(chan struct{})
			ts.Cert.installRelease = make(chan struct{})
			defer close(ts.Cert.installRelease)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- ts.client(t).InstallProvisioningCertificate(ctx, installBundle, signer)
			}()

			select {
			case <-ts.Cert.installResponseSent:
			case <-ctx.Done():
				t.Fatalf("server did not send LoadCertificateResponse: %v", ctx.Err())
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatalf("InstallProvisioningCertificate: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("InstallProvisioningCertificate waited for final stream EOF")
			}

			if len(ts.Cert.installRequests) != 2 {
				t.Fatalf("Install sent %d requests, want GenerateCSR then LoadCertificate", len(ts.Cert.installRequests))
			}
			generate := ts.Cert.installRequests[0].GetGenerateCsr()
			if generate == nil || generate.GetCertificateId() != "cvk-gnoi" || generate.GetCsrParams().GetType() != certpb.CertificateType_CT_X509 {
				t.Fatalf("first request=%+v, want CT_X509 GenerateCSR for cvk-gnoi", ts.Cert.installRequests[0])
			}
			load := ts.Cert.installRequests[1].GetLoadCertificate()
			if load == nil || load.GetCertificateId() != "" || load.GetKeyPair() != nil {
				t.Fatalf("second request=%+v, want CSR Load without certificate ID or key pair", ts.Cert.installRequests[1])
			}
			if load.GetCertificate().GetType() != certpb.CertificateType_CT_X509 {
				t.Fatalf("loaded certificate type=%s, want CT_X509", load.GetCertificate().GetType())
			}
			wantCAs := [][]byte{unrelated.intermediatePEM, unrelated.rootPEM, pki.intermediatePEM, pki.rootPEM}
			if len(load.GetCaCertificates()) != len(wantCAs) {
				t.Fatalf("Install sent %d CA certificates, want %d", len(load.GetCaCertificates()), len(wantCAs))
			}
			for i, want := range wantCAs {
				ca := load.GetCaCertificates()[i]
				if ca.GetType() != certpb.CertificateType_CT_X509 || !bytes.Equal(ca.GetCertificate(), want) {
					t.Fatalf("Install CA certificate %d changed or moved", i)
				}
			}
			issued, err := parseSingleCertificatePEM(load.GetCertificate().GetCertificate(), "issued certificate")
			if err != nil {
				t.Fatalf("parse loaded certificate: %v", err)
			}
			issuedKey, ok := issued.PublicKey.(*rsa.PublicKey)
			if !ok || !rsaPublicKeysEqual(issuedKey, &targetKey.PublicKey) {
				t.Fatal("loaded certificate does not use the target CSR key")
			}
			if ts.Cert.canGenRequest != nil {
				t.Fatal("Install unexpectedly called CanGenerateCSR")
			}
		})

		t.Run("external signer implementation", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installCSR = validCSR
			signer := certificateSignerFunc(func(ctx context.Context, csr []byte) ([]byte, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if !bytes.Equal(csr, validCSR) {
					t.Fatal("signer did not receive the target-generated CSR")
				}
				return bundle.signCSR(csr, pki.intermediateKey)
			})
			if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle, signer); err != nil {
				t.Fatalf("InstallProvisioningCertificate with external signer: %v", err)
			}
			if len(ts.Cert.installRequests) != 2 || ts.Cert.installRequests[1].GetLoadCertificate() == nil {
				t.Fatalf("Install requests=%d, want GenerateCSR then Load", len(ts.Cert.installRequests))
			}
		})

		t.Run("invalid target CSR is rejected before signer", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installCSR = []byte("not PEM")
			called := false
			signer := certificateSignerFunc(func(context.Context, []byte) ([]byte, error) {
				called = true
				return nil, nil
			})
			err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle, signer)
			if err == nil || !strings.Contains(err.Error(), "validate CSR") {
				t.Fatalf("error=%v, want local CSR validation failure", err)
			}
			if called {
				t.Fatal("untrusted target CSR was sent to the certificate signer")
			}
			if len(ts.Cert.installRequests) != 1 {
				t.Fatalf("Install sent %d requests after invalid CSR, want 1", len(ts.Cert.installRequests))
			}
		})

		t.Run("signer result is validated before Load", func(t *testing.T) {
			wrongKeyCSR := certificateRequestPEM(t, mustRSAKey(t, iosXEProvisioningRSAKeyBits), &x509.CertificateRequest{})
			wrongKeyCertificate, err := bundle.signCSR(wrongKeyCSR, pki.intermediateKey)
			if err != nil {
				t.Fatalf("sign wrong-key certificate: %v", err)
			}
			validCertificate, err := bundle.signCSR(validCSR, pki.intermediateKey)
			if err != nil {
				t.Fatalf("sign valid certificate: %v", err)
			}
			validParsed, err := parseSingleCertificatePEM(validCertificate, "valid certificate")
			if err != nil {
				t.Fatalf("parse valid certificate: %v", err)
			}
			unsupportedCritical := reissueProvisioningCertificate(
				t,
				validParsed,
				pki.intermediate,
				&targetKey.PublicKey,
				pki.intermediateKey,
				func(certificate *x509.Certificate) {
					certificate.ExtraExtensions = append(certificate.ExtraExtensions, pkix.Extension{
						Id:       asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 9, 999, 1},
						Critical: true,
						Value:    []byte{0x05, 0x00},
					})
				},
			)

			tests := []struct {
				name    string
				result  []byte
				wantErr string
			}{
				{name: "malformed PEM", result: []byte("not PEM"), wantErr: "signer certificate"},
				{name: "multiple certificates", result: append(bytes.Clone(validCertificate), validCertificate...), wantErr: "exactly one PEM certificate"},
				{name: "different CSR key", result: wrongKeyCertificate, wantErr: "does not match the target-generated CSR"},
				{name: "unsupported critical extension", result: certificatePEM(unsupportedCritical), wantErr: "unhandled critical extension"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ts := newTestServer(t)
					ts.Cert.installCSR = validCSR
					signer := certificateSignerFunc(func(context.Context, []byte) ([]byte, error) {
						return bytes.Clone(tt.result), nil
					})
					err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle, signer)
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("error=%v, want signer-result rejection containing %q", err, tt.wantErr)
					}
					if len(ts.Cert.installRequests) != 1 {
						t.Fatalf("Install sent %d requests after invalid signer result, want 1", len(ts.Cert.installRequests))
					}
				})
			}
		})

		t.Run("omitted certificate type is accepted", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installCSR = validCSR
			unknown := certpb.CertificateType_CT_UNKNOWN
			ts.Cert.installCSRType = &unknown
			installBundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
			if err := installWithLocalSigner(t, ts.client(t), context.Background(), installBundle, pki.caKeyPEM); err != nil {
				t.Fatalf("InstallProvisioningCertificate with CT_UNKNOWN: %v", err)
			}
		})

		t.Run("explicit unsupported certificate type is rejected", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installCSR = validCSR
			unsupported := certpb.CertificateType(99)
			ts.Cert.installCSRType = &unsupported
			installBundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
			err := installWithLocalSigner(t, ts.client(t), context.Background(), installBundle, pki.caKeyPEM)
			if err == nil || !strings.Contains(err.Error(), "want CT_X509") {
				t.Fatalf("error=%v, want certificate type rejection", err)
			}
			if len(ts.Cert.installRequests) != 1 {
				t.Fatalf("Install sent %d requests after rejected CSR response, want 1", len(ts.Cert.installRequests))
			}
		})

		t.Run("missing CSR is rejected before Load", func(t *testing.T) {
			ts := newTestServer(t)
			installBundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
			err := installWithLocalSigner(t, ts.client(t), context.Background(), installBundle, pki.caKeyPEM)
			if err == nil || !strings.Contains(err.Error(), "expected GenerateCSRResponse with CSR") {
				t.Fatalf("error=%v, want missing CSR rejection", err)
			}
			if len(ts.Cert.installRequests) != 1 {
				t.Fatalf("Install sent %d requests after missing CSR, want 1", len(ts.Cert.installRequests))
			}
		})

		t.Run("unexpected Load response is indeterminate", func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installCSR = validCSR
			ts.Cert.installResponse = &certpb.InstallCertificateResponse{
				InstallResponse: &certpb.InstallCertificateResponse_GeneratedCsr{
					GeneratedCsr: &certpb.GenerateCSRResponse{},
				},
			}
			installBundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
			err := installWithLocalSigner(t, ts.client(t), context.Background(), installBundle, pki.caKeyPEM)
			if !IsCertificateInstallIndeterminate(err) {
				t.Fatalf("error=%T %v, want indeterminate outcome", err, err)
			}
		})
	})

	t.Run("indeterminate Install errors", func(t *testing.T) {
		tests := []struct {
			name string
			err  error
			want bool
		}{
			{name: "EOF", err: io.EOF, want: true},
			{name: "context deadline", err: context.DeadlineExceeded, want: true},
			{name: "target restart", err: status.Error(codes.Unavailable, "server restarting"), want: true},
			{name: "create-only ID exists", err: status.Error(codes.AlreadyExists, "ID exists"), want: true},
			{name: "request rejected", err: status.Error(codes.InvalidArgument, "bad certificate"), want: false},
			{name: "permission denied", err: status.Error(codes.PermissionDenied, "denied"), want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				wrapped := wrapCertificateInstallError("cvk-gnoi", "test", tt.err)
				if got := IsCertificateInstallIndeterminate(wrapped); got != tt.want {
					t.Fatalf("IsCertificateInstallIndeterminate=%v error=%T %v, want %v", got, wrapped, wrapped, tt.want)
				}
				if !errors.Is(wrapped, tt.err) {
					t.Fatal("wrapped error did not preserve its cause")
				}
			})
		}
	})
}

func TestProvisioningClientTLSBootstrapTransition(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bootstrapPKI := newProvisioningTestPKI(t, "bootstrap.invalid")
	bundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if err := bundle.ConfigureClientTLS(tlsCfg, bootstrapPKI.leafPEM); err != nil {
		t.Fatalf("ConfigureClientTLS: %v", err)
	}
	if tlsCfg.ServerName != provisioningTestServerName || !tlsCfg.InsecureSkipVerify || tlsCfg.VerifyConnection == nil { //nolint:gosec // the callback performs verification
		t.Fatalf("configured TLS=%+v, want explicit server name and custom verifier", tlsCfg)
	}

	verify := func(certificates ...*x509.Certificate) error {
		t.Helper()
		return tlsCfg.VerifyConnection(tls.ConnectionState{PeerCertificates: certificates})
	}
	if err := verify(bootstrapPKI.leaf); err != nil {
		t.Fatalf("exact bootstrap pin: %v", err)
	}

	// A reissued certificate with the same public key is not the exact pin.
	reissuedBootstrap := reissueProvisioningCertificate(
		t, bootstrapPKI.leaf, bootstrapPKI.intermediate,
		&bootstrapPKI.leafKey.PublicKey, bootstrapPKI.intermediateKey,
		func(*x509.Certificate) {},
	)
	if err := verify(reissuedBootstrap); err == nil {
		t.Fatal("bootstrap verifier accepted a different certificate with the pinned public key")
	}

	// The intermediate is supplied as an intermediate, not promoted to a root;
	// this permits IOS XE to serve only its new leaf without weakening the chain.
	if _, err := pki.leaf.Verify(x509.VerifyOptions{
		DNSName:   provisioningTestServerName,
		Roots:     tlsCfg.RootCAs,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Fatal("provisioning intermediate was incorrectly promoted to a trust root")
	}
	if err := verify(pki.leaf); err != nil {
		t.Fatalf("CA-verified leaf-only connection: %v", err)
	}
	if err := verify(bootstrapPKI.leaf); err == nil {
		t.Fatal("bootstrap pin remained enabled after a CA-verified connection")
	}
}

func TestProvisioningClientTLSRejectsInvalidBootstrap(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
	expired := reissueProvisioningCertificate(t, pki.leaf, pki.intermediate, &pki.leafKey.PublicKey, pki.intermediateKey, func(cert *x509.Certificate) {
		cert.NotBefore = time.Now().Add(-2 * time.Hour)
		cert.NotAfter = time.Now().Add(-time.Hour)
	})
	for _, tt := range []struct {
		name string
		pem  []byte
	}{
		{name: "malformed", pem: []byte("not PEM")},
		{name: "multiple certificates", pem: append(bytes.Clone(pki.leafPEM), pki.leafPEM...)},
		{name: "expired", pem: certificatePEM(expired)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := bundle.ConfigureClientTLS(&tls.Config{}, tt.pem); err == nil {
				t.Fatal("ConfigureClientTLS accepted an invalid bootstrap certificate")
			}
		})
	}
}

func TestProvisioningSignerIsOneShotUnderConcurrency(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle := mustProvisioningBundle(t, pki, pki.caBundlePEM)
	signer := mustLocalCertificateSigner(t, bundle, pki.caKeyPEM)
	csr := certificateRequestPEM(t, mustRSAKey(t, iosXEProvisioningRSAKeyBits), &x509.CertificateRequest{})

	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := signer.SignCSR(context.Background(), csr)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	signed := 0
	for err := range results {
		if err == nil {
			signed++
			continue
		}
		if !strings.Contains(err.Error(), "only once") {
			t.Errorf("SignCSR: %v", err)
		}
	}
	if signed != 1 {
		t.Fatalf("successful signatures=%d, want exactly 1", signed)
	}
}

func TestInstallWithoutSignerFailsBeforeRequest(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	err = ts.client(t).InstallProvisioningCertificate(context.Background(), bundle, nil)
	if err == nil || !strings.Contains(err.Error(), "certificate signer is required") {
		t.Fatalf("InstallProvisioningCertificate error=%v, want missing signer", err)
	}
	if len(ts.Cert.installRequests) != 0 {
		t.Fatalf("Install sent %d requests without a signing key, want 0", len(ts.Cert.installRequests))
	}
}

func TestVerifyClassifiesExactIOSXEDeviceNotProvisionedError(t *testing.T) {
	for _, message := range []string{iosXEDeviceNotProvisionedMessage, iosXEDeviceNotProvisionedMessage + "."} {
		t.Run(message, func(t *testing.T) {
			ts := newTestServer(t)
			ts.OS.verifyErr = status.Error(codes.FailedPrecondition, message)
			_, err := ts.client(t).Verify(context.Background())
			if !IsDeviceNotProvisioned(err) {
				t.Fatalf("Verify error=%T %v, want device-not-provisioned classification", err, err)
			}
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("status.Code=%v, want FailedPrecondition", status.Code(err))
			}
		})
	}

	for _, err := range []error{
		status.Error(codes.FailedPrecondition, "Device certificate has not been provisioned"),
		status.Error(codes.Unavailable, iosXEDeviceNotProvisionedMessage),
	} {
		if IsDeviceNotProvisioned(err) {
			t.Fatalf("unrelated error %v was classified as device-not-provisioned", err)
		}
	}
}

func newProvisioningTestPKI(t *testing.T, serverName string) *provisioningTestPKI {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)

	rootKey := mustRSAKey(t, iosXEProvisioningRSAKeyBits)
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CVK test root " + serverName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          []byte{1, 2, 3},
	}
	root := mustIssueCertificate(t, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)

	intermediateKey := mustRSAKey(t, iosXEProvisioningRSAKeyBits)
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "CVK test intermediate " + serverName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(12 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          []byte{4, 5, 6},
	}
	intermediate := mustIssueCertificate(t, intermediateTemplate, root, &intermediateKey.PublicKey, rootKey)

	leafKey := mustRSAKey(t, iosXEProvisioningRSAKeyBits)
	leafURI, err := url.Parse("spiffe://" + serverName + "/gnoi")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject: pkix.Name{
			CommonName:         serverName,
			Country:            []string{"US"},
			Province:           []string{"California"},
			Locality:           []string{"San Jose"},
			Organization:       []string{"Cisco"},
			OrganizationalUnit: []string{"gNOI"},
		},
		DNSNames:       []string{serverName},
		IPAddresses:    []net.IP{net.ParseIP(provisioningTestIPAddress)},
		EmailAddresses: []string{"gnoi@example.test"},
		URIs:           []*url.URL{leafURI},
		NotBefore:      now.Add(-time.Hour),
		NotAfter:       now.Add(6 * time.Hour),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf := mustIssueCertificate(t, leafTemplate, intermediate, &leafKey.PublicKey, intermediateKey)
	intermediatePEM := certificatePEM(intermediate)
	rootPEM := certificatePEM(root)
	return &provisioningTestPKI{
		leafPEM:         certificatePEM(leaf),
		intermediatePEM: intermediatePEM,
		rootPEM:         rootPEM,
		caBundlePEM:     append(bytes.Clone(intermediatePEM), rootPEM...),
		caKeyPEM:        rsaPrivateKeyPEM(t, intermediateKey),
		leaf:            leaf,
		leafKey:         leafKey,
		intermediate:    intermediate,
		intermediateKey: intermediateKey,
		root:            root,
		rootKey:         rootKey,
	}
}

func mustProvisioningBundle(t *testing.T, pki *provisioningTestPKI, caBundle []byte) *ProvisioningBundle {
	t.Helper()
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, caBundle)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	return bundle
}

func mustLocalCertificateSigner(t *testing.T, bundle *ProvisioningBundle, caKeyPEM []byte) CertificateSigner {
	t.Helper()
	signer, err := NewLocalCertificateSigner(bundle, caKeyPEM)
	if err != nil {
		t.Fatalf("NewLocalCertificateSigner: %v", err)
	}
	return signer
}

func installWithLocalSigner(t *testing.T, client *Client, ctx context.Context, bundle *ProvisioningBundle, caKeyPEM []byte) error {
	t.Helper()
	return client.InstallProvisioningCertificate(ctx, bundle, mustLocalCertificateSigner(t, bundle, caKeyPEM))
}

func mustSignCSR(t *testing.T, bundle *ProvisioningBundle, signingKey *rsa.PrivateKey, key any, template *x509.CertificateRequest) []byte {
	t.Helper()
	issued, err := bundle.signCSR(certificateRequestPEM(t, key, template), signingKey)
	if err != nil {
		t.Fatalf("signCSR: %v", err)
	}
	return issued
}

func certificateRequestPEM(t *testing.T, key any, template *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func certificateInventory(certificateID string, certificate []byte) *certpb.GetCertificatesResponse {
	return &certpb.GetCertificatesResponse{CertificateInfo: []*certpb.CertificateInfo{{
		CertificateId: certificateID,
		Certificate: &certpb.Certificate{
			Type:        certpb.CertificateType_CT_X509,
			Certificate: certificate,
		},
	}}}
}

func mustRSAKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(%d): %v", bits, err)
	}
	return key
}

func rsaPrivateKeyPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func mustIssueCertificate(t *testing.T, template, parent *x509.Certificate, publicKey, signer any) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, publicKey, signer)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return certificate
}

func reissueProvisioningCertificate(
	t *testing.T,
	base, issuer *x509.Certificate,
	publicKey, signer any,
	mutate func(*x509.Certificate),
) *x509.Certificate {
	t.Helper()
	template := *base
	template.Raw = nil
	template.RawTBSCertificate = nil
	template.RawSubjectPublicKeyInfo = nil
	template.RawSubject = nil
	template.RawIssuer = nil
	template.Signature = nil
	template.SignatureAlgorithm = x509.UnknownSignatureAlgorithm
	template.SubjectKeyId = nil
	template.AuthorityKeyId = bytes.Clone(issuer.SubjectKeyId)
	template.SerialNumber = new(big.Int).Add(base.SerialNumber, big.NewInt(1000))
	mutate(&template)
	return mustIssueCertificate(t, &template, issuer, publicKey, signer)
}

func certificatePEM(certificate *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
}
