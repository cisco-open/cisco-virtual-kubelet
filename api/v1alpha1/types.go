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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=XE;XR;NXOS;FAKE
type DeviceDriver string

const (
	DeviceDriverXE   DeviceDriver = "XE"
	DeviceDriverXR   DeviceDriver = "XR"
	DeviceDriverNXOS DeviceDriver = "NXOS"
	DeviceDriverFAKE DeviceDriver = "FAKE"
)

// CiscoDevice is the Schema for the ciscodevices API.
// It represents a single Cisco device managed by the virtual kubelet operator.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cvk
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver`
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type CiscoDevice struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeviceSpec   `json:"spec"`
	Status DeviceStatus `json:"status,omitempty"`
}

// CiscoDeviceList contains a list of CiscoDevice.
//
// +kubebuilder:object:root=true
type CiscoDeviceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CiscoDevice `json:"items"`
}

// DeviceSpec defines the desired state of a Cisco device.
// Shared fields are common to all drivers; driver-specific networking
// configuration lives under the corresponding driver section (XE, XR, etc.).
type DeviceSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=XE;XR;NXOS;FAKE
	Driver DeviceDriver `json:"driver" mapstructure:"driver"`

	// Address is the management IP or hostname of the device.
	// +kubebuilder:validation:Required
	Address string `json:"address" mapstructure:"address"`

	// Port for device communication (default: 443 for TLS, 80 otherwise).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int `json:"port,omitempty" mapstructure:"port"`

	// Username for device authentication.
	// +kubebuilder:validation:Required
	Username string `json:"username" mapstructure:"username"`

	// Password for device authentication.
	// In CRD mode the controller should source this from a Secret reference.
	// +kubebuilder:validation:Optional
	Password string `json:"password,omitempty" mapstructure:"password"`

	// CredentialSecretRef references a Secret containing device credentials.
	// Used by the controller when creating VK deployments from CRDs.
	// +kubebuilder:validation:Optional
	CredentialSecretRef *v1.LocalObjectReference `json:"credentialSecretRef,omitempty"`

	// TLS configuration for device communication.
	// +kubebuilder:validation:Optional
	TLS *TLSConfig `json:"tls,omitempty" mapstructure:"tls,omitempty"`

	// PodCIDR is the CIDR to use for pod network interfaces when using static IP allocation.
	// +kubebuilder:validation:Optional
	PodCIDR string `json:"podCIDR,omitempty" mapstructure:"podCIDR"`

	// Labels to apply to the virtual kubelet node.
	// +kubebuilder:validation:Optional
	Labels map[string]string `json:"labels,omitempty" mapstructure:"labels,omitempty"`

	// Taints to apply to the virtual kubelet node.
	// +kubebuilder:validation:Optional
	Taints []v1.Taint `json:"taints,omitempty" mapstructure:"taints,omitempty"`

	// MaxPods is the maximum number of pods the device can host.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=16
	MaxPods int32 `json:"maxPods,omitempty" mapstructure:"maxPods"`

	// Region for node topology.
	// +kubebuilder:validation:Optional
	Region string `json:"region,omitempty" mapstructure:"region,omitempty"`

	// Zone for node topology.
	// +kubebuilder:validation:Optional
	Zone string `json:"zone,omitempty" mapstructure:"zone,omitempty"`

	// ResourceLimits defines default and maximum resource allocations.
	// +kubebuilder:validation:Optional
	ResourceLimits ResourceConfig `json:"resourceLimits,omitempty" mapstructure:"resourceLimits"`

	// --- Driver-specific networking configuration (union) ---
	// Only the section matching Driver should be set.

	// XE holds IOS-XE specific networking configuration.
	// Required when driver=XE.
	// +kubebuilder:validation:Optional
	XE *XEConfig `json:"xe,omitempty" mapstructure:"xe,omitempty"`

	// XR holds IOS-XR specific networking configuration (future).
	// +kubebuilder:validation:Optional
	// XR *XRConfig `json:"xr,omitempty" mapstructure:"xr,omitempty"`

	// NXOS holds NX-OS specific networking configuration (future).
	// +kubebuilder:validation:Optional
	// NXOS *NXOSConfig `json:"nxos,omitempty" mapstructure:"nxos,omitempty"`
}

// DeviceStatus defines the observed state of a CiscoDevice.
type DeviceStatus struct {
	// Phase represents the current lifecycle phase of the device.
	// +kubebuilder:validation:Enum=Pending;Provisioning;Ready;Error;Deleting
	Phase string `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the device's state.
	// +kubebuilder:validation:Optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TLSConfig represents TLS configuration for device communication.
type TLSConfig struct {
	// Enabled toggles TLS for device communication.
	Enabled bool `json:"enabled" mapstructure:"enabled"`

	// InsecureSkipVerify disables TLS certificate verification.
	// +kubebuilder:validation:Optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" mapstructure:"insecureSkipVerify"`

	// CertFile is the path to the client certificate file.
	// +kubebuilder:validation:Optional
	CertFile string `json:"certFile,omitempty" mapstructure:"certFile,omitempty"`

	// KeyFile is the path to the client key file.
	// +kubebuilder:validation:Optional
	KeyFile string `json:"keyFile,omitempty" mapstructure:"keyFile,omitempty"`

	// CAFile is the path to the CA certificate file.
	// +kubebuilder:validation:Optional
	CAFile string `json:"caFile,omitempty" mapstructure:"caFile,omitempty"`
}

// ResourceConfig represents resource limits and defaults for container workloads.
type ResourceConfig struct {
	// +kubebuilder:validation:Optional
	DefaultCPU string `json:"defaultCPU,omitempty" mapstructure:"defaultCPU"`
	// +kubebuilder:validation:Optional
	DefaultMemory string `json:"defaultMemory,omitempty" mapstructure:"defaultMemory"`
	// +kubebuilder:validation:Optional
	DefaultStorage string `json:"defaultStorage,omitempty" mapstructure:"defaultStorage"`
	// +kubebuilder:validation:Optional
	MaxCPU string `json:"maxCPU,omitempty" mapstructure:"maxCPU"`
	// +kubebuilder:validation:Optional
	MaxMemory string `json:"maxMemory,omitempty" mapstructure:"maxMemory"`
	// +kubebuilder:validation:Optional
	MaxStorage string `json:"maxStorage,omitempty" mapstructure:"maxStorage"`
	// +kubebuilder:validation:Optional
	Others map[string]string `json:"others,omitempty" mapstructure:"others,omitempty"`
}
