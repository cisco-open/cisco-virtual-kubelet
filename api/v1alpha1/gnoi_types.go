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
// protected. The empty value has the same behavior as auto.
// +kubebuilder:validation:Enum=auto;tls;plaintext
type GNOITransportSecurity string

const (
	// GNOITransportSecurityAuto inherits the secure/plaintext decision from
	// the device's shared TLS configuration.
	GNOITransportSecurityAuto GNOITransportSecurity = "auto"
	// GNOITransportSecurityTLS forces gNOI to use TLS while reusing the trust
	// and optional client-certificate material from DeviceSpec.TLS.
	GNOITransportSecurityTLS GNOITransportSecurity = "tls"
	// GNOITransportSecurityPlaintext forces gNOI to use plaintext transport.
	// It exists for compatibility with older IOS-XE lab deployments.
	GNOITransportSecurityPlaintext GNOITransportSecurity = "plaintext"
)

// GNOIConfig carries per-device gNOI transport overrides. Credentials are not
// duplicated here: secure gNOI derives the IOS XE username/password metadata
// from DeviceSpec.Username and the resolved device password at runtime.
type GNOIConfig struct {
	// Port is the device-side gNOI listener port. When omitted, CVK uses 9339
	// for TLS and 50052 for plaintext. Unlike the legacy inference path, a
	// present GNOI block never inherits DeviceSpec.Port.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port,omitempty" mapstructure:"port,omitempty"`

	// TransportSecurity controls whether gNOI uses TLS. Empty and auto inherit
	// the decision from DeviceSpec.TLS.Enabled.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=auto
	TransportSecurity GNOITransportSecurity `json:"transportSecurity,omitempty" mapstructure:"transportSecurity,omitempty"`
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
	case "", GNOITransportSecurityAuto, GNOITransportSecurityTLS, GNOITransportSecurityPlaintext:
		return nil
	default:
		return fmt.Errorf("transportSecurity must be one of auto, tls, or plaintext")
	}
}
