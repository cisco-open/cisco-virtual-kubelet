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

// XEConfig holds all IOS-XE driver-specific configuration.
type XEConfig struct {
	// Networking holds the IOS-XE networking configuration.
	// +kubebuilder:validation:Required
	Networking XENetworkConfig `json:"networking" mapstructure:"networking"`
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
