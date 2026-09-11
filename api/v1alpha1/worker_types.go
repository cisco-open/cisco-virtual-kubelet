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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// DeviceWorkerConfig controls resources consumed by the Kubernetes worker.
// It does not change the virtual node's advertised capacity or hosted apps.
type DeviceWorkerConfig struct {
	// Resources replaces the manager's complete worker resource requirements
	// when present. An explicit empty object clears inherited requirements.
	// +kubebuilder:validation:Optional
	Resources *DeviceWorkerResources `json:"resources,omitempty"`

	// TmpSizeLimit caps the worker's disk-backed /tmp emptyDir, including image
	// staging. Omitted inherits the manager default; no default means unbounded.
	// This is an eviction limit, not reserved storage. Use ephemeral-storage
	// requests to account for scheduling and leave headroom for logs and images.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="quantity(string(self)).isGreaterThan(quantity('0'))",message="tmpSizeLimit must be positive"
	TmpSizeLimit *resource.Quantity `json:"tmpSizeLimit,omitempty"`
}

// DeviceWorkerResources supports the standard container CPU, memory, and
// ephemeral-storage requests and limits. Device plugins and DRA claims are not
// needed by the CVK worker and are intentionally outside this configuration.
// +kubebuilder:validation:XValidation:rule="!has(self.requests) || !has(self.limits) || self.requests.all(k, !(k in self.limits) || !quantity(string(self.requests[k])).isGreaterThan(quantity(string(self.limits[k]))))",message="worker requests must not exceed their corresponding limits"
type DeviceWorkerResources struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxProperties=3
	// +kubebuilder:validation:XValidation:rule="self.all(k, k in ['cpu', 'memory', 'ephemeral-storage'] && !quantity(string(self[k])).isLessThan(quantity('0')))",message="worker requests must be nonnegative CPU, memory, or ephemeral-storage quantities"
	Requests map[corev1.ResourceName]resource.Quantity `json:"requests,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxProperties=3
	// +kubebuilder:validation:XValidation:rule="self.all(k, k in ['cpu', 'memory', 'ephemeral-storage'] && !quantity(string(self[k])).isLessThan(quantity('0')))",message="worker limits must be nonnegative CPU, memory, or ephemeral-storage quantities"
	Limits map[corev1.ResourceName]resource.Quantity `json:"limits,omitempty"`
}

// Validate also applies admission constraints to manager defaults, which do
// not pass through the CiscoDevice API.
func (c *DeviceWorkerConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.TmpSizeLimit != nil && c.TmpSizeLimit.Sign() <= 0 {
		return fmt.Errorf("tmpSizeLimit must be positive")
	}
	if c.Resources == nil {
		return nil
	}
	for _, set := range []struct {
		name   string
		values corev1.ResourceList
	}{{"requests", c.Resources.Requests}, {"limits", c.Resources.Limits}} {
		for name, value := range set.values {
			if name != corev1.ResourceCPU && name != corev1.ResourceMemory && name != corev1.ResourceEphemeralStorage {
				return fmt.Errorf("resources.%s: unsupported resource %q", set.name, name)
			}
			if value.Sign() < 0 {
				return fmt.Errorf("resources.%s.%s must be nonnegative", set.name, name)
			}
		}
	}
	for name, request := range c.Resources.Requests {
		if limit, exists := c.Resources.Limits[name]; exists && request.Cmp(limit) > 0 {
			return fmt.Errorf("resources.requests.%s exceeds its limit", name)
		}
	}
	return nil
}
