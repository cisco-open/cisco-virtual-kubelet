// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controlleradapter

import (
	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ConfigurationLifecycle is an optional metadata-only manager/worker contract.
// Callbacks must be pure: no clients, credentials, product APIs or factory calls.
// The manager alone patches finalizers. A product worker proves completion using
// its existing status/journal protocol, without gaining config metadata writes.
// Nil preserves existing adapter behavior.
type ConfigurationLifecycle struct {
	Finalizer string
	Eligible  func(*cvk.NetworkController, *config.NetworkControllerConfig) bool
	Completed func(*cvk.NetworkController, *config.NetworkControllerConfig) bool
}

func ConfigurationLifecycleFor(typeName string) *ConfigurationLifecycle {
	registration, ok := Lookup(typeName)
	if !ok || registration.ConfigurationLifecycle == nil {
		return nil
	}
	copy := *registration.ConfigurationLifecycle
	return &copy
}

// ConfigurationLifecyclesFor returns independent metadata-only lifecycle
// families, legacy policy first. The returned slice never aliases the registry.
func ConfigurationLifecyclesFor(typeName string) []ConfigurationLifecycle {
	registration, ok := Lookup(typeName)
	if !ok {
		return nil
	}
	var policies []ConfigurationLifecycle
	if registration.ConfigurationLifecycle != nil {
		policies = append(policies, *registration.ConfigurationLifecycle)
	}
	return append(policies, registration.ConfigurationLifecycles...)
}
