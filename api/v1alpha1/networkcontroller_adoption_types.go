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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkControllerDeviceRemovalPolicy says what happens to an adopted
// CiscoDevice whose switch is no longer in the controller's inventory.
// +kubebuilder:validation:Enum=Retain;Prune
type NetworkControllerDeviceRemovalPolicy string

const (
	// DeviceRemovalRetain only annotates missing devices. It is the default.
	DeviceRemovalRetain NetworkControllerDeviceRemovalPolicy = "Retain"
	// DeviceRemovalPrune deletes devices that stayed missing for the grace period.
	DeviceRemovalPrune NetworkControllerDeviceRemovalPolicy = "Prune"

	DefaultDeviceRemovalGracePeriod     = 24 * time.Hour
	MinDeviceRemovalGracePeriod         = 10 * time.Minute
	MaxDeviceRemovalGracePeriod         = 30 * 24 * time.Hour
	DefaultDeviceRemovalMaxPrunePercent = int32(25)
)

// NetworkControllerDeviceRemoval controls devices whose switch has left the
// controller inventory. Missing devices are always annotated; they are only
// deleted with policy Prune.
type NetworkControllerDeviceRemoval struct {
	// Policy is Retain (default) or Prune.
	// +kubebuilder:default=Retain
	// +optional
	Policy NetworkControllerDeviceRemovalPolicy `json:"policy,omitempty"`

	// GracePeriod is how long a device must stay missing, across refreshes,
	// before Prune deletes it. Defaults to 24h; between 10m and 720h.
	// +optional
	GracePeriod *metav1.Duration `json:"gracePeriod,omitempty"`

	// MaxPrunePercent is the guard against mass deletion: when more than this
	// percentage of the adopted devices are due for pruning at once (at least
	// one device is always allowed), nothing is deleted. Defaults to 25.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxPrunePercent *int32 `json:"maxPrunePercent,omitempty"`
}

// NetworkControllerDeviceAccess is the per-device access material copied onto
// each adopted CiscoDevice. Controllers do not expose device passwords, so the
// operator names the Secret that holds them; the adapter never reads it. The
// Secret must live in the NetworkController's namespace because
// CiscoDevice.spec.credentialSecretRef is a same-namespace reference, and it
// follows the existing CiscoDevice contract (key "password").
type NetworkControllerDeviceAccess struct {
	// Username is the device login copied to CiscoDevice.spec.username.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Username string `json:"username"`

	// CredentialSecretRef names the Secret copied to
	// CiscoDevice.spec.credentialSecretRef.
	// +kubebuilder:validation:Required
	CredentialSecretRef NetworkControllerSecretReference `json:"credentialSecretRef"`

	// TLS is copied to CiscoDevice.spec.tls. Omit to keep the CiscoDevice
	// default.
	// +optional
	TLS *TLSConfig `json:"tls,omitempty"`

	// Labels are added to every adopted CiscoDevice, both in metadata.labels
	// (for kubectl selectors) and in spec.labels (projected onto the Node), for
	// example to group devices into upgrade waves. The adapter's own
	// nd.cisco.vk/ labels, the cisco.vk/ and kubernetes.io/ namespaces and
	// scheduling-topology keys are reserved. A label removed here is removed
	// from the devices the adapter manages.
	// +kubebuilder:validation:MaxProperties=16
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// NetworkControllerDeviceScopeOverride replaces selected access fields for the
// devices in one controller-defined scope (a Nexus Dashboard fabric, for
// example). Omitted fields inherit from spec.deviceAdoption.defaults.
type NetworkControllerDeviceScopeOverride struct {
	// Scope is the controller-defined grouping name, matched exactly.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9_.-]*$`
	Scope string `json:"scope"`

	// Username overrides the default device login.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Username string `json:"username,omitempty"`

	// CredentialSecretRef overrides the default credential Secret.
	// +optional
	CredentialSecretRef *NetworkControllerSecretReference `json:"credentialSecretRef,omitempty"`

	// TLS overrides the default device TLS settings.
	// +optional
	TLS *TLSConfig `json:"tls,omitempty"`

	// Labels are merged key by key over the default labels; a key set here
	// replaces the default value for devices in this scope.
	// +kubebuilder:validation:MaxProperties=16
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// NetworkControllerDeviceAdoption opts a controller into turning its inventory
// into standalone CiscoDevice objects. It is off unless Enabled is true, and
// only adapters that advertise the inventory capability act on it.
//
// +kubebuilder:validation:XValidation:rule="!self.enabled || has(self.defaults)",message="defaults is required when enabled is true"
type NetworkControllerDeviceAdoption struct {
	// Enabled turns adoption on.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Defaults apply to every adopted device that has no scope override.
	// +optional
	Defaults *NetworkControllerDeviceAccess `json:"defaults,omitempty"`

	// ScopeOverrides replace default access fields per scope.
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=scope
	// +optional
	ScopeOverrides []NetworkControllerDeviceScopeOverride `json:"scopeOverrides,omitempty"`

	// Removal controls devices whose switch has left the inventory.
	// +optional
	Removal *NetworkControllerDeviceRemoval `json:"removal,omitempty"`
}

// EffectivePolicy returns the removal policy with the default applied.
func (r *NetworkControllerDeviceRemoval) EffectivePolicy() NetworkControllerDeviceRemovalPolicy {
	if r == nil || r.Policy == "" {
		return DeviceRemovalRetain
	}
	return r.Policy
}

// EffectiveGracePeriod returns the grace period with the default applied.
func (r *NetworkControllerDeviceRemoval) EffectiveGracePeriod() time.Duration {
	if r == nil || r.GracePeriod == nil {
		return DefaultDeviceRemovalGracePeriod
	}
	return r.GracePeriod.Duration
}

// EffectiveMaxPrunePercent returns the mass-delete guard with the default applied.
func (r *NetworkControllerDeviceRemoval) EffectiveMaxPrunePercent() int32 {
	if r == nil || r.MaxPrunePercent == nil {
		return DefaultDeviceRemovalMaxPrunePercent
	}
	return *r.MaxPrunePercent
}

// ResolveAccess returns the effective access for one scope: the defaults with
// any matching override applied field by field. It returns false when
// adoption is disabled or defaults are missing, so callers never invent
// credentials.
func (a *NetworkControllerDeviceAdoption) ResolveAccess(scope string) (NetworkControllerDeviceAccess, bool) {
	if a == nil || !a.Enabled || a.Defaults == nil {
		return NetworkControllerDeviceAccess{}, false
	}
	out := *a.Defaults.DeepCopy()
	for i := range a.ScopeOverrides {
		o := &a.ScopeOverrides[i]
		if o.Scope != scope {
			continue
		}
		if o.Username != "" {
			out.Username = o.Username
		}
		if o.CredentialSecretRef != nil {
			out.CredentialSecretRef = *o.CredentialSecretRef
		}
		if o.TLS != nil {
			out.TLS = o.TLS.DeepCopy()
		}
		if len(o.Labels) > 0 && out.Labels == nil {
			out.Labels = make(map[string]string, len(o.Labels))
		}
		for k, v := range o.Labels {
			out.Labels[k] = v
		}
		break
	}
	return out, true
}
