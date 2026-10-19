// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// ValidateNetworkSettingsSource reuses the existing strict offline compiler.
// It performs no native reads and grants no ownership, submission or parity.
func ValidateNetworkSettingsSource(raw []byte) error {
	_, err := (&client{}).compileConfigurationApply(raw, []string{"network_settings"}, nil)
	return err
}
