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

// XRConfig contains IOS XR specific management and appmgr settings.
type XRConfig struct {
	// Management configures the IOS XR SSH CLI channel used for appmgr,
	// operations, troubleshooting, and observability.
	// +kubebuilder:validation:Optional
	Management *XRManagementConfig `json:"management,omitempty" mapstructure:"management,omitempty"`

	// AppHosting configures IOS XR appmgr defaults for Docker applications.
	// +kubebuilder:validation:Optional
	AppHosting *XRAppHostingConfig `json:"appHosting,omitempty" mapstructure:"appHosting,omitempty"`

	// Resources overrides the virtual router resource shape advertised to
	// Kubernetes.
	// +kubebuilder:validation:Optional
	Resources *XRResourceConfig `json:"resources,omitempty" mapstructure:"resources,omitempty"`
}

// XRManagementConfig describes the IOS XR management transport.
type XRManagementConfig struct {
	// SSHPort is the IOS XR CLI SSH port. If omitted, spec.port is used, then 22.
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

	// CommandTimeoutSeconds bounds a single IOS XR CLI command.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=180
	CommandTimeoutSeconds int `json:"commandTimeoutSeconds,omitempty" mapstructure:"commandTimeoutSeconds,omitempty"`
}

// XRAppHostingConfig configures IOS XR appmgr Docker behavior.
type XRAppHostingConfig struct {
	// DefaultSource is used when a pod container image does not point to an
	// appmgr RPM and no source can be inferred. The source must already be
	// present in `show appmgr source-table`.
	// +kubebuilder:validation:Optional
	DefaultSource string `json:"defaultSource,omitempty" mapstructure:"defaultSource,omitempty"`

	// DefaultRunOptions are appended to each appmgr docker-run-opts string
	// before pod labels and environment variables. The default is
	// ["--network host"].
	// +kubebuilder:validation:Optional
	DefaultRunOptions []string `json:"defaultRunOptions,omitempty" mapstructure:"defaultRunOptions,omitempty"`

	// PackageInstallPath is the IOS XR filesystem prefix used when a pod image
	// names only an RPM basename. Defaults to /harddisk:.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="/harddisk:"
	PackageInstallPath string `json:"packageInstallPath,omitempty" mapstructure:"packageInstallPath,omitempty"`

	// EnableDockerCommand starts the IOS XR docker daemon before appmgr
	// operations.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="run systemctl start docker"
	EnableDockerCommand string `json:"enableDockerCommand,omitempty" mapstructure:"enableDockerCommand,omitempty"`
}

// XRResourceConfig overrides resources advertised for the IOS XR node.
type XRResourceConfig struct {
	// CPUCores is the number of vCPU cores assigned to the virtual router.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=4
	CPUCores int64 `json:"cpuCores,omitempty" mapstructure:"cpuCores,omitempty"`

	// MemoryMB is the amount of memory assigned to the virtual router.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=16384
	MemoryMB int64 `json:"memoryMB,omitempty" mapstructure:"memoryMB,omitempty"`

	// StorageMB is the app-hosting storage capacity reported for scheduling.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1500
	StorageMB int64 `json:"storageMB,omitempty" mapstructure:"storageMB,omitempty"`
}
