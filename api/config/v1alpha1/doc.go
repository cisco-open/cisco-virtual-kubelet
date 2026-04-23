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

// Package v1alpha1 contains the declarative configuration API types for
// managing IOS-XE device state alongside application hosting.
//
// Four CRDs are defined in this group, mirroring the netascode scope tree:
//
//   - IOSXEConfig            — per-device desired configuration
//   - IOSXEConfigDefaults    — cluster-scoped defaults merged into every device
//   - IOSXEDeviceGroupConfig — shared configuration for a selector-matched set
//   - IOSXETemplate          — parameterised fragments referenced from the above
//
// The schema carried by spec.configuration is intentionally unstructured
// (runtime.RawExtension) so netascode YAML under iosxe.devices[*].configuration
// can be pasted verbatim into a CR. Per-family validation is performed by
// the config driver at admit/plan time, not at CRD schema time.
//
// +kubebuilder:object:generate=true
// +groupName=config.cisco.vk
package v1alpha1
