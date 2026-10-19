// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// Bind an explicitly declared discovery set to known PnP records. A stack must
// be declared in full. This does not prove authentication/readiness, prevent
// other DHCP-discovered devices, or authorize a reset/claim/start operation.
func bindLANDiscoveryDevices(want lanSessionIntent, pnp []pnpDevice, inventory []Device) ([]pnpDevice, error) {
	metadata := want
	metadata.Password = ""
	if err := validateLANSessionIntent(metadata, false); err != nil {
		return nil, err
	}
	if len(want.Devices) == 0 {
		return nil, errConfigurationDeviceAdmission
	}
	serials := make([]string, 0, len(want.Devices))
	for _, device := range want.Devices {
		serials = append(serials, device.Serial)
	}
	return bindPnPPhysicalDevices(serials, pnp, inventory)
}
