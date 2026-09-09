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

import "fmt"

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
	// from DeviceSpec.TLS, a gNOI-only override, or driver-specific
	// provisioning policy.
	GNOITransportSecurityTLS GNOITransportSecurity = "tls"
)

// GNOIConfig carries opt-in, per-device gNOI settings. Omit fields in a
// Kubernetes object to preserve legacy transport and port inference; local
// YAML also accepts zero values. Authentication and provisioning policy are
// not duplicated here: the selected driver owns those details. The IOS-XE
// runtime derives secure password authentication from the shared DeviceSpec
// credentials, while its certificate policy lives under DeviceSpec.XE.GNOI.
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

	// TLS supplies verified trust for the gNOI connection independently from
	// DeviceSpec.TLS. Kubernetes objects use SecretRef; local YAML uses CAFile
	// and may include a CertFile/KeyFile client pair. It cannot be combined with
	// driver-specific certificate provisioning, which derives its own trust.
	// +kubebuilder:validation:Optional
	TLS *GNOITLSConfig `json:"tls,omitempty" mapstructure:"tls,omitempty"`
}

// GNOITLSConfig contains verified-only TLS material dedicated to gNOI. It
// intentionally omits transport and skip-verification switches: presence is
// valid only with transportSecurity=tls and certificate verification is
// always required.
//
// Kubernetes CiscoDevice objects must use SecretRef so host filesystem paths
// never cross the API boundary. The controller projects the recognized Secret
// keys to local files before starting a worker.
// +kubebuilder:validation:XValidation:rule="!has(self.caFile) && !has(self.certFile) && !has(self.keyFile)",message="caFile, certFile, and keyFile are local-only and cannot be set in a Kubernetes object"
type GNOITLSConfig struct {
	// CAFile is the local path to the PEM CA bundle used to verify the gNOI
	// server. It is required in local YAML and forbidden in Kubernetes objects.
	// +kubebuilder:validation:Optional
	CAFile string `json:"caFile,omitempty" mapstructure:"caFile,omitempty"`

	// CertFile is the optional local path to a PEM client certificate. It must
	// be configured together with KeyFile and is forbidden in Kubernetes objects.
	// +kubebuilder:validation:Optional
	CertFile string `json:"certFile,omitempty" mapstructure:"certFile,omitempty"`

	// KeyFile is the optional local path to the client certificate private key.
	// It must be configured together with CertFile and is forbidden in
	// Kubernetes objects.
	// +kubebuilder:validation:Optional
	KeyFile string `json:"keyFile,omitempty" mapstructure:"keyFile,omitempty"`

	// SecretRef names a Secret in the CiscoDevice namespace. ca.crt is required;
	// tls.crt and tls.key may both be provided for mutual TLS. The controller
	// projects only these recognized keys and does not copy their contents into
	// the device ConfigMap. SecretRef is required in Kubernetes objects and is
	// rejected in local YAML. A valid Secret change rolls the worker so new gNOI
	// connections load it when gNOI and per-device topology are enabled.
	// +kubebuilder:validation:Required
	SecretRef *GNOITLSSecretReference `json:"secretRef,omitempty" mapstructure:"secretRef,omitempty"`
}

// GNOITLSSecretReference is a same-namespace, name-only reference to verified
// gNOI trust material.
type GNOITLSSecretReference struct {
	// Name is the DNS-subdomain name of the gNOI TLS Secret.
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
		// Valid.
	default:
		return fmt.Errorf("transportSecurity must be auto or tls")
	}
	if c.TLS == nil {
		return nil
	}
	if c.TransportSecurity != GNOITransportSecurityTLS {
		return fmt.Errorf("tls requires transportSecurity to be tls")
	}
	if err := c.TLS.validateLocal(); err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	return nil
}

func (c *GNOITLSConfig) validateLocal() error {
	if c.SecretRef != nil {
		return fmt.Errorf("secretRef is supported only in Kubernetes objects; local YAML must use caFile")
	}
	if c.CAFile == "" {
		return fmt.Errorf("caFile is required")
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return fmt.Errorf("certFile and keyFile must be configured together")
	}
	return nil
}
