// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controlleradapter

import (
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ConfigurationMetadataSource is the manager-read non-Secret model revision.
// Callbacks must not retain or log Raw, which may contain sensitive intent.
type ConfigurationMetadataSource struct {
	Raw          []byte
	UID, Version string
}

func (ConfigurationMetadataSource) String() string   { return "<configuration model source>" }
func (ConfigurationMetadataSource) GoString() string { return "<configuration model source>" }

// ConfigurationMetadataScope is an optional pure product projection. It binds
// an exact prepared metadata operation to the complete owned physical-identity
// cohort before a manager performs shared uncached scope observations. It does
// not construct an adapter, read credentials, call a product API, authorize an
// actor or grant dispatch. Nil leaves existing products and operations unchanged.
type ConfigurationMetadataScope struct {
	// Settlement validates a terminal original outcome and reconstructs its
	// pre-submission ledger. Nil disables product settlement; no API calls.
	// Cancellation validates original ready Prepared ownership without reading
	// current intent or minting a terminal proof. Nil disables cancellation.
	Cancellation       func(*cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error)
	Settlement         func(*cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error)
	Selected           func(*cvk.NetworkController) bool
	PhysicalIdentities func(*cvk.NetworkController, *config.NetworkControllerConfig, ConfigurationMetadataSource) ([]string, error)
}

func ConfigurationMetadataScopeFor(typeName string) *ConfigurationMetadataScope {
	registration, ok := Lookup(typeName)
	if !ok || registration.ConfigurationMetadataScope == nil {
		return nil
	}
	copy := *registration.ConfigurationMetadataScope
	return &copy
}
