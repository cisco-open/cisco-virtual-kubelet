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

package config

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
)

type DeviceDriver string

type Config struct {
	// Kubelet tier: Standard Virtual Kubelet settings
	Kubelet KubeletConfig `mapstructure:"kubelet"`

	// Device tier: Abstracted Cisco-specific settings
	Device DeviceConfig `mapstructure:"device"`
}

type KubeletConfig struct {
	NodeName       string `mapstructure:"node_name"`
	NodeInternalIP string `mapstructure:"node_internal_ip"`
}

type DeviceConfig struct {
	Name           string            `mapstructure:"name"`
	Driver         DeviceDriver      `mapstructure:"driver"`
	Address        string            `mapstructure:"address"`
	Port           int               `mapstructure:"port"`
	Username       string            `mapstructure:"username"`
	Password       string            `mapstructure:"password"`
	TLSConfig      *TLSConfig        `mapstructure:"tls,omitempty"`
	Labels         map[string]string `mapstructure:"labels,omitempty"`
	Taints         []v1.Taint        `mapstructure:"taints,omitempty"`
	MaxPods        int32             `mapstructure:"maxPods"`
	Region         string            `mapstructure:"region,omitempty"`
	Zone           string            `mapstructure:"zone,omitempty"`
	ResourceLimits ResourceConfig    `mapstructure:"resourceLimits"`
	Networking     NetworkConfig     `mapstructure:"networking"`
}

// TLSConfig represents TLS configuration for device communication
type TLSConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	InsecureSkipVerify bool   `mapstructure:"insecureSkipVerify"`
	CertFile           string `mapstructure:"certFile,omitempty"`
	KeyFile            string `mapstructure:"keyFile,omitempty"`
	CAFile             string `mapstructure:"caFile,omitempty"`
}

// ResourceConfig represents resource limits and defaults
type ResourceConfig struct {
	DefaultCPU     string            `mapstructure:"defaultCPU"`
	DefaultMemory  string            `mapstructure:"defaultMemory"`
	DefaultStorage string            `mapstructure:"defaultStorage"`
	MaxCPU         string            `mapstructure:"maxCPU"`
	MaxMemory      string            `mapstructure:"maxMemory"`
	MaxStorage     string            `mapstructure:"maxStorage"`
	Others         map[string]string `mapstructure:"others,omitempty"`
}

// InterfaceType defines the type of network interface
type InterfaceType string

const (
	InterfaceTypeVirtualPortGroup   InterfaceType = "VirtualPortGroup"
	InterfaceTypeAppGigabitEthernet InterfaceType = "AppGigabitEthernet"
	InterfaceTypeManagement         InterfaceType = "Management"
)

// AppGigabitEthernetMode defines the mode of AppGigabitEthernet interface
type AppGigabitEthernetMode string

const (
	AppGigabitEthernetModeAccess AppGigabitEthernetMode = "access"
	AppGigabitEthernetModeTrunk  AppGigabitEthernetMode = "trunk"
)

// NetworkConfig represents networking configuration
type NetworkConfig struct {
	// Prefix to use for pod network interfaces
	PodPrefix string `mapstructure:"podPrefix,omitempty"`

	// Interface configuration
	Interface *InterfaceConfig `mapstructure:"interface,omitempty"`
}

// InterfaceConfig represents a polymorphic interface configuration
// Only one of the specific interface type configurations should be set
type InterfaceConfig struct {
	// Type specifies which interface type is configured
	Type InterfaceType `mapstructure:"type"`

	// VirtualPortGroup configuration (for VirtualPortGroup interfaces)
	VirtualPortGroup *VirtualPortGroupConfig `mapstructure:"virtualPortGroup,omitempty"`

	// AppGigabitEthernet configuration (for AppGigabitEthernet interfaces)
	AppGigabitEthernet *AppGigabitEthernetConfig `mapstructure:"appGigabitEthernet,omitempty"`

	// Management configuration (for Management interfaces)
	Management *ManagementConfig `mapstructure:"management,omitempty"`
}

// Validate ensures only one interface config is set and it matches Type.
func (c *InterfaceConfig) Validate() error {
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
	case InterfaceTypeVirtualPortGroup:
		if c.VirtualPortGroup == nil {
			return fmt.Errorf("type VirtualPortGroup requires virtualPortGroup config")
		}
	case InterfaceTypeAppGigabitEthernet:
		if c.AppGigabitEthernet == nil {
			return fmt.Errorf("type AppGigabitEthernet requires appGigabitEthernet config")
		}
	case InterfaceTypeManagement:
		if c.Management == nil {
			return fmt.Errorf("type Management requires management config")
		}
	default:
		return fmt.Errorf("unsupported interface type: %s", c.Type)
	}

	return nil
}

// VirtualPortGroupConfig represents VirtualPortGroup interface settings
type VirtualPortGroupConfig struct {
	// Enable DHCP for the VirtualPortGroup interface
	Dhcp bool `mapstructure:"dhcp"`

	// Interface number (0-3 for VirtualPortGroup0-3)
	Interface string `mapstructure:"interface"`

	// GuestInterface number inside the container (optional, defaults to 0)
	GuestInterface uint8 `mapstructure:"guestInterface,omitempty"`
}

type ManagementConfig struct {
	// Enable DHCP for the Management interface
	Dhcp bool `mapstructure:"dhcp"`

	// GuestInterface number inside the container (optional, defaults to 0)
	GuestInterface uint8 `mapstructure:"guestInterface,omitempty"`
}

type VlanInterfaceConfig struct {
	// Enable DHCP for the VLAN interface
	Dhcp bool `mapstructure:"dhcp"`

	// VlanID for access mode (single VLAN)
	Vlan uint16 `mapstructure:"vlan"`

	// GuestInterface number inside the container (optional, defaults to 0)
	GuestInterface uint8 `mapstructure:"guestInterface,omitempty"`

	// MacForwardingEnabled enables MAC address forwarding
	MacForwardingEnabled bool `mapstructure:"macForwardingEnabled,omitempty"`

	// MulticastEnabled enables multicast traffic
	MulticastEnabled bool `mapstructure:"multicastEnabled,omitempty"`

	// MirrorEnabled enables port mirroring
	MirrorEnabled bool `mapstructure:"mirrorEnabled,omitempty"`
}

// AppGigabitEthernetConfig represents AppGigabitEthernet interface settings
type AppGigabitEthernetConfig struct {
	// PortMode specifies access or trunk mode
	Mode AppGigabitEthernetMode `mapstructure:"mode"`

	// VlanID for access mode (single VLAN)
	VlanIf VlanInterfaceConfig `mapstructure:"vlanIf,omitempty"`

	// GuestInterface number inside the container (optional, defaults to 0)
	GuestInterface uint8 `mapstructure:"guestInterface,omitempty"`

	// Enable DHCP when we have an access interface
	Dhcp bool `mapstructure:"dhcp"`
}
