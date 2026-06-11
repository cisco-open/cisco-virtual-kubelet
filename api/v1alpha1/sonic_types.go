// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

// SONICConfig contains Cisco SONiC specific settings. SONiC support is
// OpenConfig-first: CVK uses gNMI/OpenConfig for configuration, telemetry, and
// health while deliberately advertising no Cisco app-hosting capacity.
type SONICConfig struct {
	// OpenConfig configures the gNMI/OpenConfig management channel.
	// +kubebuilder:validation:Optional
	OpenConfig *SONICOpenConfig `json:"openConfig,omitempty" mapstructure:"openConfig,omitempty"`

	// Management configures the optional SSH troubleshooting channel. gNMI is the
	// source of truth for health and configuration; SSH is used only to enrich
	// device info and run read-only operational commands.
	// +kubebuilder:validation:Optional
	Management *SONICManagementConfig `json:"management,omitempty" mapstructure:"management,omitempty"`

	// Resources overrides the virtual appliance resource shape reported to
	// Kubernetes for observability. Pods remain zero-capacity because SONiC does
	// not expose Cisco app-hosting.
	// +kubebuilder:validation:Optional
	Resources *SONICResourceConfig `json:"resources,omitempty" mapstructure:"resources,omitempty"`
}

// SONICOpenConfig describes the gNMI endpoint used for OpenConfig operations.
type SONICOpenConfig struct {
	// GNMIPort is the gNMI listener port. If omitted, spec.port is used, then
	// 57400. Lab SONiC images commonly proxy gNMI to 57400 without TLS.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=57400
	GNMIPort int `json:"gnmiPort,omitempty" mapstructure:"gnmiPort,omitempty"`

	// TLS enables TLS for gNMI. The default is false for the SONiC VXR images
	// used by the lab; set true for production devices with secure gNMI.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	TLS bool `json:"tls,omitempty" mapstructure:"tls,omitempty"`

	// InsecureSkipVerify disables gNMI server certificate verification when TLS
	// is enabled. This should remain false for production unless a lab image uses
	// self-signed certificates.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" mapstructure:"insecureSkipVerify,omitempty"`

	// RequestTimeoutSeconds bounds a single gNMI request.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=30
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds,omitempty" mapstructure:"requestTimeoutSeconds,omitempty"`
}

// SONICManagementConfig describes optional SSH operational access.
type SONICManagementConfig struct {
	// SSHPort is the SONiC CLI SSH port. If omitted, 22 is used.
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

	// CommandTimeoutSeconds bounds a single SONiC operational command.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=600
	// +kubebuilder:default=45
	CommandTimeoutSeconds int `json:"commandTimeoutSeconds,omitempty" mapstructure:"commandTimeoutSeconds,omitempty"`
}

// SONICResourceConfig overrides the resources advertised for the SONiC node.
type SONICResourceConfig struct {
	// CPUCores is the number of vCPU cores assigned to the virtual switch.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=4
	CPUCores int64 `json:"cpuCores,omitempty" mapstructure:"cpuCores,omitempty"`

	// MemoryMB is the amount of memory assigned to the virtual switch.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=20480
	MemoryMB int64 `json:"memoryMB,omitempty" mapstructure:"memoryMB,omitempty"`

	// StorageMB is the storage capacity reported for observability. It is not
	// used for workload scheduling because SONiC app-hosting is unsupported.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	StorageMB int64 `json:"storageMB,omitempty" mapstructure:"storageMB,omitempty"`
}
