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
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
// IOS XE's target-generated gNOI CertificateManagement.Install flow. Its
// formatter redacts all fields so formatting cannot expose signing material.
type ProvisioningBundle struct {
	certificateID            string
	publicMaterialSHA256     string
	expectedServerName       string
	caPEM                    [][]byte
	clientTrustRoot          *x509.Certificate
	clientTrustIntermediates []*x509.Certificate
	leafTemplate             *x509.Certificate
	expectedSubjectDER       []byte
	csrParams                *certpb.CSRParams
	signingCert              *x509.Certificate
}

// CertificateSigner signs one target-generated CSR and returns exactly one
// PEM-encoded X.509 certificate. Implementations may keep the issuer key in a
// local Secret, KMS/HSM, or external CA. Because the method receives only the
// CSR, an external implementation must already be bound to the same issuance
// profile and issuer as its ProvisioningBundle. InstallProvisioningCertificate
// treats every implementation as untrusted: it validates the CSR before
// calling the signer and validates the returned certificate against both that
// CSR and the configured bundle before sending it to IOS XE.
type CertificateSigner interface {
	SignCSR(context.Context, []byte) ([]byte, error)
}

type localCertificateSigner struct {
	bundle *ProvisioningBundle
	mu     sync.Mutex
	key    *rsa.PrivateKey
}

// Format prevents fmt from rendering the locally held private key.
func (*localCertificateSigner) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("CertificateSigner{REDACTED}"))
}

func (s *localCertificateSigner) SignCSR(ctx context.Context, rawCSR []byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("local certificate signer is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	key := s.key
	s.key = nil
	s.mu.Unlock()
	if key == nil {
		return nil, fmt.Errorf("local certificate signer may be used only once per process")
	}
	return s.bundle.signCSR(rawCSR, key)
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

// PublicMaterialSHA256 returns the lowercase SHA-256 digest of the exact
// tls.crt bytes followed immediately by the exact ca.crt bytes supplied when
// the bundle was constructed. It is safe to expose in action intent/status.
func (b *ProvisioningBundle) PublicMaterialSHA256() string {
	if b == nil {
		return ""
	}
	return b.publicMaterialSHA256
}

// ConfigureClientTLS adds the provisioned identity's verified chain to one
// gNOI-only TLS config. bootstrapPEM may contain the exact certificate served
// by IOS XE before provisioning; it is accepted only as a leaf pin and never
// promoted to a CA. Normal CA and hostname verification is always attempted
// first and permanently disables the pin fallback after its first success.
func (b *ProvisioningBundle) ConfigureClientTLS(tlsCfg *tls.Config, bootstrapPEM []byte) error {
	if b == nil {
		return fmt.Errorf("gnoi provisioning: nil bundle")
	}
	if tlsCfg == nil {
		return fmt.Errorf("gnoi provisioning: nil TLS config")
	}
	if tlsCfg.InsecureSkipVerify {
		return fmt.Errorf("gnoi provisioning: verified TLS is required; insecureSkipVerify cannot be used")
	}

	var bootstrap *x509.Certificate
	if bootstrapPEM != nil {
		var err error
		bootstrap, err = parseSingleCertificatePEM(bootstrapPEM, "bootstrap certificate")
		if err != nil {
			return fmt.Errorf("gnoi provisioning: %w", err)
		}
		now := time.Now()
		if now.Before(bootstrap.NotBefore) || now.After(bootstrap.NotAfter) {
			return fmt.Errorf("gnoi provisioning: bootstrap certificate is not currently valid")
		}
	}

	roots := tlsCfg.RootCAs
	if roots == nil {
		roots, _ = x509.SystemCertPool()
		if roots == nil {
			roots = x509.NewCertPool()
		}
	} else {
		roots = roots.Clone()
	}
	roots.AddCert(b.clientTrustRoot)
	tlsCfg.RootCAs = roots
	tlsCfg.ServerName = b.expectedServerName

	knownIntermediates := x509.NewCertPool()
	for _, certificate := range b.clientTrustIntermediates {
		knownIntermediates.AddCert(certificate)
	}
	previousVerifyConnection := tlsCfg.VerifyConnection
	var caVerified atomic.Bool
	tlsCfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return fmt.Errorf("gnoi TLS: server presented no certificate")
		}
		intermediates := knownIntermediates.Clone()
		for _, certificate := range state.PeerCertificates[1:] {
			intermediates.AddCert(certificate)
		}
		chains, verifyErr := state.PeerCertificates[0].Verify(x509.VerifyOptions{
			DNSName:       b.expectedServerName,
			Roots:         roots,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if verifyErr == nil {
			state.VerifiedChains = chains
			if previousVerifyConnection != nil {
				if err := previousVerifyConnection(state); err != nil {
					return err
				}
			}
			caVerified.Store(true)
			return nil
		}
		if bootstrap != nil && !caVerified.Load() && bytes.Equal(state.PeerCertificates[0].Raw, bootstrap.Raw) {
			now := time.Now()
			if now.Before(bootstrap.NotBefore) || now.After(bootstrap.NotAfter) {
				return fmt.Errorf("gnoi TLS: pinned bootstrap certificate is not currently valid")
			}
			if previousVerifyConnection != nil {
				return previousVerifyConnection(state)
			}
			return nil
		}
		return fmt.Errorf("gnoi TLS: verify server certificate for %q: %w", b.expectedServerName, verifyErr)
	}
	// #nosec G402 -- VerifyConnection above performs full CA/hostname checks or
	// an exact, validity-checked bootstrap leaf pin before accepting a peer.
	tlsCfg.InsecureSkipVerify = true
	return nil
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
	if e.Cause == nil {
		return fmt.Sprintf("gnoi Cert.Install certificate ID %q outcome is indeterminate", e.CertificateID)
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

// NewProvisioningBundle validates and normalizes the public certificate
// material for IOS XE's target-generated CSR flow. leafPEM is the desired
// device certificate profile and caBundlePEM is the complete replacement
// target CA bundle. Private signing material is supplied separately through a
// CertificateSigner. The device identity private key stays on IOS XE.
func NewProvisioningBundle(
	certificateID, expectedServerName string,
	leafPEM, caBundlePEM []byte,
) (*ProvisioningBundle, error) {
	if !provisioningCertificateIDPattern.MatchString(certificateID) {
		return nil, fmt.Errorf("gnoi provisioning certificate ID must start with an alphanumeric and contain 1-64 characters from [A-Za-z0-9_.-]")
	}
	expectedServerName = strings.TrimSpace(expectedServerName)
	if expectedServerName == "" {
		return nil, fmt.Errorf("gnoi provisioning expected server name is required")
	}
	leaf, err := parseSingleCertificatePEM(leafPEM, "leaf certificate")
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
	if len(verifiedChain) < 3 {
		return nil, fmt.Errorf("gnoi provisioning: tls.crt must be issued by a dedicated intermediate CA, not directly by a root CA")
	}
	verifiedIssuer := verifiedChain[1]
	verifiedRoot := verifiedChain[len(verifiedChain)-1]
	verifiedIntermediates := slices.Clone(verifiedChain[1 : len(verifiedChain)-1])

	profile := *leaf
	if strings.TrimSpace(profile.Subject.CommonName) == "" {
		profile.Subject.CommonName = expectedServerName
	}

	csrParams, err := iosXECSRParams(expectedServerName, &profile)
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: %w", err)
	}
	// gNOI v0.8 can request only scalar subject fields. Use that same
	// normalized subject in the signed certificate so reconciliation has one
	// unambiguous profile to compare after IOS XE restarts.
	leafTemplate := &x509.Certificate{
		Subject: pkix.Name{
			CommonName:         csrParams.CommonName,
			Country:            []string{csrParams.Country},
			Province:           []string{csrParams.State},
			Organization:       []string{csrParams.Organization},
			OrganizationalUnit: []string{csrParams.OrganizationalUnit},
		},
		NotBefore:             leaf.NotBefore,
		NotAfter:              leaf.NotAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              slices.Clone(leaf.DNSNames),
		IPAddresses:           slices.Clone(leaf.IPAddresses),
	}
	expectedSubjectDER, err := asn1.Marshal(leafTemplate.Subject.ToRDNSequence())
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: encode certificate subject: %w", err)
	}

	publicMaterialDigest := sha256.New()
	_, _ = publicMaterialDigest.Write(leafPEM)
	_, _ = publicMaterialDigest.Write(caBundlePEM)

	return &ProvisioningBundle{
		certificateID:            certificateID,
		publicMaterialSHA256:     fmt.Sprintf("%x", publicMaterialDigest.Sum(nil)),
		expectedServerName:       expectedServerName,
		caPEM:                    normalizedCAs,
		clientTrustRoot:          verifiedRoot,
		clientTrustIntermediates: verifiedIntermediates,
		leafTemplate:             leafTemplate,
		expectedSubjectDER:       expectedSubjectDER,
		csrParams:                csrParams,
		signingCert:              verifiedIssuer,
	}, nil
}

// NewLocalCertificateSigner constructs the transitional PEM-backed signer for
// a provisioning bundle. The key must belong to the bundle's dedicated
// intermediate issuer and is consumed after one signing attempt. Keeping this
// constructor separate from NewProvisioningBundle prevents read-only trust
// configuration from retaining private signing material.
func NewLocalCertificateSigner(bundle *ProvisioningBundle, caKeyPEM []byte) (CertificateSigner, error) {
	if bundle == nil {
		return nil, fmt.Errorf("gnoi provisioning: nil bundle")
	}
	parsedKey, err := parseRSAPrivateKeyPEM(caKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("gnoi provisioning: parse CA signing key: %w", err)
	}
	if parsedKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
		return nil, fmt.Errorf("gnoi provisioning: CA signing key is %d bits; at least %d bits are required", parsedKey.N.BitLen(), iosXEProvisioningRSAKeyBits)
	}
	issuerPublicKey, ok := bundle.signingCert.PublicKey.(*rsa.PublicKey)
	if !ok || !rsaPublicKeysEqual(issuerPublicKey, &parsedKey.PublicKey) {
		return nil, fmt.Errorf("gnoi provisioning: ca.key must match tls.crt's verified intermediate issuer in ca.crt")
	}
	for _, caPEM := range bundle.caPEM {
		ca, err := parseSingleCertificatePEM(caPEM, "CA bundle certificate")
		if err != nil {
			return nil, fmt.Errorf("gnoi provisioning: validate CA bundle certificate: %w", err)
		}
		rootPublicKey, ok := ca.PublicKey.(*rsa.PublicKey)
		if bytes.Equal(ca.RawSubject, ca.RawIssuer) && ca.CheckSignatureFrom(ca) == nil && ok && rsaPublicKeysEqual(rootPublicKey, issuerPublicKey) {
			return nil, fmt.Errorf("gnoi provisioning: ca.key must belong to a dedicated intermediate CA, not a root CA")
		}
	}
	return &localCertificateSigner{bundle: bundle, key: parsedKey}, nil
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

// ProvisioningCertificateInstalled reports whether the target already contains
// the requested identity. A different certificate at the same create-only ID
// fails closed because gNOI Install is create-only and must not overwrite it.
func (c *Client) ProvisioningCertificateInstalled(ctx context.Context, bundle *ProvisioningBundle) (bool, error) {
	if bundle == nil {
		return false, fmt.Errorf("gnoi Cert.GetCertificates: nil provisioning bundle")
	}
	certificates, err := c.GetCertificates(ctx)
	if err != nil {
		return false, err
	}

	var match *CertificateInfo
	for i := range certificates {
		if certificates[i].CertificateID != bundle.certificateID {
			continue
		}
		if match != nil {
			return false, bundle.conflictError("device returned duplicate records for the certificate ID", nil)
		}
		match = &certificates[i]
	}
	if match == nil {
		return false, nil
	}
	if match.Type != "" && match.Type != certpb.CertificateType_CT_UNKNOWN.String() && match.Type != certpb.CertificateType_CT_X509.String() {
		return false, bundle.conflictError(fmt.Sprintf("installed certificate has type %q", match.Type), nil)
	}
	installed, err := parseSingleCertificatePEM(match.Certificate, "installed certificate")
	if err != nil {
		return false, bundle.conflictError("installed certificate cannot be parsed", err)
	}
	if err := bundle.validateTargetGeneratedCertificate(installed); err != nil {
		return false, bundle.conflictError("installed certificate does not match the requested profile", err)
	}
	return true, nil
}

func (b *ProvisioningBundle) validateTargetGeneratedCertificate(installed *x509.Certificate) error {
	if installed.IsCA {
		return fmt.Errorf("installed target-generated certificate is a CA")
	}
	if err := installed.CheckSignatureFrom(b.signingCert); err != nil {
		return fmt.Errorf("installed target-generated certificate was not issued by configured tls.crt issuer: %w", err)
	}
	if !bytes.Equal(installed.RawIssuer, b.signingCert.RawSubject) {
		return fmt.Errorf("installed target-generated certificate issuer does not match configured tls.crt issuer")
	}
	if !bytes.Equal(installed.RawSubject, b.expectedSubjectDER) {
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
		len(installed.EmailAddresses) != 0 || len(installed.URIs) != 0 {
		return fmt.Errorf("installed target-generated certificate subject alternative names do not match tls.crt template")
	}
	publicKey, ok := installed.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() < iosXEProvisioningRSAKeyBits {
		return fmt.Errorf("installed target-generated certificate does not contain an RSA key of at least %d bits", iosXEProvisioningRSAKeyBits)
	}
	roots := x509.NewCertPool()
	roots.AddCert(b.clientTrustRoot)
	intermediates := x509.NewCertPool()
	for _, certificate := range b.clientTrustIntermediates {
		intermediates.AddCert(certificate)
	}
	if _, err := installed.Verify(x509.VerifyOptions{
		DNSName:       b.expectedServerName,
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime:   time.Now(),
	}); err != nil {
		return fmt.Errorf("installed target-generated certificate does not form a valid server chain for %q: %w", b.expectedServerName, err)
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

// InstallProvisioningCertificate installs the configured gNXI identity with
// IOS XE's target-generated CSR workflow. A transport loss after either stream
// request is indeterminate and must be reconciled by certificate ID on a fresh
// connection.
func (c *Client) InstallProvisioningCertificate(ctx context.Context, bundle *ProvisioningBundle, signer CertificateSigner) error {
	if bundle == nil {
		return fmt.Errorf("gnoi Cert.Install: nil provisioning bundle")
	}
	if err := c.cap.ensureSupported(ServiceCert); err != nil {
		return err
	}
	if signer == nil {
		return fmt.Errorf("gnoi Cert.Install: certificate signer is required for a new certificate")
	}
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
	if got := generatedCSR.GetCsr().GetType(); got != certpb.CertificateType_CT_UNKNOWN && got != certpb.CertificateType_CT_X509 {
		err := fmt.Errorf("gnoi Cert.Install: GenerateCSRResponse has certificate type %s; want CT_X509", generatedCSR.GetCsr().GetType())
		c.cap.Observe(ServiceCert, err)
		return err
	}
	rawCSR := generatedCSR.GetCsr().GetCsr()
	csrPublicKey, err := parseProvisioningCSR(rawCSR)
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return fmt.Errorf("gnoi Cert.Install validate CSR: %w", err)
	}
	leafPEM, err := signer.SignCSR(ctx, bytes.Clone(rawCSR))
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return fmt.Errorf("gnoi Cert.Install sign CSR: %w", err)
	}
	leafPEM, err = bundle.validateSignerCertificate(leafPEM, csrPublicKey)
	if err != nil {
		c.cap.Observe(ServiceCert, err)
		return fmt.Errorf("gnoi Cert.Install validate signer certificate: %w", err)
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

func (b *ProvisioningBundle) signCSR(rawCSR []byte, signingKey *rsa.PrivateKey) ([]byte, error) {
	if signingKey == nil {
		return nil, fmt.Errorf("CA signing key is unavailable")
	}
	publicKey, err := parseProvisioningCSR(rawCSR)
	if err != nil {
		return nil, err
	}
	return b.issueCertificate(publicKey, signingKey)
}

func parseProvisioningCSR(rawCSR []byte) (*rsa.PublicKey, error) {
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
	return publicKey, nil
}

func (b *ProvisioningBundle) issueCertificate(publicKey *rsa.PublicKey, signingKey *rsa.PrivateKey) ([]byte, error) {
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("encode CSR public key: %w", err)
	}
	subjectKeyID := sha256.Sum256(publicDER)

	// The CSR proves possession of IOS XE's private key; the locally normalized
	// tls.crt profile remains authoritative for the certificate subject, SANs,
	// validity, and usages. Some conforming targets omit requested fields from
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
	template.RawSubject = nil
	template.SignatureAlgorithm = x509.UnknownSignatureAlgorithm
	template.SubjectKeyId = bytes.Clone(subjectKeyID[:])
	template.AuthorityKeyId = bytes.Clone(b.signingCert.SubjectKeyId)
	der, err := x509.CreateCertificate(rand.Reader, &template, b.signingCert, publicKey, signingKey)
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

func (b *ProvisioningBundle) validateSignerCertificate(certificatePEM []byte, csrPublicKey *rsa.PublicKey) ([]byte, error) {
	issued, err := parseSingleCertificatePEM(certificatePEM, "signer certificate")
	if err != nil {
		return nil, err
	}
	issuedPublicKey, ok := issued.PublicKey.(*rsa.PublicKey)
	if !ok || !rsaPublicKeysEqual(issuedPublicKey, csrPublicKey) {
		return nil, fmt.Errorf("signer certificate public key does not match the target-generated CSR")
	}
	if err := b.validateTargetGeneratedCertificate(issued); err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issued.Raw}), nil
}

func (b *ProvisioningBundle) conflictError(detail string, cause error) error {
	if cause != nil {
		return fmt.Errorf(
			"gnoi Cert.Install certificate ID %q already contains a different certificate: %s: %w",
			b.certificateID,
			detail,
			cause,
		)
	}
	return fmt.Errorf(
		"gnoi Cert.Install certificate ID %q already contains a different certificate: %s",
		b.certificateID,
		detail,
	)
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

func parseSingleCertificatePEM(data []byte, label string) (*x509.Certificate, error) {
	certificates, _, err := parseCertificatePEM(data, label)
	if err != nil {
		return nil, err
	}
	if len(certificates) != 1 {
		return nil, fmt.Errorf("%s must contain exactly one PEM certificate", label)
	}
	return certificates[0], nil
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

func parseRSAPrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	trimmed := bytes.TrimSpace(data)
	if !bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
		return nil, fmt.Errorf("private key is not PEM encoded")
	}
	block, rest := pem.Decode(trimmed)
	if block == nil {
		return nil, fmt.Errorf("private key contains an invalid PEM block")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("private key must contain exactly one PEM block")
	}
	if x509.IsEncryptedPEMBlock(block) || strings.Contains(block.Type, "ENCRYPTED") {
		return nil, fmt.Errorf("encrypted private keys are not supported")
	}

	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 RSA private key: %w", err)
		}
		key = parsed
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS#8 private key must contain an RSA key")
		}
	default:
		return nil, fmt.Errorf("private key PEM block type %q is not supported; want RSA PRIVATE KEY or PRIVATE KEY", block.Type)
	}
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("invalid RSA private key: %w", err)
	}
	return key, nil
}
