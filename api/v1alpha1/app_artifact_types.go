// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// AppArtifactMode selects the application package trust policy. Verified requires an
// explicitly configured delivery/checksum runtime and durable installation journal.
// +kubebuilder:validation:Enum=Standard;Verified
type AppArtifactMode string

const (
	AppArtifactModeStandard           AppArtifactMode = "Standard"
	AppArtifactModeVerified           AppArtifactMode = "Verified"
	DefaultAppArtifactMaxPackageBytes int64           = 4 << 30
	MaxAppArtifactPackageBytes        int64           = 64 << 30
	MaxAppArtifactProducerPublicKeys                  = 8
	MaxAppArtifactPublicKeyBytes                      = 16384
)

// AppArtifactConfig is operator-owned trust configuration. Workload annotations
// cannot override it. Omission preserves Standard operation and adds no store,
// signature, credential, or resource requirements.
// +kubebuilder:validation:XValidation:rule="!has(self.mode) || self.mode != 'Verified' || (has(self.storeURL) && has(self.producerPublicKeys) && has(self.platform))",message="Verified requires storeURL, producerPublicKeys, and platform"
// +kubebuilder:validation:XValidation:rule="(has(self.mode) && self.mode == 'Verified') || (!has(self.storeURL) && !has(self.producerPublicKeys) && !has(self.platform) && !has(self.maxPackageBytes) && !has(self.delivery) && !has(self.sshHostKeySHA256))",message="artifact trust and package limits require mode Verified"
// +kubebuilder:validation:XValidation:rule="!has(self.delivery) || self.delivery != 'Copy' || has(self.sshHostKeySHA256)",message="Copy delivery requires a pinned SSH host key for native file verification"
// +kubebuilder:validation:XValidation:rule="!has(self.sshHostKeySHA256) || (has(self.delivery) && self.delivery == 'Copy')",message="sshHostKeySHA256 requires Copy delivery"
type AppArtifactConfig struct {
	// Mode defaults to Standard. Verified requires a supported device checksum and durable recovery runtime.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=Standard
	Mode AppArtifactMode `json:"mode,omitempty" mapstructure:"mode,omitempty"`

	// Delivery selects staging and file verification in Verified mode. GNOI is
	// the compatibility default. Copy reuses the RESTCONF copy RPC and performs
	// native SHA512 file verification over host-key-pinned SSH (port 22).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=GNOI;Copy
	Delivery string `json:"delivery,omitempty" mapstructure:"delivery,omitempty"`

	// SSHHostKeySHA256 pins the device SSH host key for Copy checksum queries.
	// Credentials come from the same device identity used by RESTCONF. No SCP
	// service, shell on the application, or private SSH key is required.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^SHA256:[A-Za-z0-9+/]{43}$`
	SSHHostKeySHA256 string `json:"sshHostKeySHA256,omitempty" mapstructure:"sshHostKeySHA256,omitempty"`

	// StoreURL is a canonical HTTPS origin (lowercase host and optional port), with at most a
	// trailing slash. Credentials, query parameters, fragments and base paths
	// are forbidden. The verifier uses system TLS trust; insecure TLS is not an
	// option. This setting grants no authority to the store to approve bytes.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https://[^/?#@]+/?$`
	StoreURL string `json:"storeURL,omitempty" mapstructure:"storeURL,omitempty"`

	// ProducerPublicKeys contains accepted producer PKIX PUBLIC KEY PEM blocks.
	// These are public trust anchors, never private signing keys. Each entry
	// contains exactly one RSA (2048, 3072 or 4096 bits), ECDSA, or Ed25519 public key.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=16384
	ProducerPublicKeys []string `json:"producerPublicKeys,omitempty" mapstructure:"producerPublicKeys,omitempty"`

	// Platform is the explicit Linux source platform accepted by this device.
	// Declaring a platform is not a claim of device/package compatibility.
	// +kubebuilder:validation:Optional
	Platform *AppArtifactPlatform `json:"platform,omitempty" mapstructure:"platform,omitempty"`

	// MaxPackageBytes bounds one artifact's exact byte size. Omitted in Verified
	// mode, it is 4 GiB. It is a safety limit, not a storage reservation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=68719476736
	MaxPackageBytes int64 `json:"maxPackageBytes,omitempty" mapstructure:"maxPackageBytes,omitempty"`
}

// AppArtifactPlatform identifies the selected OCI image platform.
type AppArtifactPlatform struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=linux
	OS string `json:"os" mapstructure:"os"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9_-]{0,31}$`
	Architecture string `json:"architecture" mapstructure:"architecture"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._-]{1,32}$`
	Variant string `json:"variant,omitempty" mapstructure:"variant,omitempty"`
}

// EffectiveMode does not allocate or mutate configuration for Standard mode.
func (c *AppArtifactConfig) EffectiveMode() AppArtifactMode {
	if c == nil || c.Mode == "" {
		return AppArtifactModeStandard
	}
	return c.Mode
}

// EffectiveMaxPackageBytes returns the per-artifact safety limit. Callers must
// validate configuration first; this default does not activate Verified mode.
func (c *AppArtifactConfig) EffectiveMaxPackageBytes() int64 {
	if c == nil || c.MaxPackageBytes == 0 {
		return DefaultAppArtifactMaxPackageBytes
	}
	return c.MaxPackageBytes
}

var (
	appArtifactArchitecturePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	appArtifactVariantPattern      = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,32}$`)
)

// Validate applies the same trust constraints at manager rendering and local
// worker startup. It performs no network access and reports no key contents.
func (c *AppArtifactConfig) Validate() error {
	if c == nil {
		return nil
	}
	switch c.EffectiveMode() {
	case AppArtifactModeStandard:
		if c.StoreURL != "" || len(c.ProducerPublicKeys) != 0 || c.Platform != nil || c.MaxPackageBytes != 0 || c.Delivery != "" || c.SSHHostKeySHA256 != "" {
			return fmt.Errorf("artifact trust and package limits require mode Verified")
		}
		return nil
	case AppArtifactModeVerified:
	default:
		return fmt.Errorf("mode must be Standard or Verified")
	}
	switch c.Delivery {
	case "", "GNOI":
		if c.SSHHostKeySHA256 != "" {
			return fmt.Errorf("sshHostKeySHA256 requires Copy delivery")
		}
	case "Copy":
		pin, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(c.SSHHostKeySHA256, "SHA256:"))
		if !strings.HasPrefix(c.SSHHostKeySHA256, "SHA256:") || err != nil || len(pin) != sha256.Size {
			return fmt.Errorf("Copy delivery requires a valid SSH SHA256 host key fingerprint")
		}
	default:
		return fmt.Errorf("delivery must be GNOI or Copy")
	}
	if err := validateAppArtifactStoreURL(c.StoreURL); err != nil {
		return err
	}
	if c.Platform == nil || c.Platform.OS != "linux" || !appArtifactArchitecturePattern.MatchString(c.Platform.Architecture) ||
		(c.Platform.Variant != "" && !appArtifactVariantPattern.MatchString(c.Platform.Variant)) {
		return fmt.Errorf("platform requires os linux and an explicit valid architecture and optional variant")
	}
	if c.MaxPackageBytes < 0 || c.MaxPackageBytes > MaxAppArtifactPackageBytes {
		return fmt.Errorf("maxPackageBytes must be between 1 and %d when set", MaxAppArtifactPackageBytes)
	}
	if len(c.ProducerPublicKeys) == 0 || len(c.ProducerPublicKeys) > MaxAppArtifactProducerPublicKeys {
		return fmt.Errorf("producerPublicKeys must contain between 1 and %d keys", MaxAppArtifactProducerPublicKeys)
	}
	seen := make(map[[sha256.Size]byte]bool, len(c.ProducerPublicKeys))
	for i, raw := range c.ProducerPublicKeys {
		if len(raw) > MaxAppArtifactPublicKeyBytes || !strings.HasPrefix(strings.TrimSpace(raw), "-----BEGIN PUBLIC KEY-----") {
			return fmt.Errorf("producerPublicKeys[%d] must be one bounded PKIX PUBLIC KEY PEM block", i)
		}
		block, rest := pem.Decode([]byte(raw))
		if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return fmt.Errorf("producerPublicKeys[%d] must be exactly one PKIX PUBLIC KEY PEM block", i)
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return fmt.Errorf("producerPublicKeys[%d] is not a valid PKIX public key", i)
		}
		switch publicKey := key.(type) {
		case *rsa.PublicKey:
			switch publicKey.N.BitLen() {
			case 2048, 3072, 4096:
			default:
				return fmt.Errorf("producerPublicKeys[%d] RSA key must be 2048, 3072 or 4096 bits", i)
			}
		case *ecdsa.PublicKey:
			switch publicKey.Curve.Params().Name {
			case "P-256", "P-384", "P-521":
			default:
				return fmt.Errorf("producerPublicKeys[%d] uses an unsupported ECDSA curve", i)
			}
		case ed25519.PublicKey:
		default:
			return fmt.Errorf("producerPublicKeys[%d] uses an unsupported key algorithm", i)
		}
		identity := sha256.Sum256(block.Bytes)
		if seen[identity] {
			return fmt.Errorf("producerPublicKeys[%d] duplicates a configured key", i)
		}
		seen[identity] = true
	}
	return nil
}

func validateAppArtifactStoreURL(raw string) error {
	const message = "storeURL must be an HTTPS origin without credentials, path, query, or fragment"
	u, err := url.Parse(raw)
	if len(raw) > 2048 || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%s", message)
	}
	if net.ParseIP(u.Hostname()) == nil && len(validation.IsDNS1123Subdomain(strings.ToLower(u.Hostname()))) != 0 {
		return fmt.Errorf("%s", message)
	}
	if u.Hostname() != strings.ToLower(u.Hostname()) || strings.HasSuffix(u.Hostname(), ".") {
		return fmt.Errorf("storeURL host must be lowercase without a trailing dot")
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%s", message)
	}
	if port := u.Port(); port != "" {
		parsed, err := strconv.Atoi(port)
		if err != nil || parsed < 1 || parsed > 65535 {
			return fmt.Errorf("storeURL port must be between 1 and 65535")
		}
	}
	return nil
}
