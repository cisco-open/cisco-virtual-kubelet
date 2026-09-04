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
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	certpb "github.com/openconfig/gnoi/cert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var provisioningCertificateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// IOS XE's documented and reference-tested certificate bootstrap uses a
// target-generated 2048-bit RSA key. Keep this independent of the public key
// in the certificate template: that key is replaced by the key in the CSR.
const iosXEProvisioningRSAKeyBits = 2048

// ProvisioningBundle is a locally validated, normalized device identity for
// gNOI CertificateManagement.Install. It supports IOS XE's target-generated
// CSR flow and the protocol's external-key compatibility flow. Its formatter
// redacts all fields so struct formatting cannot expose private-key material.
type ProvisioningBundle struct {
	certificateID      string
	expectedServerName string
	leafPEM            []byte
	privateKeyPEM      []byte
	publicKeyPEM       []byte
	caPEM              [][]byte
	clientTrustCAPEM   []byte
	leafFingerprint    [sha256.Size]byte
	leafTemplate       *x509.Certificate
	csrParams          *certpb.CSRParams
	signingCert        *x509.Certificate
	signingKey         *rsa.PrivateKey
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

// ClientTrustCAPEM returns the verified issuer-to-root chain for the requested
// device identity. Adding this chain only to the gNOI client's trust pool lets
// it reconnect when IOS XE serves either the full chain or only the leaf after
// provisioning, without trusting unrelated CAs from the replacement bundle.
func (b *ProvisioningBundle) ClientTrustCAPEM() []byte {
	if b == nil {
		return nil
	}
	return bytes.Clone(b.clientTrustCAPEM)
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
	return newProvisioningBundle(certificateID, expectedServerName, leafPEM, privateKeyPEM, caBundlePEM, nil)
}

// NewProvisioningBundleWithSigningKey enables the documented target-generated
// CSR provisioning flow. leafPEM is the desired certificate template and does
// not require its private key: IOS XE generates the installed key. The supplied
// CA key must match the template leaf's issuer in the CA replacement bundle.
func NewProvisioningBundleWithSigningKey(
	certificateID, expectedServerName string,
	leafPEM, caBundlePEM, caKeyPEM []byte,
) (*ProvisioningBundle, error) {
	return newProvisioningBundle(certificateID, expectedServerName, leafPEM, nil, caBundlePEM, caKeyPEM)
}

func newProvisioningBundle(
	certificateID, expectedServerName string,
	leafPEM, privateKeyPEM, caBundlePEM, caKeyPEM []byte,
) (*ProvisioningBundle, error) {
	if !provisioningCertificateIDPattern.MatchString(certificateID) {
		return nil, fmt.Errorf("gnoi provisioning certificate ID must start with an alphanumeric and contain 1-64 characters from [A-Za-z0-9_.-]")
	}
	expectedServerName = strings.TrimSpace(expectedServerName)
	if expectedServerName == "" {
		return nil, fmt.Errorf("gnoi provisioning expected server name is required")
	}
	if caKeyPEM != nil && len(bytes.TrimSpace(caKeyPEM)) == 0 {
		return nil, fmt.Errorf("gnoi provisioning: ca.key is present but empty")
	}
	targetGeneratedKey := len(bytes.TrimSpace(caKeyPEM)) > 0

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

	var normalizedPrivateKey, normalizedPublicKey []byte
	if !targetGeneratedKey {
		leafPublicKey, ok := leaf.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("gnoi provisioning: leaf certificate public key must be RSA")
		}
		if leafPublicKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
			return nil, fmt.Errorf("gnoi provisioning: leaf certificate RSA public key is %d bits; at least %d bits are required", leafPublicKey.N.BitLen(), iosXEProvisioningRSAKeyBits)
		}
		privateKey, encodedPrivateKey, err := parseRSAPrivateKeyPEM(privateKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("gnoi provisioning: %w", err)
		}
		if privateKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
			return nil, fmt.Errorf("gnoi provisioning: RSA private key is %d bits; at least %d bits are required", privateKey.N.BitLen(), iosXEProvisioningRSAKeyBits)
		}
		if leafPublicKey.E != privateKey.PublicKey.E || leafPublicKey.N.Cmp(privateKey.PublicKey.N) != 0 {
			return nil, fmt.Errorf("gnoi provisioning: leaf certificate and private key do not match")
		}
		publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("gnoi provisioning: encode RSA public key: %w", err)
		}
		normalizedPrivateKey = encodedPrivateKey
		normalizedPublicKey = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	}

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
	verifiedChain := verifiedChains[0]
	if len(verifiedChain) < 2 {
		return nil, fmt.Errorf("gnoi provisioning: leaf verification returned no issuer")
	}
	verifiedIssuer := verifiedChain[1]
	var clientTrustPEM []byte
	for _, ca := range verifiedChain[1:] {
		clientTrustPEM = append(clientTrustPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...)
	}

	leafTemplate := *leaf
	leafTemplate.Subject = leaf.Subject
	if strings.TrimSpace(leafTemplate.Subject.CommonName) == "" {
		leafTemplate.Subject.CommonName = expectedServerName
		leafTemplate.RawSubject = nil
	}

	var signingKey *rsa.PrivateKey
	var signingCert *x509.Certificate
	var csrParams *certpb.CSRParams
	if targetGeneratedKey {
		parsedKey, _, err := parseRSAPrivateKeyPEM(caKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("gnoi provisioning: parse CA signing key: %w", err)
		}
		if parsedKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
			return nil, fmt.Errorf("gnoi provisioning: CA signing key is %d bits; at least %d bits are required", parsedKey.N.BitLen(), iosXEProvisioningRSAKeyBits)
		}
		issuerPublicKey, ok := verifiedIssuer.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("gnoi provisioning: tls.crt issuer public key must be RSA when ca.key is used")
		}
		matchingCAKey := rsaPublicKeysEqual(issuerPublicKey, &parsedKey.PublicKey)
		matchingOtherCA := false
		for _, ca := range caCerts {
			caPublicKey, ok := ca.PublicKey.(*rsa.PublicKey)
			if ok && rsaPublicKeysEqual(caPublicKey, &parsedKey.PublicKey) && !bytes.Equal(ca.Raw, verifiedIssuer.Raw) {
				matchingOtherCA = true
				break
			}
		}
		if !matchingCAKey {
			if matchingOtherCA {
				return nil, fmt.Errorf("gnoi provisioning: CA signing key must match the ca.crt certificate on tls.crt's verified issuer chain")
			}
			return nil, fmt.Errorf("gnoi provisioning: CA signing key does not match tls.crt's verified issuer in ca.crt")
		}
		signingCert = verifiedIssuer
		csrParams, err = iosXECSRParams(expectedServerName, &leafTemplate)
		if err != nil {
			return nil, fmt.Errorf("gnoi provisioning: %w", err)
		}
		// Match the signed certificate subject to the scalar subject requested
		// from IOS XE. The template remains authoritative, but extra subject
		// values that the v0.8 CSR request cannot express must not appear only
		// in the returned certificate.
		leafTemplate.Subject = pkix.Name{
			CommonName:         csrParams.CommonName,
			Country:            []string{csrParams.Country},
			Province:           []string{csrParams.State},
			Organization:       []string{csrParams.Organization},
			OrganizationalUnit: []string{csrParams.OrganizationalUnit},
		}
		leafTemplate.RawSubject = nil
		signingKey = parsedKey
	}

	return &ProvisioningBundle{
		certificateID:      certificateID,
		expectedServerName: expectedServerName,
		leafPEM:            normalizedLeaf,
		privateKeyPEM:      normalizedPrivateKey,
		publicKeyPEM:       normalizedPublicKey,
		caPEM:              normalizedCAs,
		clientTrustCAPEM:   clientTrustPEM,
		leafFingerprint:    sha256.Sum256(leaf.Raw),
		leafTemplate:       &leafTemplate,
		csrParams:          csrParams,
		signingCert:        signingCert,
		signingKey:         signingKey,
	}, nil
}

// iosXECSRParams builds the complete profile used by Google's reference gNXI
// client and Cisco's IOS XE examples. IOS XE 17.18 rejects the otherwise-valid
// sparse protobuf (CN plus key parameters) with INVALID_ARGUMENT. The locally
// validated leaf is the policy template; no arbitrary identity values are
// invented when required subject fields are absent.
func iosXECSRParams(expectedServerName string, leaf *x509.Certificate) (*certpb.CSRParams, error) {
	params := &certpb.CSRParams{
		Type:               certpb.CertificateType_CT_X509,
		MinKeySize:         iosXEProvisioningRSAKeyBits,
		KeyType:            certpb.KeyType_KT_RSA,
		CommonName:         strings.TrimSpace(leaf.Subject.CommonName),
		Country:            firstTrimmed(leaf.Subject.Country),
		State:              firstTrimmed(leaf.Subject.Province),
		Organization:       firstTrimmed(leaf.Subject.Organization),
		OrganizationalUnit: firstTrimmed(leaf.Subject.OrganizationalUnit),
	}
	if ip := net.ParseIP(expectedServerName); ip != nil {
		params.IpAddress = ip.String()
	} else if len(leaf.IPAddresses) > 0 {
		params.IpAddress = leaf.IPAddresses[0].String()
	}

	var missing []string
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "common name", value: params.CommonName},
		{name: "country", value: params.Country},
		{name: "state/province", value: params.State},
		{name: "organization", value: params.Organization},
		{name: "organizational unit", value: params.OrganizationalUnit},
		{name: "IP address", value: params.IpAddress},
	} {
		if field.value == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"IOS XE target-generated CSR requires %s; set the subject fields and an IP SAN in tls.crt (spec.address supplies the IP when it is an IP literal)",
			strings.Join(missing, ", "),
		)
	}
	if len(params.Country) != 2 {
		return nil, fmt.Errorf("IOS XE target-generated CSR country from tls.crt must be a two-letter code")
	}
	return params, nil
}

func firstTrimmed(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func rsaPublicKeysEqual(left, right *rsa.PublicKey) bool {
	return left != nil && right != nil && left.E == right.E && left.N.Cmp(right.N) == 0
}

// ProvisioningCertificateState compares the requested identity with the
// certificate already occupying its ID on the target. External-key installs
// use an exact DER fingerprint. A target-generated key necessarily produces a
// different certificate, so that mode matches the signed certificate profile
// and issuer instead.
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
	if bundle.usesTargetGeneratedKey() {
		if err := bundle.validateTargetGeneratedCertificate(installed); err != nil {
			return "", bundle.installedConflict(installedFingerprint, err)
		}
		return ProvisioningCertificateMatching, nil
	}
	if installedFingerprint != bundle.leafFingerprint {
		return "", &ErrCertificateIDConflict{
			CertificateID:        bundle.certificateID,
			ExpectedFingerprint:  hex.EncodeToString(bundle.leafFingerprint[:]),
			InstalledFingerprint: hex.EncodeToString(installedFingerprint[:]),
		}
	}
	return ProvisioningCertificateMatching, nil
}

func (b *ProvisioningBundle) usesTargetGeneratedKey() bool {
	return b != nil && b.signingCert != nil && b.signingKey != nil && b.csrParams != nil
}

func (b *ProvisioningBundle) validateTargetGeneratedCertificate(installed *x509.Certificate) error {
	if installed.IsCA {
		return fmt.Errorf("installed target-generated certificate is a CA")
	}
	if err := installed.CheckSignatureFrom(b.signingCert); err != nil {
		return fmt.Errorf("installed target-generated certificate was not issued by configured tls.crt issuer: %w", err)
	}
	if !equalSubjects(installed.Subject, b.leafTemplate.Subject) {
		return fmt.Errorf("installed target-generated certificate subject does not match tls.crt template")
	}
	if !installed.NotBefore.Equal(b.leafTemplate.NotBefore) || !installed.NotAfter.Equal(b.leafTemplate.NotAfter) {
		return fmt.Errorf("installed target-generated certificate validity does not match tls.crt template")
	}
	if installed.KeyUsage != b.leafTemplate.KeyUsage || !slices.Equal(installed.ExtKeyUsage, b.leafTemplate.ExtKeyUsage) {
		return fmt.Errorf("installed target-generated certificate usages do not match tls.crt template")
	}
	if !slices.Equal(installed.DNSNames, b.leafTemplate.DNSNames) ||
		!equalIPAddresses(installed.IPAddresses, b.leafTemplate.IPAddresses) ||
		!slices.Equal(installed.EmailAddresses, b.leafTemplate.EmailAddresses) ||
		!equalURIs(installed.URIs, b.leafTemplate.URIs) {
		return fmt.Errorf("installed target-generated certificate subject alternative names do not match tls.crt template")
	}
	if err := installed.VerifyHostname(b.expectedServerName); err != nil {
		return fmt.Errorf("installed target-generated certificate is not valid for %q: %w", b.expectedServerName, err)
	}
	publicKey, ok := installed.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
		return fmt.Errorf("installed target-generated certificate does not contain an RSA key of at least %d bits", iosXEProvisioningRSAKeyBits)
	}
	now := time.Now()
	if now.Before(installed.NotBefore) || now.After(installed.NotAfter) {
		return fmt.Errorf("installed target-generated certificate is not currently valid")
	}
	return nil
}

func equalIPAddresses(left, right []net.IP) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !left[i].Equal(right[i]) {
			return false
		}
	}
	return true
}

func equalSubjects(left, right pkix.Name) bool {
	return left.CommonName == right.CommonName &&
		left.SerialNumber == right.SerialNumber &&
		slices.Equal(left.Country, right.Country) &&
		slices.Equal(left.Organization, right.Organization) &&
		slices.Equal(left.OrganizationalUnit, right.OrganizationalUnit) &&
		slices.Equal(left.Locality, right.Locality) &&
		slices.Equal(left.Province, right.Province) &&
		slices.Equal(left.StreetAddress, right.StreetAddress) &&
		slices.Equal(left.PostalCode, right.PostalCode)
}

func equalURIs(left, right []*url.URL) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] == nil || right[i] == nil {
			if left[i] != right[i] {
				return false
			}
			continue
		}
		if left[i].String() != right[i].String() {
			return false
		}
	}
	return true
}

// InstallProvisioningCertificate installs the configured gNXI identity. IOS XE
// uses its target-generated CSR workflow when a signing key is configured; the
// external-key form remains available for targets that support it. A transport
// loss after LoadCertificate is sent is indeterminate and must be reconciled by
// certificate ID on a fresh connection.
func (c *Client) InstallProvisioningCertificate(ctx context.Context, bundle *ProvisioningBundle) error {
	if bundle == nil {
		return fmt.Errorf("gnoi Cert.Install: nil provisioning bundle")
	}
	if err := c.cap.ensureSupported(ServiceCert); err != nil {
		return err
	}
	if bundle.usesTargetGeneratedKey() {
		return c.installProvisioningCertificateFromCSR(ctx, bundle)
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
		return &ErrCertificateInstallIndeterminate{CertificateID: bundle.certificateID, Cause: err}
	}
	// LoadCertificateResponse is the protocol acknowledgement. The reference
	// client returns at this point; requiring EOF first makes IOS XE treat the
	// bidirectional stream as broken and roll back the install.
	c.cap.Observe(ServiceCert, nil)
	return nil
}

func (c *Client) installProvisioningCertificateFromCSR(ctx context.Context, bundle *ProvisioningBundle) error {
	// Follow the protocol's Install exchange directly. CanGenerateCSR is a
	// separate diagnostic RPC and some IOS XE builds reject it even though the
	// GenerateCSR arm of Install is supported.
	stream, err := c.cert.Install(c.authCtx(ctx))
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return fmt.Errorf("gnoi Cert.Install open: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	generateRequest := &certpb.InstallCertificateRequest{
		InstallRequest: &certpb.InstallCertificateRequest_GenerateCsr{
			GenerateCsr: &certpb.GenerateCSRRequest{
				CertificateId: bundle.certificateID,
				CsrParams:     bundle.csrParams,
			},
		},
	}
	if err := stream.Send(generateRequest); err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "send GenerateCSRRequest", err)
	}
	csrResponse, err := stream.Recv()
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "receive GenerateCSRResponse", err)
	}
	generatedCSR := csrResponse.GetGeneratedCsr()
	if generatedCSR == nil || len(generatedCSR.GetCsr().GetCsr()) == 0 {
		err := fmt.Errorf("gnoi Cert.Install: expected GenerateCSRResponse with CSR")
		c.cap.Observe(ServiceCert, err)
		return err
	}
	if generatedCSR.GetCsr().GetType() != certpb.CertificateType_CT_X509 {
		err := fmt.Errorf("gnoi Cert.Install: GenerateCSRResponse has certificate type %s; want CT_X509", generatedCSR.GetCsr().GetType())
		c.cap.Observe(ServiceCert, err)
		return err
	}
	leafPEM, err := bundle.signCSR(generatedCSR.GetCsr().GetCsr())
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return fmt.Errorf("gnoi Cert.Install sign CSR: %w", err)
	}

	caCertificates := make([]*certpb.Certificate, 0, len(bundle.caPEM))
	for _, ca := range bundle.caPEM {
		caCertificates = append(caCertificates, &certpb.Certificate{
			Type:        certpb.CertificateType_CT_X509,
			Certificate: bytes.Clone(ca),
		})
	}
	loadRequest := &certpb.InstallCertificateRequest{
		InstallRequest: &certpb.InstallCertificateRequest_LoadCertificate{
			LoadCertificate: &certpb.LoadCertificateRequest{
				Certificate: &certpb.Certificate{
					Type:        certpb.CertificateType_CT_X509,
					Certificate: leafPEM,
				},
				CaCertificates: caCertificates,
			},
		},
	}
	if err := stream.Send(loadRequest); err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "send LoadCertificateRequest", err)
	}
	loadResponse, err := stream.Recv()
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return wrapCertificateInstallError(bundle.certificateID, "receive LoadCertificateResponse", err)
	}
	if loadResponse == nil || loadResponse.GetLoadCertificate() == nil {
		err := fmt.Errorf("gnoi Cert.Install: expected LoadCertificateResponse")
		c.cap.Observe(ServiceCert, err)
		return &ErrCertificateInstallIndeterminate{CertificateID: bundle.certificateID, Cause: err}
	}
	c.cap.Observe(ServiceCert, nil)
	return nil
}

func (b *ProvisioningBundle) signCSR(rawCSR []byte) ([]byte, error) {
	csrBlock, rest := pem.Decode(bytes.TrimSpace(rawCSR))
	if csrBlock == nil {
		return nil, fmt.Errorf("CSR is not PEM encoded")
	}
	if (csrBlock.Type != "CERTIFICATE REQUEST" && csrBlock.Type != "NEW CERTIFICATE REQUEST") || len(csrBlock.Headers) != 0 {
		return nil, fmt.Errorf("CSR has PEM block type %q; want CERTIFICATE REQUEST", csrBlock.Type)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("CSR must contain exactly one PEM block")
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("verify CSR signature: %w", err)
	}
	publicKey, ok := csr.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("CSR public key must be RSA")
	}
	if publicKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
		return nil, fmt.Errorf("CSR RSA public key is %d bits; at least %d bits are required", publicKey.N.BitLen(), iosXEProvisioningRSAKeyBits)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("encode CSR public key: %w", err)
	}
	subjectKeyID := sha256.Sum256(publicDER)

	// The CSR proves possession of IOS XE's private key; the locally validated
	// tls.crt remains authoritative for the certificate subject, SANs, validity,
	// and usages. Some conforming targets omit requested subject or IP fields from
	// the encoded CSR, so do not require them to be echoed. In particular, gNOI
	// v0.8 cannot request a DNS SAN, and copying arbitrary CSR identity fields
	// could make the post-provisioning TLS connection unverifiable.
	template := *b.leafTemplate
	serialLimit := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	serial.Add(serial, big.NewInt(1))
	template.SerialNumber = serial
	template.Raw = nil
	template.RawTBSCertificate = nil
	template.RawSubjectPublicKeyInfo = nil
	template.RawSubject = nil
	template.RawIssuer = nil
	template.Signature = nil
	template.SignatureAlgorithm = x509.UnknownSignatureAlgorithm
	template.PublicKeyAlgorithm = x509.RSA
	template.PublicKey = publicKey
	template.SubjectKeyId = bytes.Clone(subjectKeyID[:])
	template.AuthorityKeyId = bytes.Clone(b.signingCert.SubjectKeyId)
	der, err := x509.CreateCertificate(rand.Reader, &template, b.signingCert, publicKey, b.signingKey)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	issued, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}
	if err := b.validateTargetGeneratedCertificate(issued); err != nil {
		return nil, fmt.Errorf("validate issued certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func (b *ProvisioningBundle) conflictError(detail string, cause error) error {
	conflictCause := errors.New(detail)
	if cause != nil {
		conflictCause = fmt.Errorf("%s: %w", detail, cause)
	}
	expectedFingerprint := ""
	if !b.usesTargetGeneratedKey() {
		expectedFingerprint = hex.EncodeToString(b.leafFingerprint[:])
	}
	return &ErrCertificateIDConflict{
		CertificateID:       b.certificateID,
		ExpectedFingerprint: expectedFingerprint,
		Cause:               conflictCause,
	}
}

func (b *ProvisioningBundle) installedConflict(installedFingerprint [sha256.Size]byte, cause error) error {
	expectedFingerprint := ""
	if !b.usesTargetGeneratedKey() {
		expectedFingerprint = hex.EncodeToString(b.leafFingerprint[:])
	}
	return &ErrCertificateIDConflict{
		CertificateID:        b.certificateID,
		ExpectedFingerprint:  expectedFingerprint,
		InstalledFingerprint: hex.EncodeToString(installedFingerprint[:]),
		Cause:                cause,
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
	case codes.Aborted, codes.AlreadyExists, codes.Canceled, codes.DataLoss, codes.DeadlineExceeded, codes.Internal, codes.Unavailable, codes.Unknown:
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
