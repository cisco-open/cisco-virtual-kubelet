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
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const provisioningTestServerName = "switch.example.test"
const provisioningTestIPAddress = "192.0.2.10"

var (
	_ fmt.Formatter = ProvisioningBundle{}
	_ fmt.Formatter = (*ProvisioningBundle)(nil)
)

type provisioningTestPKI struct {
	leafPEM         []byte
	privateKeyPKCS1 []byte
	privateKeyPKCS8 []byte
	intermediatePEM []byte
	rootPEM         []byte
	caBundlePEM     []byte
	leaf            *x509.Certificate
	leafKey         *rsa.PrivateKey
	intermediate    *x509.Certificate
	intermediateKey *rsa.PrivateKey
	root            *x509.Certificate
	rootKey         *rsa.PrivateKey
}

func newProvisioningTestPKI(t *testing.T, serverName string) *provisioningTestPKI {
	t.Helper()
	now := time.Now()

	rootKey := mustRSAKey(t, 2048)
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CVK test root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	root := mustIssueCertificate(t, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)

	intermediateKey := mustRSAKey(t, 2048)
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "CVK test intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(12 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	intermediate := mustIssueCertificate(t, intermediateTemplate, root, &intermediateKey.PublicKey, rootKey)

	leafKey := mustRSAKey(t, 2048)
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
		NotBefore:      now.Add(-time.Hour),
		NotAfter:       now.Add(6 * time.Hour),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf := mustIssueCertificate(t, leafTemplate, intermediate, &leafKey.PublicKey, intermediateKey)

	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	intermediatePEM := certificatePEM(intermediate)
	rootPEM := certificatePEM(root)
	return &provisioningTestPKI{
		leafPEM:         certificatePEM(leaf),
		privateKeyPKCS1: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}),
		privateKeyPKCS8: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8DER}),
		intermediatePEM: intermediatePEM,
		rootPEM:         rootPEM,
		caBundlePEM:     append(bytes.Clone(intermediatePEM), rootPEM...),
		leaf:            leaf,
		leafKey:         leafKey,
		intermediate:    intermediate,
		intermediateKey: intermediateKey,
		root:            root,
		rootKey:         rootKey,
	}
}

func (p *provisioningTestPKI) intermediateKeyPEM(t *testing.T) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(p.intermediateKey)})
}

func newCertificateRequestPEM(t *testing.T, serverName string) []byte {
	t.Helper()
	return newCertificateRequestPEMForKey(t, serverName, mustRSAKey(t, 2048))
}

func newCertificateRequestPEMForKey(t *testing.T, serverName string, key any) []byte {
	t.Helper()
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:         serverName,
			Country:            []string{"US"},
			Province:           []string{"California"},
			Organization:       []string{"Cisco"},
			OrganizationalUnit: []string{"gNOI"},
		},
		IPAddresses: []net.IP{net.ParseIP(provisioningTestIPAddress)},
	}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
}

func TestIOSXECSRParamsUsesCompleteTemplateProfile(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	params, err := iosXECSRParams(provisioningTestServerName, pki.leaf)
	if err != nil {
		t.Fatalf("iosXECSRParams: %v", err)
	}
	if params.GetType() != certpb.CertificateType_CT_X509 || params.GetKeyType() != certpb.KeyType_KT_RSA || params.GetMinKeySize() != 2048 {
		t.Fatalf("key profile=%s/%s/%d, want CT_X509/KT_RSA/2048", params.GetType(), params.GetKeyType(), params.GetMinKeySize())
	}
	if params.GetCommonName() != provisioningTestServerName || params.GetCountry() != "US" || params.GetState() != "California" ||
		params.GetOrganization() != "Cisco" || params.GetOrganizationalUnit() != "gNOI" || params.GetIpAddress() != provisioningTestIPAddress {
		t.Fatalf("CSR params=%+v, want complete template identity", params)
	}
	if params.GetCity() != "" || params.GetEmailId() != "" {
		t.Fatalf("CSR params=%+v, city/email must remain unset to match IOS XE's reference client", params)
	}
}

func TestIOSXECSRParamsRejectsMissingRequiredCompatibilityFields(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	tests := []struct {
		name    string
		mutate  func(*x509.Certificate)
		wantErr string
	}{
		{name: "country", mutate: func(cert *x509.Certificate) { cert.Subject.Country = nil }, wantErr: "country"},
		{name: "state", mutate: func(cert *x509.Certificate) { cert.Subject.Province = nil }, wantErr: "state/province"},
		{name: "organization", mutate: func(cert *x509.Certificate) { cert.Subject.Organization = nil }, wantErr: "organization"},
		{name: "organizational unit", mutate: func(cert *x509.Certificate) { cert.Subject.OrganizationalUnit = nil }, wantErr: "organizational unit"},
		{name: "IP address", mutate: func(cert *x509.Certificate) { cert.IPAddresses = nil }, wantErr: "IP address"},
		{name: "country code length", mutate: func(cert *x509.Certificate) { cert.Subject.Country = []string{"USA"} }, wantErr: "two-letter"},
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
}

func TestNewProvisioningBundleValidatesAndNormalizes(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle(
		"cvk-gnoi_01", provisioningTestServerName,
		pki.leafPEM, pki.privateKeyPKCS8, pki.caBundlePEM,
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	if got := bundle.CertificateID(); got != "cvk-gnoi_01" {
		t.Fatalf("CertificateID=%q, want cvk-gnoi_01", got)
	}
	privateBlock, rest := pem.Decode(bundle.privateKeyPEM)
	if privateBlock == nil || privateBlock.Type != "RSA PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("normalized private key is not one RSA PRIVATE KEY PEM block")
	}
	publicBlock, rest := pem.Decode(bundle.publicKeyPEM)
	if publicBlock == nil || publicBlock.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("normalized public key is not one PUBLIC KEY PEM block")
	}
	publicKey, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}
	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok || rsaPublicKey.N.Cmp(pki.leafKey.N) != 0 || rsaPublicKey.E != pki.leafKey.E {
		t.Fatal("normalized public key does not match leaf key")
	}
	if len(bundle.caPEM) != 2 || !bytes.Equal(bundle.caPEM[0], pki.intermediatePEM) || !bytes.Equal(bundle.caPEM[1], pki.rootPEM) {
		t.Fatalf("normalized CA order changed: got %d certificates", len(bundle.caPEM))
	}
	trustCopy := bundle.ClientTrustCAPEM()
	if !bytes.Equal(trustCopy, pki.caBundlePEM) {
		t.Fatal("ClientTrustCAPEM did not return the verified issuer-to-root chain")
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(trustCopy) {
		t.Fatal("ClientTrustCAPEM could not be loaded into a client trust pool")
	}
	if _, err := pki.leaf.Verify(x509.VerifyOptions{
		DNSName:   provisioningTestServerName,
		Roots:     clientRoots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("leaf-only TLS chain did not verify against ClientTrustCAPEM: %v", err)
	}
	trustCopy[0] ^= 0xff
	if bytes.Equal(trustCopy, bundle.ClientTrustCAPEM()) {
		t.Fatal("ClientTrustCAPEM exposed mutable bundle storage")
	}
}

func TestProvisioningBundleFormattingRedactsAllFields(t *testing.T) {
	bundle := ProvisioningBundle{
		certificateID:      "sensitive-certificate-id",
		expectedServerName: "sensitive-server-name",
		leafPEM:            []byte("sensitive-leaf"),
		privateKeyPEM:      []byte("sensitive-private-key"),
		publicKeyPEM:       []byte("sensitive-public-key"),
		caPEM:              [][]byte{[]byte("sensitive-ca")},
		clientTrustCAPEM:   []byte("sensitive-client-trust-ca"),
	}
	const want = "ProvisioningBundle{REDACTED}"
	for _, operand := range []any{bundle, &bundle} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
			if got := fmt.Sprintf(format, operand); got != want {
				t.Errorf("Sprintf(%q, %T)=%q, want %q", format, operand, got, want)
			}
		}
	}
}

func TestNewProvisioningBundlePreservesCompleteCAReplacementBundle(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	unrelated := newProvisioningTestPKI(t, "unrelated.example.test")
	completeCABundle := bytes.Join([][]byte{
		unrelated.intermediatePEM,
		unrelated.rootPEM,
		pki.intermediatePEM,
		pki.rootPEM,
	}, nil)

	bundle, err := NewProvisioningBundle(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.privateKeyPKCS1, completeCABundle,
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	wantCAs := [][]byte{unrelated.intermediatePEM, unrelated.rootPEM, pki.intermediatePEM, pki.rootPEM}
	if len(bundle.caPEM) != len(wantCAs) {
		t.Fatalf("CA bundle has %d certificates, want %d", len(bundle.caPEM), len(wantCAs))
	}
	for i := range wantCAs {
		if !bytes.Equal(bundle.caPEM[i], wantCAs[i]) {
			t.Fatalf("CA certificate %d was not retained in input order", i)
		}
	}
	if got := bundle.ClientTrustCAPEM(); !bytes.Equal(got, pki.caBundlePEM) {
		t.Fatal("ClientTrustCAPEM did not select only the leaf's verified issuer chain")
	}

	ts := newTestServer(t)
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	installedCAs := ts.Cert.installRequest.GetLoadCertificate().GetCaCertificates()
	if len(installedCAs) != len(wantCAs) {
		t.Fatalf("Install sent %d CA certificates, want %d", len(installedCAs), len(wantCAs))
	}
	for i := range wantCAs {
		if !bytes.Equal(installedCAs[i].GetCertificate(), wantCAs[i]) {
			t.Fatalf("Install CA certificate %d was not sent in input order", i)
		}
	}
}

func TestNewProvisioningBundleRejectsUnsafeMaterial(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	other := newProvisioningTestPKI(t, provisioningTestServerName)
	tweak := func(certificateID, serverName string, leaf, key, ca []byte) func() (string, string, []byte, []byte, []byte) {
		return func() (string, string, []byte, []byte, []byte) {
			return certificateID, serverName, leaf, key, ca
		}
	}

	tweakWeakKey := func() (string, string, []byte, []byte, []byte) {
		weakKey := mustRSAKey(t, 1024)
		now := time.Now()
		leafTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: provisioningTestServerName},
			DNSNames: []string{provisioningTestServerName}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		leaf := mustIssueCertificate(t, leafTemplate, pki.intermediate, &weakKey.PublicKey, pki.intermediateKey)
		key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(weakKey)})
		return "cvk-gnoi", provisioningTestServerName, certificatePEM(leaf), key, pki.caBundlePEM
	}

	tweakECKey := func() (string, string, []byte, []byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("MarshalPKCS8PrivateKey EC: %v", err)
		}
		return "cvk-gnoi", provisioningTestServerName, pki.leafPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), pki.caBundlePEM
	}

	caLeaf := &x509.Certificate{
		SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: provisioningTestServerName}, DNSNames: []string{provisioningTestServerName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	caLeafCert := mustIssueCertificate(t, caLeaf, pki.intermediate, &pki.leafKey.PublicKey, pki.intermediateKey)

	tests := []struct {
		name    string
		input   func() (string, string, []byte, []byte, []byte)
		wantErr string
	}{
		{name: "invalid certificate ID", input: tweak("bad id", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM), wantErr: "certificate ID"},
		{name: "missing server name", input: tweak("cvk-gnoi", "", pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM), wantErr: "server name is required"},
		{name: "multiple leaves", input: tweak("cvk-gnoi", provisioningTestServerName, append(bytes.Clone(pki.leafPEM), pki.leafPEM...), pki.privateKeyPKCS1, pki.caBundlePEM), wantErr: "exactly one"},
		{name: "CA used as leaf", input: tweak("cvk-gnoi", provisioningTestServerName, certificatePEM(caLeafCert), pki.privateKeyPKCS1, pki.caBundlePEM), wantErr: "must not be a CA"},
		{name: "mismatched key", input: tweak("cvk-gnoi", provisioningTestServerName, pki.leafPEM, other.privateKeyPKCS1, pki.caBundlePEM), wantErr: "do not match"},
		{name: "weak RSA key", input: tweakWeakKey, wantErr: "at least 2048"},
		{name: "non-RSA key", input: tweakECKey, wantErr: "must contain an RSA key"},
		{name: "SAN mismatch", input: tweak("cvk-gnoi", "other.example.test", pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM), wantErr: "not other.example.test"},
		{name: "empty CA bundle", input: tweak("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, nil), wantErr: "at least one"},
		{name: "non-CA in CA bundle", input: tweak("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.leafPEM), wantErr: "is not a CA"},
		{name: "missing self-signed root", input: tweak("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.intermediatePEM), wantErr: "self-signed root"},
		{name: "unrelated CA", input: tweak("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, other.caBundlePEM), wantErr: "verify leaf certificate"},
		{name: "trailing leaf data", input: tweak("cvk-gnoi", provisioningTestServerName, append(bytes.Clone(pki.leafPEM), []byte("not pem")...), pki.privateKeyPKCS1, pki.caBundlePEM), wantErr: "outside PEM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certificateID, serverName, leaf, key, ca := tt.input()
			_, err := NewProvisioningBundle(certificateID, serverName, leaf, key, ca)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewProvisioningBundle error=%v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewProvisioningBundleWithSigningKeyRequiresVerifiedLeafIssuer(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	unrelated := newProvisioningTestPKI(t, "unrelated.example.test")
	completeCABundle := bytes.Join([][]byte{
		unrelated.intermediatePEM,
		unrelated.rootPEM,
		pki.intermediatePEM,
		pki.rootPEM,
	}, nil)
	_, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, completeCABundle, unrelated.intermediateKeyPEM(t),
	)
	if err == nil || !strings.Contains(err.Error(), "verified issuer chain") {
		t.Fatalf("error=%v, want signing-key issuer-chain rejection", err)
	}
}

func TestNewProvisioningBundleWithSigningKeyRejectsEmptyCAKey(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	_, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.caBundlePEM, []byte(" \n"),
	)
	if err == nil || !strings.Contains(err.Error(), "ca.key is present but empty") {
		t.Fatalf("error=%v, want empty ca.key rejection", err)
	}
}

func TestProvisioningCertificateState(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}

	t.Run("missing", func(t *testing.T) {
		ts := newTestServer(t)
		ts.Cert.getResp = &certpb.GetCertificatesResponse{}
		state, err := ts.client(t).ProvisioningCertificateState(context.Background(), bundle)
		if err != nil || state != ProvisioningCertificateMissing {
			t.Fatalf("state=%q err=%v, want Missing", state, err)
		}
	})

	t.Run("matching ignores PEM whitespace", func(t *testing.T) {
		ts := newTestServer(t)
		ts.Cert.getResp = &certpb.GetCertificatesResponse{CertificateInfo: []*certpb.CertificateInfo{{
			CertificateId: "cvk-gnoi",
			Certificate:   &certpb.Certificate{Type: certpb.CertificateType_CT_X509, Certificate: append([]byte("\n"), pki.leafPEM...)},
		}}}
		state, err := ts.client(t).ProvisioningCertificateState(context.Background(), bundle)
		if err != nil || state != ProvisioningCertificateMatching {
			t.Fatalf("state=%q err=%v, want Matching", state, err)
		}
	})

	t.Run("occupied by another certificate", func(t *testing.T) {
		other := newProvisioningTestPKI(t, provisioningTestServerName)
		ts := newTestServer(t)
		ts.Cert.getResp = &certpb.GetCertificatesResponse{CertificateInfo: []*certpb.CertificateInfo{{
			CertificateId: "cvk-gnoi",
			Certificate:   &certpb.Certificate{Type: certpb.CertificateType_CT_X509, Certificate: other.leafPEM},
		}}}
		state, err := ts.client(t).ProvisioningCertificateState(context.Background(), bundle)
		var conflict *ErrCertificateIDConflict
		if state != "" || !errors.As(err, &conflict) {
			t.Fatalf("state=%q error=%T %v, want ErrCertificateIDConflict", state, err, err)
		}
		if conflict.CertificateID != "cvk-gnoi" || conflict.ExpectedFingerprint == "" || conflict.InstalledFingerprint == "" {
			t.Fatalf("conflict=%+v", conflict)
		}
	})
}

func TestProvisioningCertificateStateMatchesTargetGeneratedCertificateAfterRestart(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	newBundle := func(t *testing.T) *ProvisioningBundle {
		t.Helper()
		bundle, err := NewProvisioningBundleWithSigningKey(
			"cvk-gnoi", provisioningTestServerName,
			pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
		)
		if err != nil {
			t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
		}
		return bundle
	}

	issuedPEM, err := newBundle(t).signCSR(newCertificateRequestPEM(t, provisioningTestServerName))
	if err != nil {
		t.Fatalf("signCSR: %v", err)
	}
	// Reconstructing the bundle models a worker restart: the random serial and
	// target public key are not retained locally, so matching must use the
	// configured certificate profile rather than an in-memory fingerprint.
	bundleAfterRestart := newBundle(t)
	ts := newTestServer(t)
	ts.Cert.getResp = &certpb.GetCertificatesResponse{CertificateInfo: []*certpb.CertificateInfo{{
		CertificateId: "cvk-gnoi",
		Certificate:   &certpb.Certificate{Type: certpb.CertificateType_CT_X509, Certificate: issuedPEM},
	}}}
	state, err := ts.client(t).ProvisioningCertificateState(context.Background(), bundleAfterRestart)
	if err != nil || state != ProvisioningCertificateMatching {
		t.Fatalf("state=%q err=%v, want Matching after bundle reconstruction", state, err)
	}

	other := newProvisioningTestPKI(t, provisioningTestServerName)
	ts = newTestServer(t)
	ts.Cert.getResp = &certpb.GetCertificatesResponse{CertificateInfo: []*certpb.CertificateInfo{{
		CertificateId: "cvk-gnoi",
		Certificate:   &certpb.Certificate{Type: certpb.CertificateType_CT_X509, Certificate: other.leafPEM},
	}}}
	state, err = ts.client(t).ProvisioningCertificateState(context.Background(), bundleAfterRestart)
	var conflict *ErrCertificateIDConflict
	if state != "" || !errors.As(err, &conflict) {
		t.Fatalf("state=%q err=%T %v, want issuer conflict", state, err, err)
	}
}

func TestTargetGeneratedCertificateProfileRejectsUnsafeMatches(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
	}
	issuedPEM, err := bundle.signCSR(newCertificateRequestPEM(t, provisioningTestServerName))
	if err != nil {
		t.Fatalf("signCSR: %v", err)
	}
	issued, _, err := parseSingleCertificatePEM(issuedPEM, "issued certificate")
	if err != nil {
		t.Fatalf("parse issued certificate: %v", err)
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tests := []struct {
		name      string
		publicKey any
		mutate    func(*x509.Certificate)
		wantErr   string
	}{
		{name: "subject", publicKey: issued.PublicKey, mutate: func(cert *x509.Certificate) { cert.Subject.CommonName = "other.example.test" }, wantErr: "subject"},
		{name: "validity", publicKey: issued.PublicKey, mutate: func(cert *x509.Certificate) { cert.NotAfter = cert.NotAfter.Add(-time.Minute) }, wantErr: "validity"},
		{name: "usage", publicKey: issued.PublicKey, mutate: func(cert *x509.Certificate) { cert.KeyUsage = x509.KeyUsageDigitalSignature }, wantErr: "usages"},
		{name: "SAN", publicKey: issued.PublicKey, mutate: func(cert *x509.Certificate) { cert.DNSNames = []string{"other.example.test"} }, wantErr: "alternative names"},
		{name: "ECDSA key", publicKey: &ecKey.PublicKey, mutate: func(*x509.Certificate) {}, wantErr: "RSA key"},
		{name: "weak RSA key", publicKey: &mustRSAKey(t, 1024).PublicKey, mutate: func(*x509.Certificate) {}, wantErr: "RSA key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := reissueProvisioningCertificate(t, issued, pki.intermediate, tt.publicKey, pki.intermediateKey, tt.mutate)
			if err := bundle.validateTargetGeneratedCertificate(candidate); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestSignCSRAcceptsTargetOmittedProfileFields(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
	}
	csrKey := mustRSAKey(t, 2048)
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, csrKey)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	issuedPEM, err := bundle.signCSR(pem.EncodeToMemory(&pem.Block{
		Type:  "NEW CERTIFICATE REQUEST",
		Bytes: csrDER,
	}))
	if err != nil {
		t.Fatalf("sign CSR with omitted target profile fields: %v", err)
	}
	issued, _, err := parseSingleCertificatePEM(issuedPEM, "issued certificate")
	if err != nil {
		t.Fatalf("parse issued certificate: %v", err)
	}
	issuedKey, ok := issued.PublicKey.(*rsa.PublicKey)
	if !ok || !rsaPublicKeysEqual(issuedKey, &csrKey.PublicKey) {
		t.Fatal("issued certificate did not retain the target CSR public key")
	}
	if !equalSubjects(issued.Subject, bundle.leafTemplate.Subject) ||
		!slices.Equal(issued.DNSNames, bundle.leafTemplate.DNSNames) ||
		!equalIPAddresses(issued.IPAddresses, bundle.leafTemplate.IPAddresses) {
		t.Fatal("issued certificate did not retain the locally validated profile")
	}
}

func TestInstallProvisioningCertificateWireShape(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS8, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	request := ts.Cert.installRequest
	if request == nil || request.GetGenerateCsr() != nil || request.GetLoadCertificate() == nil {
		t.Fatalf("Install request=%+v, want one LoadCertificate request", request)
	}
	load := request.GetLoadCertificate()
	if load.CertificateId != "cvk-gnoi" {
		t.Fatalf("certificate ID=%q", load.CertificateId)
	}
	if load.Certificate == nil || load.Certificate.Type != certpb.CertificateType_CT_X509 || !bytes.Equal(load.Certificate.Certificate, bundle.leafPEM) {
		t.Fatalf("leaf certificate was not sent as normalized CT_X509 PEM")
	}
	if load.KeyPair == nil || !bytes.Equal(load.KeyPair.PrivateKey, bundle.privateKeyPEM) || !bytes.Equal(load.KeyPair.PublicKey, bundle.publicKeyPEM) {
		t.Fatalf("normalized key pair was not sent")
	}
	if len(load.CaCertificates) != 2 || !bytes.Equal(load.CaCertificates[0].Certificate, pki.intermediatePEM) || !bytes.Equal(load.CaCertificates[1].Certificate, pki.rootPEM) {
		t.Fatalf("CA bundle order/content=%+v", load.CaCertificates)
	}
	for i, ca := range load.CaCertificates {
		if ca.Type != certpb.CertificateType_CT_X509 {
			t.Fatalf("CA %d type=%v, want CT_X509", i, ca.Type)
		}
	}
}

func TestInstallProvisioningCertificateWaitsForResponseBeforeHalfClose(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS8, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installRejectEarlyEOF = true
	ts.Cert.installFinished = make(chan struct{})
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	<-ts.Cert.installFinished
	if ts.Cert.installRequest == nil {
		t.Fatal("fake server did not receive the LoadCertificate request")
	}
	if ts.Cert.installEOFEarly {
		t.Fatal("client half-closed the Install stream before LoadCertificateResponse")
	}
	if !ts.Cert.installEOFSeen {
		t.Fatal("client did not close its send side after LoadCertificateResponse")
	}
}

func TestInstallProvisioningCertificateDoesNotWaitForFinalEOF(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS8, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installResponseSent = make(chan struct{})
	ts.Cert.installRelease = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- ts.client(t).InstallProvisioningCertificate(context.Background(), bundle)
	}()
	<-ts.Cert.installResponseSent
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("InstallProvisioningCertificate: %v", err)
		}
	case <-time.After(time.Second):
		close(ts.Cert.installRelease)
		t.Fatal("Install waited for final EOF after LoadCertificateResponse")
	}
	close(ts.Cert.installRelease)
}

func TestInstallProvisioningCertificateCSRWaitsForResponseBeforeHalfClose(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installCSR = newCertificateRequestPEM(t, provisioningTestServerName)
	ts.Cert.installRejectEarlyEOF = true
	ts.Cert.installFinished = make(chan struct{})
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	<-ts.Cert.installFinished
	if len(ts.Cert.installRequests) != 2 || ts.Cert.installRequests[1].GetLoadCertificate() == nil {
		t.Fatalf("Install requests=%+v, want GenerateCSR then LoadCertificate", ts.Cert.installRequests)
	}
	if ts.Cert.installEOFEarly {
		t.Fatal("client half-closed the CSR Install stream before LoadCertificateResponse")
	}
	if !ts.Cert.installEOFSeen {
		t.Fatal("client did not close the CSR Install send side after LoadCertificateResponse")
	}
}

func TestInstallProvisioningCertificateCSRDoesNotWaitForFinalEOF(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installCSR = newCertificateRequestPEM(t, provisioningTestServerName)
	ts.Cert.installResponseSent = make(chan struct{})
	ts.Cert.installRelease = make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- ts.client(t).InstallProvisioningCertificate(context.Background(), bundle)
	}()
	<-ts.Cert.installResponseSent
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("InstallProvisioningCertificate: %v", err)
		}
	case <-time.After(time.Second):
		close(ts.Cert.installRelease)
		t.Fatal("CSR Install waited for final EOF after LoadCertificateResponse")
	}
	close(ts.Cert.installRelease)
}

func TestInstallProvisioningCertificateUsesCSRFlowWhenSigningKeyPresent(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installCSR = newCertificateRequestPEM(t, provisioningTestServerName)
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	if len(ts.Cert.installRequests) != 2 {
		t.Fatalf("Install sent %d requests, want GenerateCSR then LoadCertificate", len(ts.Cert.installRequests))
	}
	generate := ts.Cert.installRequests[0].GetGenerateCsr()
	if generate == nil {
		t.Fatalf("first request=%+v, want GenerateCSR", ts.Cert.installRequests[0])
	}
	if generate.GetCertificateId() != "cvk-gnoi" {
		t.Fatalf("GenerateCSR certificate ID=%q, want cvk-gnoi", generate.GetCertificateId())
	}
	csrParams := generate.GetCsrParams()
	if csrParams.GetType() != certpb.CertificateType_CT_X509 || csrParams.GetMinKeySize() != 2048 || csrParams.GetKeyType() != certpb.KeyType_KT_RSA {
		t.Fatalf("CSR params key size/type = %d/%s, want 2048/KT_RSA", csrParams.GetMinKeySize(), csrParams.GetKeyType())
	}
	if csrParams.GetCommonName() != provisioningTestServerName ||
		csrParams.GetCountry() != "US" ||
		csrParams.GetState() != "California" ||
		csrParams.GetOrganization() != "Cisco" ||
		csrParams.GetOrganizationalUnit() != "gNOI" ||
		csrParams.GetIpAddress() != provisioningTestIPAddress {
		t.Fatalf("CSR identity params=%+v, want complete tls.crt subject and IP SAN", csrParams)
	}
	load := ts.Cert.installRequests[1].GetLoadCertificate()
	if load == nil {
		t.Fatalf("second request=%+v, want LoadCertificate", ts.Cert.installRequests[1])
	}
	if load.KeyPair != nil {
		t.Fatal("CSR flow must not send an external private key")
	}
	if load.CertificateId != "" {
		t.Fatalf("CSR flow LoadCertificate certificate ID=%q, want empty (the GenerateCSR request owns it)", load.CertificateId)
	}
	if load.Certificate == nil || load.Certificate.GetType() != certpb.CertificateType_CT_X509 {
		t.Fatalf("CSR flow certificate=%+v, want CT_X509", load.Certificate)
	}
	if len(load.CaCertificates) != 2 ||
		load.CaCertificates[0].GetType() != certpb.CertificateType_CT_X509 ||
		load.CaCertificates[1].GetType() != certpb.CertificateType_CT_X509 ||
		!bytes.Equal(load.CaCertificates[0].GetCertificate(), pki.intermediatePEM) ||
		!bytes.Equal(load.CaCertificates[1].GetCertificate(), pki.rootPEM) {
		t.Fatalf("CSR flow CA bundle order/content=%+v", load.CaCertificates)
	}
	cert, _, err := parseSingleCertificatePEM(load.Certificate.Certificate, "signed leaf")
	if err != nil {
		t.Fatalf("parse signed leaf: %v", err)
	}
	csrBlock, _ := pem.Decode(ts.Cert.installCSR)
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatalf("parse generated CSR: %v", err)
	}
	issuedKey, issuedRSA := cert.PublicKey.(*rsa.PublicKey)
	csrKey, csrRSA := csr.PublicKey.(*rsa.PublicKey)
	if !issuedRSA || !csrRSA || !rsaPublicKeysEqual(issuedKey, csrKey) {
		t.Fatal("signed leaf public key does not match the switch-generated CSR key")
	}
	if err := cert.CheckSignatureFrom(pki.intermediate); err != nil {
		t.Fatalf("signed leaf was not issued by intermediate: %v", err)
	}
	if err := cert.VerifyHostname(provisioningTestServerName); err != nil {
		t.Fatalf("signed leaf did not preserve the template DNS SAN: %v", err)
	}
	if cert.Subject.Organization[0] != "Cisco" {
		t.Fatalf("signed leaf subject=%+v, want validated tls.crt profile", cert.Subject)
	}
	if ts.Cert.canGenRequest != nil {
		t.Fatalf("CSR provisioning unexpectedly called CanGenerateCSR: %+v", ts.Cert.canGenRequest)
	}
}

func TestInstallProvisioningCertificateCSRSupportsDirectRootSigner(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	leafKey := mustRSAKey(t, 2048)
	directLeaf := reissueProvisioningCertificate(
		t, pki.leaf, pki.root, &leafKey.PublicKey, pki.rootKey,
		func(*x509.Certificate) {},
	)
	rootKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(pki.rootKey),
	})
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		certificatePEM(directLeaf), pki.rootPEM, rootKeyPEM,
	)
	if err != nil {
		t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installCSR = newCertificateRequestPEM(t, provisioningTestServerName)
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	if len(ts.Cert.installRequests) != 2 {
		t.Fatalf("Install sent %d requests, want GenerateCSR then LoadCertificate", len(ts.Cert.installRequests))
	}
	load := ts.Cert.installRequests[1].GetLoadCertificate()
	if load == nil || load.GetCertificate() == nil {
		t.Fatalf("second request=%+v, want signed LoadCertificate", ts.Cert.installRequests[1])
	}
	issued, _, err := parseSingleCertificatePEM(load.GetCertificate().GetCertificate(), "signed leaf")
	if err != nil {
		t.Fatalf("parse signed leaf: %v", err)
	}
	if err := issued.CheckSignatureFrom(pki.root); err != nil {
		t.Fatalf("signed leaf was not issued directly by configured root: %v", err)
	}
	if len(load.GetCaCertificates()) != 1 || !bytes.Equal(load.GetCaCertificates()[0].GetCertificate(), pki.rootPEM) {
		t.Fatalf("direct-root CA bundle=%+v, want one configured root", load.GetCaCertificates())
	}
}

func TestInstallProvisioningCertificateRejectsInvalidGeneratedCSR(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	newBundle := func(t *testing.T) *ProvisioningBundle {
		t.Helper()
		bundle, err := NewProvisioningBundleWithSigningKey(
			"cvk-gnoi", provisioningTestServerName,
			pki.leafPEM, pki.caBundlePEM, pki.intermediateKeyPEM(t),
		)
		if err != nil {
			t.Fatalf("NewProvisioningBundleWithSigningKey: %v", err)
		}
		return bundle
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	valid := newCertificateRequestPEM(t, provisioningTestServerName)
	block, _ := pem.Decode(valid)
	badSignatureDER := bytes.Clone(block.Bytes)
	badSignatureDER[len(badSignatureDER)-1] ^= 0xff

	tests := []struct {
		name    string
		csr     []byte
		csrType certpb.CertificateType
		wantErr string
	}{
		{name: "wrong certificate type", csr: valid, csrType: certpb.CertificateType_CT_UNKNOWN, wantErr: "want CT_X509"},
		{name: "ECDSA key", csr: newCertificateRequestPEMForKey(t, provisioningTestServerName, ecKey), csrType: certpb.CertificateType_CT_X509, wantErr: "must be RSA"},
		{name: "weak RSA key", csr: newCertificateRequestPEMForKey(t, provisioningTestServerName, mustRSAKey(t, 1024)), csrType: certpb.CertificateType_CT_X509, wantErr: "at least 2048"},
		{name: "invalid signature", csr: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: badSignatureDER}), csrType: certpb.CertificateType_CT_X509, wantErr: "verify CSR signature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installCSR = tt.csr
			ts.Cert.installCSRType = &tt.csrType
			err := ts.client(t).InstallProvisioningCertificate(context.Background(), newBundle(t))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error=%v, want substring %q", err, tt.wantErr)
			}
			if len(ts.Cert.installRequests) != 1 {
				t.Fatalf("sent %d Install requests after invalid CSR, want only GenerateCSR", len(ts.Cert.installRequests))
			}
		})
	}
}

func TestInstallProvisioningCertificateRejectsUnexpectedResponse(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installResponse = &certpb.InstallCertificateResponse{
		InstallResponse: &certpb.InstallCertificateResponse_GeneratedCsr{
			GeneratedCsr: &certpb.GenerateCSRResponse{},
		},
	}
	err = ts.client(t).InstallProvisioningCertificate(context.Background(), bundle)
	if err == nil || !strings.Contains(err.Error(), "expected LoadCertificateResponse") {
		t.Fatalf("error=%v, want unexpected response error", err)
	}
}

func TestInstallProvisioningCertificateClassifiesRestartLoss(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}

	for _, tt := range []struct {
		name          string
		serverErr     error
		indeterminate bool
	}{
		{name: "gNXI restart", serverErr: status.Error(codes.Unavailable, "server restarting"), indeterminate: true},
		{name: "IOS XE event timeout", serverErr: status.Error(codes.Aborted, "Timeout waiting for event"), indeterminate: true},
		{name: "internal completion failure", serverErr: status.Error(codes.Internal, "completion status lost"), indeterminate: true},
		{name: "certificate ID already exists", serverErr: status.Error(codes.AlreadyExists, "certificate ID already exists"), indeterminate: true},
		{name: "request rejected", serverErr: status.Error(codes.InvalidArgument, "bad certificate"), indeterminate: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			ts.Cert.installErr = tt.serverErr
			err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle)
			if got := IsCertificateInstallIndeterminate(err); got != tt.indeterminate {
				t.Fatalf("IsCertificateInstallIndeterminate=%v error=%T %v, want %v", got, err, err, tt.indeterminate)
			}
			if ts.Cert.installRequest == nil {
				t.Fatal("fake server did not receive the LoadCertificate request")
			}
		})
	}
}

func TestInstallProvisioningCertificateTreatsLoadAcknowledgementAsSuccess(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installFinalErr = status.Error(codes.Unavailable, "gNXI restarting after certificate acknowledgement")
	err = ts.client(t).InstallProvisioningCertificate(context.Background(), bundle)
	if err != nil {
		t.Fatalf("error=%T %v, want success after LoadCertificateResponse", err, err)
	}
}

func TestVerifyWrapsExactIOSXEDeviceNotProvisionedError(t *testing.T) {
	for _, message := range []string{iosXEDeviceNotProvisionedMessage, iosXEDeviceNotProvisionedMessage + "."} {
		t.Run(message, func(t *testing.T) {
			ts := newTestServer(t)
			ts.OS.verifyErr = status.Error(codes.FailedPrecondition, message)
			_, err := ts.client(t).Verify(context.Background())
			var notProvisioned *ErrDeviceNotProvisioned
			if !errors.As(err, &notProvisioned) || !IsDeviceNotProvisioned(err) {
				t.Fatalf("Verify error=%T %v, want ErrDeviceNotProvisioned", err, err)
			}
			if status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("status.Code=%v, want FailedPrecondition", status.Code(err))
			}
		})
	}

	other := status.Error(codes.FailedPrecondition, "Device certificate has not been provisioned")
	if IsDeviceNotProvisioned(other) {
		t.Fatalf("unrelated FailedPrecondition was classified as device-not-provisioned")
	}
}

func mustRSAKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("rsa.GenerateKey(%d): %v", bits, err)
	}
	return key
}

func mustIssueCertificate(t *testing.T, template, parent *x509.Certificate, publicKey any, signer any) *x509.Certificate {
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
