// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

// FMCConfig contains Cisco Secure Firewall Management Center specific settings.
// FMC support is controller/API-first: the platform does not expose Cisco
// app-hosting, so workloads are not scheduled onto FMC nodes, but health,
// operations, telemetry, and Network as Code configuration reconciliation are
// available through the FMC REST API.
type FMCConfig struct {
	// API configures the FMC REST channel used by health checks and FMCConfig reconciliation.
	// +kubebuilder:validation:Optional
	API *FMCAPIConfig `json:"api,omitempty" mapstructure:"api,omitempty"`

	// Resources overrides the virtual appliance resource shape reported to
	// Kubernetes for observability. Defaults match the lab KubeVirt FMCv profile.
	// +kubebuilder:validation:Optional
	Resources *FMCResourceConfig `json:"resources,omitempty" mapstructure:"resources,omitempty"`
}

// FMCAPIConfig describes the FMC REST endpoint used for health and NetAsCode resources.
type FMCAPIConfig struct {
	// BaseURL overrides the generated API endpoint, for example
	// https://fmc.example.com. When empty, the driver builds one from
	// spec.address and Port.
	// +kubebuilder:validation:Optional
	BaseURL string `json:"baseUrl,omitempty" mapstructure:"baseUrl,omitempty"`

	// Port is the HTTPS API port. If omitted, spec.port is used, then 443.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=443
	Port int `json:"port,omitempty" mapstructure:"port,omitempty"`

	// RequestTimeoutSeconds bounds a single REST request.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	// +kubebuilder:default=60
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds,omitempty" mapstructure:"requestTimeoutSeconds,omitempty"`

	// InsecureSkipVerify disables TLS certificate verification. Lab FMCv images
	// commonly start with a self-signed certificate, so the default is true.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty" mapstructure:"insecureSkipVerify,omitempty"`

	// DomainUUID pins reconciliation to a specific FMC domain UUID. If omitted,
	// the driver resolves DomainName from the authenticated domains and defaults
	// to Global.
	// +kubebuilder:validation:Optional
	DomainUUID string `json:"domainUuid,omitempty" mapstructure:"domainUuid,omitempty"`

	// DomainName is the FMC domain name to use when DomainUUID is omitted.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=Global
	DomainName string `json:"domainName,omitempty" mapstructure:"domainName,omitempty"`
}

// FMCResourceConfig overrides the resources advertised for the FMC node.
type FMCResourceConfig struct {
	// CPUCores is the number of vCPU cores assigned to the virtual appliance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=4
	CPUCores int64 `json:"cpuCores,omitempty" mapstructure:"cpuCores,omitempty"`

	// MemoryMB is the amount of memory assigned to the virtual appliance.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=32768
	MemoryMB int64 `json:"memoryMB,omitempty" mapstructure:"memoryMB,omitempty"`

	// StorageMB is the storage capacity reported for observability. It is not
	// used for workload scheduling because FMC app-hosting is unsupported.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=250000
	StorageMB int64 `json:"storageMB,omitempty" mapstructure:"storageMB,omitempty"`
}
