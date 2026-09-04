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
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var provisioningCertificateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ProvisioningBundle is a locally validated, normalized device identity for
// the gNOI CertificateManagement.Install external-key flow. Its formatter
// redacts all fields so struct formatting cannot expose private-key material.
type ProvisioningBundle struct {
	certificateID      string
	expectedServerName string
	leafPEM            []byte
	privateKeyPEM      []byte
	publicKeyPEM       []byte
	caPEM              [][]byte
	rootCAPEM          []byte
	leafFingerprint    [sha256.Size]byte
}

// Format prevents every fmt verb from rendering certificate or private-key
// material. Use a value receiver so both ProvisioningBundle values and pointers
// are redacted.
func (ProvisioningBundle) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("ProvisioningBundle{REDACTED}"))
}

// CertificateID returns the IOS XE PKI trustpoint identifier that the bundle
// will create.
func (b *ProvisioningBundle) CertificateID() string {
	if b == nil {
		return ""
	}
	return b.certificateID
}

// RootCAPEM returns a copy of the validated self-signed root certificate.
// The complete replacement bundle remains part of the Install request, but
// unrelated CAs and intermediates are not promoted to client trust anchors.
func (b *ProvisioningBundle) RootCAPEM() []byte {
	if b == nil {
		return nil
	}
	return bytes.Clone(b.rootCAPEM)
}

// ProvisioningCertificateState describes whether the requested certificate
// identity must be installed. A certificate ID occupied by any other
// certificate is returned as ErrCertificateIDConflict instead of a state.
type ProvisioningCertificateState string

const (
	ProvisioningCertificateMissing  ProvisioningCertificateState = "Missing"
	ProvisioningCertificateMatching ProvisioningCertificateState = "Matching"
)

// ErrCertificateIDConflict means IOS XE already has the requested certificate
// ID, but it cannot be proven to contain the requested leaf certificate.
// Install must not be retried with that ID because gNOI Install is create-only.
type ErrCertificateIDConflict struct {
	CertificateID        string
	ExpectedFingerprint  string
	InstalledFingerprint string
	Cause                error
}

func (e *ErrCertificateIDConflict) Error() string {
	if e == nil {
		return "gnoi Cert.Install certificate ID conflict"
	}
	msg := fmt.Sprintf("gnoi Cert.Install certificate ID %q already contains a different certificate", e.CertificateID)
	if e.Cause != nil {
		return msg + ": " + e.Cause.Error()
	}
	return msg
}

func (e *ErrCertificateIDConflict) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ErrCertificateInstallIndeterminate means the Install result must be reconciled
// by reconnecting and querying the certificate ID. The request may have been
// committed before the gNXI service restarted, or the target may report that the
// create-only certificate ID already exists. Blindly retrying Install is unsafe.
type ErrCertificateInstallIndeterminate struct {
	CertificateID string
	Cause         error
}

func (e *ErrCertificateInstallIndeterminate) Error() string {
	if e == nil {
		return "gnoi Cert.Install outcome is indeterminate"
	}
	return fmt.Sprintf("gnoi Cert.Install certificate ID %q outcome is indeterminate: %v", e.CertificateID, e.Cause)
}

func (e *ErrCertificateInstallIndeterminate) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsCertificateInstallIndeterminate reports whether the Install result must be
// reconciled by reconnecting and querying the certificate ID.
func IsCertificateInstallIndeterminate(err error) bool {
	var target *ErrCertificateInstallIndeterminate
	return errors.As(err, &target)
}

// NewProvisioningBundle validates and normalizes the certificate material for
// IOS XE's gNOI provisioning flow. leafPEM must contain one server certificate,
// privateKeyPEM an unencrypted RSA key, and caBundlePEM the complete replacement
// target CA bundle. Every bundle certificate must be a CA, and the bundle must
// contain a valid path from the leaf to a self-signed root. Input order is
// retained for CertificateManagement.Install.
func NewProvisioningBundle(
	certificateID, expectedServerName string,
	leafPEM, privateKeyPEM, caBundlePEM []byte,
) (*ProvisioningBundle, error) {
	if !provisioningCertificateIDPattern.MatchString(certificateID) {
		return nil, fmt.Errorf("gnoi provisioning certificate ID must start with an alphanumeric and contain 1-64 characters from [A-Za-z0-9_.-]")
	}
	expectedServerName = strings.TrimSpace(expectedServerName)
	if expectedServerName == "" {
		return nil, fmt.Errorf("gnoi provisioning expected server name is required")
	}

	leaf, normalizedLeaf, err := parseSingleCertificatePEM(leafPEM, "leaf certificate")
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: %w", err)
	}
	if leaf.IsCA {
		return nil, fmt.Errorf("gnoi provisioning: leaf certificate must not be a CA")
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) {
		return nil, fmt.Errorf("gnoi provisioning: leaf certificate is not valid before %s", leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	if now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("gnoi provisioning: leaf certificate expired at %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}

	privateKey, normalizedPrivateKey, err := parseRSAPrivateKeyPEM(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: %w", err)
	}
	if privateKey.N.BitLen() < 2048 {
		return nil, fmt.Errorf("gnoi provisioning: RSA private key is %d bits; at least 2048 bits are required", privateKey.N.BitLen())
	}
	leafPublicKey, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("gnoi provisioning: leaf certificate public key must be RSA")
	}
	if leafPublicKey.E != privateKey.PublicKey.E || leafPublicKey.N.Cmp(privateKey.PublicKey.N) != 0 {
		return nil, fmt.Errorf("gnoi provisioning: leaf certificate and private key do not match")
	}

	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: encode RSA public key: %w", err)
	}
	normalizedPublicKey := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})

	caCerts, normalizedCAs, err := parseCertificateBundlePEM(caBundlePEM)
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: %w", err)
	}
	if len(caCerts) == 0 {
		return nil, fmt.Errorf("gnoi provisioning: CA bundle must contain at least one certificate")
	}
	for i, ca := range caCerts {
		if !ca.BasicConstraintsValid || !ca.IsCA {
			return nil, fmt.Errorf("gnoi provisioning: CA bundle certificate %d is not a CA", i+1)
		}
	}

	// ca_certificates replaces the target's complete CA bundle, so unrelated CA
	// groups must be retained. Build verification pools from the whole bundle and
	// trust only certificates that are demonstrably self-signed as roots.
	roots := x509.NewCertPool()
	intermediates := x509.NewCertPool()
	hasSelfSignedRoot := false
	for _, ca := range caCerts {
		intermediates.AddCert(ca)
		if bytes.Equal(ca.RawSubject, ca.RawIssuer) && ca.CheckSignatureFrom(ca) == nil {
			roots.AddCert(ca)
			hasSelfSignedRoot = true
		}
	}
	if !hasSelfSignedRoot {
		return nil, fmt.Errorf("gnoi provisioning: CA bundle must contain at least one self-signed root")
	}
	verifiedChains, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       expectedServerName,
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime:   now,
	})
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: verify leaf certificate for %q: %w", expectedServerName, err)
	}
	if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
		return nil, fmt.Errorf("gnoi provisioning: leaf verification returned no certificate chain")
	}
	verifiedRoot := verifiedChains[0][len(verifiedChains[0])-1]
	verifiedRootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: verifiedRoot.Raw})

	return &ProvisioningBundle{
		certificateID:      certificateID,
		expectedServerName: expectedServerName,
		leafPEM:            normalizedLeaf,
		privateKeyPEM:      normalizedPrivateKey,
		publicKeyPEM:       normalizedPublicKey,
		caPEM:              normalizedCAs,
		rootCAPEM:          verifiedRootPEM,
		leafFingerprint:    sha256.Sum256(leaf.Raw),
	}, nil
}

// ProvisioningCertificateState compares the requested leaf certificate with
// the certificate already occupying its ID on the target. Fingerprints are
// computed over parsed DER so harmless PEM formatting differences compare
// equal.
func (c *Client) ProvisioningCertificateState(ctx context.Context, bundle *ProvisioningBundle) (ProvisioningCertificateState, error) {
	if bundle == nil {
		return "", fmt.Errorf("gnoi Cert.GetCertificates: nil provisioning bundle")
	}
	certificates, err := c.GetCertificates(ctx)
	if err != nil {
		return "", err
	}

	var match *CertificateInfo
	for i := range certificates {
		if certificates[i].CertificateID != bundle.certificateID {
			continue
		}
		if match != nil {
			return "", bundle.conflictError("device returned duplicate records for the certificate ID", nil)
		}
		match = &certificates[i]
	}
	if match == nil {
		return ProvisioningCertificateMissing, nil
	}
	if match.Type != certpb.CertificateType_CT_X509.String() {
		return "", bundle.conflictError(fmt.Sprintf("installed certificate has type %q", match.Type), nil)
	}
	installed, _, err := parseSingleCertificatePEM(match.Certificate, "installed certificate")
	if err != nil {
		return "", bundle.conflictError("installed certificate cannot be parsed", err)
	}
	installedFingerprint := sha256.Sum256(installed.Raw)
	if installedFingerprint != bundle.leafFingerprint {
		return "", &ErrCertificateIDConflict{
			CertificateID:        bundle.certificateID,
			ExpectedFingerprint:  hex.EncodeToString(bundle.leafFingerprint[:]),
			InstalledFingerprint: hex.EncodeToString(installedFingerprint[:]),
		}
	}
	return ProvisioningCertificateMatching, nil
}

// InstallProvisioningCertificate sends the external-key form of gNOI
// CertificateManagement.Install. It sends exactly one LoadCertificate request
// and requires exactly one corresponding response. IOS XE may restart gNXI as
// soon as the certificate is committed, so transport loss after Send is
// returned as ErrCertificateInstallIndeterminate for post-reconnect checking.
func (c *Client) InstallProvisioningCertificate(ctx context.Context, bundle *ProvisioningBundle) error {
	if bundle == nil {
		return fmt.Errorf("gnoi Cert.Install: nil provisioning bundle")
	}
	if err := c.cap.ensureSupported(ServiceCert); err != nil {
		return err
	}
	stream, err := c.cert.Install(c.authCtx(ctx))
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return fmt.Errorf("gnoi Cert.Install open: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	caCertificates := make([]*certpb.Certificate, 0, len(bundle.caPEM))
	for _, ca := range bundle.caPEM {
		caCertificates = append(caCertificates, &certpb.Certificate{
			Type:        certpb.CertificateType_CT_X509,
			Certificate: bytes.Clone(ca),
		})
	}
	request := &certpb.InstallCertificateRequest{
		InstallRequest: &certpb.InstallCertificateRequest_LoadCertificate{
			LoadCertificate: &certpb.LoadCertificateRequest{
				Certificate: &certpb.Certificate{
					Type:        certpb.CertificateType_CT_X509,
					Certificate: bytes.Clone(bundle.leafPEM),
				},
				KeyPair: &certpb.KeyPair{
					PrivateKey: bytes.Clone(bundle.privateKeyPEM),
					PublicKey:  bytes.Clone(bundle.publicKeyPEM),
				},
				CertificateId:  bundle.certificateID,
				CaCertificates: caCertificates,
			},
		},
	}
	if err := stream.Send(request); err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "send LoadCertificateRequest", err)
	}
	response, err := stream.Recv()
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "receive LoadCertificateResponse", err)
	}
	if response == nil || response.GetLoadCertificate() == nil {
		err := fmt.Errorf("gnoi Cert.Install: expected LoadCertificateResponse")
		c.cap.Observe(ServiceCert, err)
		return err
	}
	if err := stream.CloseSend(); err != nil {
		c.cap.Observe(ServiceCert, err)
		return &ErrCertificateInstallIndeterminate{
			CertificateID: bundle.certificateID,
			Cause:         fmt.Errorf("close request stream after LoadCertificateResponse: %w", err),
		}
	}
	// Install has no Finalize message. A clean EOF after the client half-close
	// is the final successful stream status; any transport loss here may have
	// occurred after IOS XE committed the identity and restarted gNXI.
	extra, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		c.cap.Observe(ServiceCert, nil)
		return nil
	}
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "complete Install stream", err)
	}
	err = fmt.Errorf("gnoi Cert.Install: unexpected additional response %T", extra.GetInstallResponse())
	c.cap.Observe(ServiceCert, err)
	return err
}

func (b *ProvisioningBundle) conflictError(detail string, cause error) error {
	conflictCause := errors.New(detail)
	if cause != nil {
		conflictCause = fmt.Errorf("%s: %w", detail, cause)
	}
	return &ErrCertificateIDConflict{
		CertificateID:       b.certificateID,
		ExpectedFingerprint: hex.EncodeToString(b.leafFingerprint[:]),
		Cause:               conflictCause,
	}
}

func wrapCertificateInstallError(certificateID, operation string, err error) error {
	if certificateInstallRequiresRecheck(err) {
		return &ErrCertificateInstallIndeterminate{
			CertificateID: certificateID,
			Cause:         fmt.Errorf("%s: %w", operation, err),
		}
	}
	return fmt.Errorf("gnoi Cert.Install %s: %w", operation, err)
}

func certificateInstallRequiresRecheck(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(err) {
	case codes.AlreadyExists, codes.Canceled, codes.DeadlineExceeded, codes.Unavailable:
		return true
	default:
		return false
	}
}

func parseSingleCertificatePEM(data []byte, label string) (*x509.Certificate, []byte, error) {
	certificates, normalized, err := parseCertificatePEM(data, label)
	if err != nil {
		return nil, nil, err
	}
	if len(certificates) != 1 {
		return nil, nil, fmt.Errorf("%s must contain exactly one PEM certificate", label)
	}
	return certificates[0], normalized[0], nil
}

func parseCertificateBundlePEM(data []byte) ([]*x509.Certificate, [][]byte, error) {
	return parseCertificatePEM(data, "CA bundle")
}

func parseCertificatePEM(data []byte, label string) ([]*x509.Certificate, [][]byte, error) {
	rest := bytes.TrimSpace(data)
	var certificates []*x509.Certificate
	var normalized [][]byte
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN ")) {
			return nil, nil, fmt.Errorf("%s contains data outside PEM blocks", label)
		}
		block, next := pem.Decode(rest)
		if block == nil {
			return nil, nil, fmt.Errorf("%s contains an invalid PEM block", label)
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, nil, fmt.Errorf("%s contains PEM block type %q; want CERTIFICATE", label, block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s X.509 certificate: %w", label, err)
		}
		certificates = append(certificates, certificate)
		normalized = append(normalized, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))
		rest = bytes.TrimSpace(next)
	}
	return certificates, normalized, nil
}

func parseRSAPrivateKeyPEM(data []byte) (*rsa.PrivateKey, []byte, error) {
	trimmed := bytes.TrimSpace(data)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
		return nil, nil, fmt.Errorf("private key is not PEM encoded")
	}
	block, rest := pem.Decode(trimmed)
	if block == nil {
		return nil, nil, fmt.Errorf("private key contains an invalid PEM block")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, fmt.Errorf("private key must contain exactly one PEM block")
	}
	if x509.IsEncryptedPEMBlock(block) || strings.Contains(block.Type, "ENCRYPTED") {
		return nil, nil, fmt.Errorf("encrypted private keys are not supported")
	}

	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse PKCS#1 RSA private key: %w", err)
		}
		key = parsed
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("PKCS#8 private key must contain an RSA key")
		}
	default:
		return nil, nil, fmt.Errorf("private key PEM block type %q is not supported; want RSA PRIVATE KEY or PRIVATE KEY", block.Type)
	}
	if err := key.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid RSA private key: %w", err)
	}
	key.Precompute()
	return key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
}
