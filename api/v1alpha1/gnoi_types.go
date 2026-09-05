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
	// and optional client-certificate material from DeviceSpec.TLS.
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

	// TLS overrides the shared DeviceSpec TLS settings for the gNOI connection
	// only. Use this when RESTCONF must retain different TLS behavior from
	// secure gNOI, for example while bootstrapping gNOI with bootstrap.crt and a
	// dedicated CA bundle.
	// +kubebuilder:validation:Optional
	TLS *TLSConfig `json:"tls,omitempty" mapstructure:"tls,omitempty"`
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
	return nil
}
