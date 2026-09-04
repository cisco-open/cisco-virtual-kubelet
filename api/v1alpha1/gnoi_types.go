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

package v1alpha1

import (
	"fmt"
	"regexp"
	"strings"

	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

var gnoiCertificateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// GNOITransportSecurity selects how the per-device gNOI connection is
// protected. Omitted (or empty in local YAML) and auto preserve legacy
// transport inference.
// +kubebuilder:validation:Enum=auto;tls
type GNOITransportSecurity string

const (
	// GNOITransportSecurityAuto inherits the secure/plaintext decision from
	// the device's shared TLS configuration.
	GNOITransportSecurityAuto GNOITransportSecurity = "auto"
	// GNOITransportSecurityTLS forces gNOI to use TLS while reusing the trust
	// and optional client-certificate material from DeviceSpec.TLS.
	GNOITransportSecurityTLS GNOITransportSecurity = "tls"
)

// GNOIConfig carries opt-in, per-device gNOI settings. Omit fields in a
// Kubernetes object to preserve legacy transport and port inference; local
// YAML also accepts zero values. Credentials are not
// duplicated here: explicit secure gNOI derives the IOS XE username/password
// metadata from DeviceSpec.Username and the resolved password at runtime.
// +kubebuilder:validation:XValidation:rule="!has(self.certificateProvisioning) || (has(self.transportSecurity) && self.transportSecurity == 'tls')",message="certificateProvisioning requires transportSecurity to be tls"
type GNOIConfig struct {
	// Port overrides the device-side gNOI listener port. Omit it to preserve
	// legacy port inference (local YAML also accepts zero).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port,omitempty" mapstructure:"port,omitempty"`

	// TransportSecurity controls whether gNOI uses TLS. Omitted/auto inherits
	// the decision from DeviceSpec.TLS.Enabled (local YAML also accepts empty).
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=auto
	TransportSecurity GNOITransportSecurity `json:"transportSecurity,omitempty" mapstructure:"transportSecurity,omitempty"`

	// CertificateProvisioning supplies the gNOI-only trust and signing material
	// used by an explicit ProvisionCertificate IOSXEOperationalAction. OS.Verify
	// remains read-only. The referenced Secret is mounted only into this device's
	// VK pod; certificate material is never copied into its ConfigMap.
	// +kubebuilder:validation:Optional
	CertificateProvisioning *GNOICertificateProvisioning `json:"certificateProvisioning,omitempty" mapstructure:"certificateProvisioning,omitempty"`
}

// GNOICertificateProvisioning identifies the certificate that CVK installs
// through the gNOI Certificate service and the same-namespace Secret carrying
// its PEM material. Presence of this block is the provisioning opt-in.
// +kubebuilder:validation:XValidation:rule="self.replaceTargetCABundle == true",message="replaceTargetCABundle must be true to acknowledge replacement of the shared gNXI/gNMI CA bundle"
type GNOICertificateProvisioning struct {
	// CertificateID is the IOS XE trustpoint identifier associated with the
	// certificate installed through gNOI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=^[A-Za-z0-9][A-Za-z0-9_.-]*$
	CertificateID string `json:"certificateID" mapstructure:"certificateID"`

	// SecretRef names a Secret in the CiscoDevice namespace. tls.crt and ca.crt
	// are required. Optional bootstrap.crt pins the current IOS XE TLS leaf;
	// optional ca.key signs one target-generated CSR and must match tls.crt's
	// dedicated intermediate issuer. ca.crt is the complete desired target CA
	// replacement bundle. The worker receives only recognized keys read-only.
	// +kubebuilder:validation:Required
	SecretRef GNOIProvisioningSecretReference `json:"secretRef" mapstructure:"secretRef"`

	// ReplaceTargetCABundle must be true to acknowledge that gNOI
	// LoadCertificate replaces the target's complete CA bundle. IOS XE shares
	// that bundle between gNOI and gNMI, so ca.crt must preserve every peer CA
	// that those services must continue to trust.
	// +kubebuilder:validation:Required
	ReplaceTargetCABundle bool `json:"replaceTargetCABundle" mapstructure:"replaceTargetCABundle"`
}

// GNOIProvisioningSecretReference is a same-namespace, name-only Secret
// reference. A dedicated type keeps Kubernetes admission validation aligned
// with the local configuration validator.
type GNOIProvisioningSecretReference struct {
	// Name is the DNS-subdomain name of the provisioning Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name" mapstructure:"name"`
}

// Validate applies the gNOI constraints to local YAML configuration, where
// Kubernetes CRD admission markers are not available.
func (c *GNOIConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535 when set")
	}
	switch c.TransportSecurity {
	case "", GNOITransportSecurityAuto, GNOITransportSecurityTLS:
		// Valid; continue with optional certificate-provisioning validation.
	default:
		return fmt.Errorf("transportSecurity must be auto or tls")
	}
	if c.CertificateProvisioning == nil {
		return nil
	}
	if c.TransportSecurity != GNOITransportSecurityTLS {
		return fmt.Errorf("certificateProvisioning requires transportSecurity to be tls")
	}
	provisioning := c.CertificateProvisioning
	if provisioning.CertificateID == "" {
		return fmt.Errorf("certificateProvisioning.certificateID is required")
	}
	if len(provisioning.CertificateID) > 64 || !gnoiCertificateIDPattern.MatchString(provisioning.CertificateID) {
		return fmt.Errorf("certificateProvisioning.certificateID must match %s and contain at most 64 characters", gnoiCertificateIDPattern.String())
	}
	if provisioning.SecretRef.Name == "" {
		return fmt.Errorf("certificateProvisioning.secretRef.name is required")
	}
	if problems := utilvalidation.IsDNS1123Subdomain(provisioning.SecretRef.Name); len(problems) > 0 {
		return fmt.Errorf("certificateProvisioning.secretRef.name is invalid: %s", strings.Join(problems, "; "))
	}
	if !provisioning.ReplaceTargetCABundle {
		return fmt.Errorf("certificateProvisioning.replaceTargetCABundle must be true to acknowledge replacement of the shared gNXI/gNMI CA bundle")
	}
	return nil
}
