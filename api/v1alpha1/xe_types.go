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

var xeGNOICertificateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// XEConfig holds all IOS-XE driver-specific configuration.
type XEConfig struct {
	// Networking holds the optional IOS-XE app-hosting network configuration.
	// It may be omitted when this section carries only IOS-XE gNOI policy.
	// +kubebuilder:validation:Optional
	Networking XENetworkConfig `json:"networking,omitempty" mapstructure:"networking,omitempty"`

	// GNOI holds IOS-XE-specific gNOI policy. Generic gNOI transport and port
	// settings remain in DeviceSpec.GNOI.
	// +kubebuilder:validation:Optional
	GNOI *XEGNOIConfig `json:"gnoi,omitempty" mapstructure:"gnoi,omitempty"`
}

// XEGNOIConfig carries IOS-XE-specific gNOI behavior.
type XEGNOIConfig struct {
	// CertificateProvisioning supplies the gNOI-only trust and signing material
	// used by an explicit ProvisionCertificate IOSXEOperationalAction. OS.Verify
	// remains read-only. The referenced Secret is mounted only into this device's
	// VK pod; certificate material is never copied into its ConfigMap.
	// +kubebuilder:validation:Optional
	CertificateProvisioning *XEGNOICertificateProvisioning `json:"certificateProvisioning,omitempty" mapstructure:"certificateProvisioning,omitempty"`
}

// XEGNOICertificateProvisioning identifies the IOS-XE certificate that CVK
// installs through the gNOI Certificate service and the same-namespace Secret
// carrying its PEM material. Presence of this block is the provisioning opt-in.
// +kubebuilder:validation:XValidation:rule="self.replaceTargetCABundle == true",message="replaceTargetCABundle must be true to acknowledge replacement of the shared gNXI/gNMI CA bundle"
type XEGNOICertificateProvisioning struct {
	// CertificateID is the IOS-XE trustpoint identifier associated with the
	// certificate installed through gNOI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=^[A-Za-z0-9][A-Za-z0-9_.-]*$
	CertificateID string `json:"certificateID" mapstructure:"certificateID"`

	// SecretRef names a Secret in the CiscoDevice namespace. tls.crt and ca.crt
	// are required. Optional bootstrap.crt pins the current IOS-XE TLS leaf;
	// optional ca.key signs one target-generated CSR and must match tls.crt's
	// dedicated intermediate issuer. ca.crt is the complete desired target CA
	// replacement bundle. The worker receives only recognized keys read-only.
	// +kubebuilder:validation:Required
	SecretRef XEGNOIProvisioningSecretReference `json:"secretRef" mapstructure:"secretRef"`

	// ReplaceTargetCABundle must be true to acknowledge that gNOI
	// LoadCertificate replaces the target's complete CA bundle. IOS-XE shares
	// that bundle between gNOI and gNMI, so ca.crt must preserve every peer CA
	// that those services must continue to trust.
	// +kubebuilder:validation:Required
	ReplaceTargetCABundle bool `json:"replaceTargetCABundle" mapstructure:"replaceTargetCABundle"`
}

// XEGNOIProvisioningSecretReference is a same-namespace, name-only Secret
// reference. A dedicated type keeps Kubernetes admission validation aligned
// with the local configuration validator.
type XEGNOIProvisioningSecretReference struct {
	// Name is the DNS-subdomain name of the provisioning Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name" mapstructure:"name"`
}

// Validate checks IOS-XE certificate provisioning for local YAML
// configuration, where Kubernetes CRD admission markers are not available.
func (c *XEGNOIConfig) Validate(gnoi *GNOIConfig) error {
	if c == nil || c.CertificateProvisioning == nil {
		return nil
	}
	if gnoi == nil || gnoi.TransportSecurity != GNOITransportSecurityTLS {
		return fmt.Errorf("certificateProvisioning requires spec.gnoi.transportSecurity to be tls")
	}
	provisioning := c.CertificateProvisioning
	if provisioning.CertificateID == "" {
		return fmt.Errorf("certificateProvisioning.certificateID is required")
	}
	if len(provisioning.CertificateID) > 64 || !xeGNOICertificateIDPattern.MatchString(provisioning.CertificateID) {
		return fmt.Errorf("certificateProvisioning.certificateID must match %s and contain at most 64 characters", xeGNOICertificateIDPattern.String())
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

// XENetworkConfig represents IOS-XE specific networking configuration.
type XENetworkConfig struct {
	// Interface holds the interface-level configuration.
	// +kubebuilder:validation:Optional
	Interface *XEInterfaceConfig `json:"interface,omitempty" mapstructure:"interface,omitempty"`
}

// +kubebuilder:validation:Enum=VirtualPortGroup;AppGigabitEthernet;Management
type XEInterfaceType string

const (
	XEInterfaceTypeVirtualPortGroup   XEInterfaceType = "VirtualPortGroup"
	XEInterfaceTypeAppGigabitEthernet XEInterfaceType = "AppGigabitEthernet"
	XEInterfaceTypeManagement         XEInterfaceType = "Management"
)

// +kubebuilder:validation:Enum=access;trunk
type XEAppGigabitEthernetMode string

const (
	XEAppGigabitEthernetModeAccess XEAppGigabitEthernetMode = "access"
	XEAppGigabitEthernetModeTrunk  XEAppGigabitEthernetMode = "trunk"
)

// XEInterfaceConfig represents a polymorphic IOS-XE interface configuration.
// Only one of the specific interface type configurations should be set.
type XEInterfaceConfig struct {
	// Type specifies which interface type is configured.
	// +kubebuilder:validation:Required
	Type XEInterfaceType `json:"type" mapstructure:"type"`

	// VirtualPortGroup configuration (when type=VirtualPortGroup).
	// +kubebuilder:validation:Optional
	VirtualPortGroup *XEVirtualPortGroupConfig `json:"virtualPortGroup,omitempty" mapstructure:"virtualPortGroup,omitempty"`

	// AppGigabitEthernet configuration (when type=AppGigabitEthernet).
	// +kubebuilder:validation:Optional
	AppGigabitEthernet *XEAppGigabitEthernetConfig `json:"appGigabitEthernet,omitempty" mapstructure:"appGigabitEthernet,omitempty"`

	// Management configuration (when type=Management).
	// +kubebuilder:validation:Optional
	Management *XEManagementConfig `json:"management,omitempty" mapstructure:"management,omitempty"`
}

// Validate ensures only one interface config is set and it matches Type.
func (c *XEInterfaceConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("interface config cannot be nil")
	}

	setCount := 0
	if c.VirtualPortGroup != nil {
		setCount++
	}
	if c.AppGigabitEthernet != nil {
		setCount++
	}
	if c.Management != nil {
		setCount++
	}

	if setCount == 0 {
		return fmt.Errorf("one interface config must be set")
	}
	if setCount > 1 {
		return fmt.Errorf("only one interface config may be set")
	}

	switch c.Type {
	case XEInterfaceTypeVirtualPortGroup:
		if c.VirtualPortGroup == nil {
			return fmt.Errorf("type VirtualPortGroup requires virtualPortGroup config")
		}
	case XEInterfaceTypeAppGigabitEthernet:
		if c.AppGigabitEthernet == nil {
			return fmt.Errorf("type AppGigabitEthernet requires appGigabitEthernet config")
		}
	case XEInterfaceTypeManagement:
		if c.Management == nil {
			return fmt.Errorf("type Management requires management config")
		}
	default:
		return fmt.Errorf("unsupported interface type: %s", c.Type)
	}

	return nil
}

// XEVirtualPortGroupConfig represents VirtualPortGroup interface settings.
type XEVirtualPortGroupConfig struct {
	// Dhcp enables DHCP for the VirtualPortGroup interface.
	Dhcp bool `json:"dhcp" mapstructure:"dhcp"`

	// Interface number (0-3 for VirtualPortGroup0-3).
	// +kubebuilder:validation:Optional
	Interface string `json:"interface,omitempty" mapstructure:"interface"`

	// GuestInterface number inside the container (optional, defaults to 0).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Maximum=3
	GuestInterface uint8 `json:"guestInterface,omitempty" mapstructure:"guestInterface,omitempty"`
}

// XEManagementConfig represents Management interface settings.
type XEManagementConfig struct {
	// Dhcp enables DHCP for the Management interface.
	Dhcp bool `json:"dhcp" mapstructure:"dhcp"`

	// GuestInterface number inside the container (optional, defaults to 0).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Maximum=3
	GuestInterface uint8 `json:"guestInterface,omitempty" mapstructure:"guestInterface,omitempty"`
}

// XEVlanInterfaceConfig represents VLAN-specific interface settings for AppGigabitEthernet.
type XEVlanInterfaceConfig struct {
	// Dhcp enables DHCP for the VLAN interface.
	Dhcp bool `json:"dhcp" mapstructure:"dhcp"`

	// Vlan ID for the interface.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4094
	Vlan uint16 `json:"vlan,omitempty" mapstructure:"vlan"`

	// GuestInterface number inside the container (optional, defaults to 0).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Maximum=3
	GuestInterface uint8 `json:"guestInterface,omitempty" mapstructure:"guestInterface,omitempty"`

	// MacForwardingEnabled enables MAC address forwarding.
	// +kubebuilder:validation:Optional
	MacForwardingEnabled bool `json:"macForwardingEnabled,omitempty" mapstructure:"macForwardingEnabled,omitempty"`

	// MulticastEnabled enables multicast traffic.
	// +kubebuilder:validation:Optional
	MulticastEnabled bool `json:"multicastEnabled,omitempty" mapstructure:"multicastEnabled,omitempty"`

	// MirrorEnabled enables port mirroring.
	// +kubebuilder:validation:Optional
	MirrorEnabled bool `json:"mirrorEnabled,omitempty" mapstructure:"mirrorEnabled,omitempty"`
}

// XEAppGigabitEthernetConfig represents AppGigabitEthernet interface settings.
type XEAppGigabitEthernetConfig struct {
	// Mode specifies access or trunk mode.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=access;trunk
	Mode XEAppGigabitEthernetMode `json:"mode" mapstructure:"mode"`

	// VlanIf holds VLAN-specific configuration.
	// +kubebuilder:validation:Optional
	VlanIf XEVlanInterfaceConfig `json:"vlanIf,omitempty" mapstructure:"vlanIf,omitempty"`

	// GuestInterface number inside the container (optional, defaults to 0).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Maximum=3
	GuestInterface uint8 `json:"guestInterface,omitempty" mapstructure:"guestInterface,omitempty"`

	// Dhcp enables DHCP when in access mode without a VLAN interface.
	// +kubebuilder:validation:Optional
	Dhcp bool `json:"dhcp,omitempty" mapstructure:"dhcp"`
}
