// Copyright (c) 2026 Cisco Systems Inc.
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

// FTDConfig contains Cisco Secure Firewall Threat Defense specific settings.
// FTD support is intentionally health/operations/telemetry-first: this
// platform does not expose Cisco app-hosting, so workloads are not scheduled
// onto FTD nodes.
type FTDConfig struct {
	// Management configures the CLI management channel used for health,
	// troubleshooting, and topology collection.
	// +kubebuilder:validation:Optional
	Management *FTDManagementConfig `json:"management,omitempty" mapstructure:"management,omitempty"`

	// Resources overrides the virtual appliance resource shape reported to
	// Kubernetes for observability. Defaults match the lab KubeVirt FTD profile.
	// +kubebuilder:validation:Optional
	Resources *FTDResourceConfig `json:"resources,omitempty" mapstructure:"resources,omitempty"`
}

// FTDManagementConfig describes the FTD management transport.
type FTDManagementConfig struct {
	// SSHPort is the FTD CLI SSH port. If omitted, spec.port is used, then 22.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=22
	SSHPort int `json:"sshPort,omitempty" mapstructure:"sshPort,omitempty"`

	// ConnectTimeoutSeconds bounds TCP and SSH handshake setup.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	// +kubebuilder:default=15
	ConnectTimeoutSeconds int `json:"connectTimeoutSeconds,omitempty" mapstructure:"connectTimeoutSeconds,omitempty"`

	// CommandTimeoutSeconds bounds a single FTD CLI command.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=600
	// +kubebuilder:default=45
	CommandTimeoutSeconds int `json:"commandTimeoutSeconds,omitempty" mapstructure:"commandTimeoutSeconds,omitempty"`
}

// FTDResourceConfig overrides the resources advertised for the FTD node.
type FTDResourceConfig struct {
	// CPUCores is the number of vCPU cores assigned to the virtual appliance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=4
	CPUCores int64 `json:"cpuCores,omitempty" mapstructure:"cpuCores,omitempty"`

	// MemoryMB is the amount of memory assigned to the virtual appliance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=8192
	MemoryMB int64 `json:"memoryMB,omitempty" mapstructure:"memoryMB,omitempty"`

	// StorageMB is the storage capacity reported for observability. It is not
	// used for workload scheduling because FTD app-hosting is unsupported.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	StorageMB int64 `json:"storageMB,omitempty" mapstructure:"storageMB,omitempty"`
}
