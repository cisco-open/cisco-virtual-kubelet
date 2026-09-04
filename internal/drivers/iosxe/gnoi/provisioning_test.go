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
	"strings"
	"testing"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const provisioningTestServerName = "switch.example.test"

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
		Subject:      pkix.Name{CommonName: serverName},
		DNSNames:     []string{serverName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(6 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
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
	key := mustRSAKey(t, 2048)
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: serverName},
		DNSNames: []string{serverName},
	}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
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
	rootCopy := bundle.RootCAPEM()
	if !bytes.Equal(rootCopy, pki.rootPEM) {
		t.Fatal("RootCAPEM did not return the validated root certificate")
	}
	rootCopy[0] ^= 0xff
	if bytes.Equal(rootCopy, bundle.RootCAPEM()) {
		t.Fatal("RootCAPEM exposed mutable bundle storage")
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
		rootCAPEM:          []byte("sensitive-root-ca"),
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
	if got := bundle.RootCAPEM(); !bytes.Equal(got, pki.rootPEM) {
		t.Fatal("RootCAPEM did not select the leaf's verified root")
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
	if !ts.Cert.installEOFSeen {
		t.Fatal("Install did not half-close and observe the final stream status")
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

func TestInstallProvisioningCertificateHalfClosesBeforeWaitingForResponse(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS8, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installWaitEOF = true
	if err := ts.client(t).InstallProvisioningCertificate(context.Background(), bundle); err != nil {
		t.Fatalf("InstallProvisioningCertificate: %v", err)
	}
	if ts.Cert.installRequest == nil {
		t.Fatal("fake server did not receive the LoadCertificate request")
	}
	if !ts.Cert.installEOFSeen {
		t.Fatal("server did not observe client half-close before response")
	}
}

func TestInstallProvisioningCertificateUsesCSRFlowWhenSigningKeyPresent(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundleWithSigningKey(
		"cvk-gnoi", provisioningTestServerName,
		pki.leafPEM, pki.privateKeyPKCS8, pki.caBundlePEM, pki.intermediateKeyPEM(t),
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
	if ts.Cert.installRequests[0].GetGenerateCsr() == nil {
		t.Fatalf("first request=%+v, want GenerateCSR", ts.Cert.installRequests[0])
	}
	csrParams := ts.Cert.installRequests[0].GetGenerateCsr().GetCsrParams()
	if csrParams.GetMinKeySize() != 2048 || csrParams.GetKeyType() != certpb.KeyType_KT_RSA {
		t.Fatalf("CSR params key size/type = %d/%s, want 2048/KT_RSA", csrParams.GetMinKeySize(), csrParams.GetKeyType())
	}
	load := ts.Cert.installRequests[1].GetLoadCertificate()
	if load == nil {
		t.Fatalf("second request=%+v, want LoadCertificate", ts.Cert.installRequests[1])
	}
	if load.KeyPair != nil {
		t.Fatal("CSR flow must not send an external private key")
	}
	cert, _, err := parseSingleCertificatePEM(load.Certificate.Certificate, "signed leaf")
	if err != nil {
		t.Fatalf("parse signed leaf: %v", err)
	}
	if err := cert.CheckSignatureFrom(pki.intermediate); err != nil {
		t.Fatalf("signed leaf was not issued by intermediate: %v", err)
	}
	if !ts.Cert.installEOFSeen {
		t.Fatal("server did not observe client half-close")
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

func TestInstallProvisioningCertificateClassifiesLossAfterAcknowledgement(t *testing.T) {
	pki := newProvisioningTestPKI(t, provisioningTestServerName)
	bundle, err := NewProvisioningBundle("cvk-gnoi", provisioningTestServerName, pki.leafPEM, pki.privateKeyPKCS1, pki.caBundlePEM)
	if err != nil {
		t.Fatalf("NewProvisioningBundle: %v", err)
	}
	ts := newTestServer(t)
	ts.Cert.installFinalErr = status.Error(codes.Unavailable, "gNXI restarting after certificate acknowledgement")
	err = ts.client(t).InstallProvisioningCertificate(context.Background(), bundle)
	if !IsCertificateInstallIndeterminate(err) {
		t.Fatalf("error=%T %v, want ErrCertificateInstallIndeterminate", err, err)
	}
	if !ts.Cert.installEOFSeen {
		t.Fatal("server did not observe the client half-close before final status")
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

func certificatePEM(certificate *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
}
