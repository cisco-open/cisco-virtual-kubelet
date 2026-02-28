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

// Package v1alpha1 contains the CiscoDevice API types for the cisco.vk group.
//
// These types define the canonical device configuration schema used by:
//   - YAML config files (read by the VK binary via mapstructure)
//   - Kubernetes CRDs (generated via controller-gen from kubebuilder markers)
//   - Device drivers (consume DeviceSpec to configure physical devices)
//
// +kubebuilder:object:generate=true
// +groupName=cisco.vk
package v1alpha1
