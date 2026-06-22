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

// NXOSConfig contains NX-OS specific app-hosting configuration.
type NXOSConfig struct {
	// Networking configures the guest interface passed to NX-OS app-hosting.
	// +kubebuilder:validation:Optional
	Networking *NXOSNetworkingConfig `json:"networking,omitempty" mapstructure:"networking,omitempty"`
}

// NXOSInterfaceType identifies the host-side app-hosting attachment model.
//
// +kubebuilder:validation:Enum=Management
type NXOSInterfaceType string

const (
	// NXOSInterfaceManagement attaches the app to NX-OS management networking.
	NXOSInterfaceManagement NXOSInterfaceType = "Management"
)

// NXOSNetworkingConfig wraps the selected NX-OS app-hosting interface.
type NXOSNetworkingConfig struct {
	// Interface is the app-hosting attachment declaration.
	// +kubebuilder:validation:Optional
	Interface *NXOSInterfaceConfig `json:"interface,omitempty" mapstructure:"interface,omitempty"`
}

// NXOSInterfaceConfig declares which NX-OS app-hosting interface style to use.
type NXOSInterfaceConfig struct {
	// Type selects the interface family. Management is the initial supported mode.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Management
	// +kubebuilder:default=Management
	Type NXOSInterfaceType `json:"type,omitempty" mapstructure:"type,omitempty"`

	// Management holds management-network app-hosting settings.
	// +kubebuilder:validation:Optional
	Management *NXOSManagementInterfaceConfig `json:"management,omitempty" mapstructure:"management,omitempty"`
}

// NXOSManagementInterfaceConfig configures the management app-vnic.
type NXOSManagementInterfaceConfig struct {
	// GuestInterface is the guest interface index used by app-hosting.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=7
	// +kubebuilder:default=0
	GuestInterface uint8 `json:"guestInterface,omitempty" mapstructure:"guestInterface,omitempty"`
}
