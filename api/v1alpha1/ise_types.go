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

// ISEConfig contains Cisco Identity Services Engine specific settings.
// ISE support is health, operations, telemetry, and declarative
// configuration-first: the platform does not expose Cisco app-hosting, so
// workloads are not scheduled onto ISE nodes.
type ISEConfig struct {
	// Management configures the CLI management channel used for health,
	// troubleshooting, and topology collection.
	// +kubebuilder:validation:Optional
	Management *ISEManagementConfig `json:"management,omitempty" mapstructure:"management,omitempty"`

	// API configures the ISE REST/ERS channel used by ISEConfig reconciliation.
	// +kubebuilder:validation:Optional
	API *ISEAPIConfig `json:"api,omitempty" mapstructure:"api,omitempty"`

	// Resources overrides the virtual appliance resource shape reported to
	// Kubernetes for observability. Defaults match the lab KubeVirt ISE profile.
	// +kubebuilder:validation:Optional
	Resources *ISEResourceConfig `json:"resources,omitempty" mapstructure:"resources,omitempty"`
}

// ISEManagementConfig describes the ISE management CLI transport.
type ISEManagementConfig struct {
	// SSHPort is the ISE CLI SSH port. If omitted, spec.port is used, then 22.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=22
	SSHPort int `json:"sshPort,omitempty" mapstructure:"sshPort,omitempty"`

	// ConnectTimeoutSeconds bounds TCP and SSH handshake setup.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	// +kubebuilder:default=20
	ConnectTimeoutSeconds int `json:"connectTimeoutSeconds,omitempty" mapstructure:"connectTimeoutSeconds,omitempty"`

	// CommandTimeoutSeconds bounds a single ISE CLI command.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=120
	CommandTimeoutSeconds int `json:"commandTimeoutSeconds,omitempty" mapstructure:"commandTimeoutSeconds,omitempty"`
}

// ISEAPIConfig describes the REST endpoint used for NetAsCode ISE resources.
type ISEAPIConfig struct {
	// BaseURL overrides the generated API endpoint, for example
	// https://ise.example.com:9060. When empty, the driver builds one from
	// spec.address and Port.
	// +kubebuilder:validation:Optional
	BaseURL string `json:"baseUrl,omitempty" mapstructure:"baseUrl,omitempty"`

	// Port is the HTTPS API/ERS port. If omitted, spec.port is used, then 443.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=443
	Port int `json:"port,omitempty" mapstructure:"port,omitempty"`

	// RequestTimeoutSeconds bounds a single REST/ERS request.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=60
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds,omitempty" mapstructure:"requestTimeoutSeconds,omitempty"`

	// InsecureSkipVerify disables TLS certificate verification. Lab ISE images
	// commonly start with a self-signed certificate, so the default is true.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" mapstructure:"insecureSkipVerify,omitempty"`
}

// ISEResourceConfig overrides the resources advertised for the ISE node.
type ISEResourceConfig struct {
	// CPUCores is the number of vCPU cores assigned to the virtual appliance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=16
	CPUCores int64 `json:"cpuCores,omitempty" mapstructure:"cpuCores,omitempty"`

	// MemoryMB is the amount of memory assigned to the virtual appliance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=32768
	MemoryMB int64 `json:"memoryMB,omitempty" mapstructure:"memoryMB,omitempty"`

	// StorageMB is the storage capacity reported for observability. It is not
	// used for workload scheduling because ISE app-hosting is unsupported.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=300000
	StorageMB int64 `json:"storageMB,omitempty" mapstructure:"storageMB,omitempty"`
}
