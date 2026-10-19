// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// Bind endpoint IP selectors to two distinct, globally unambiguous native
// serial/UUID identities. Interface existence, link state and pool ownership
// remain separate prerequisites; this does not authorize the link operation.
func bindLANLinkDevices(want lanLinkIntent, inventory []Device) ([]Device, error) {
	if err := want.validate(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, 2)
	for _, ip := range []string{want.primaryIP, want.secondaryIP} {
		device, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			return nil, err
		}
		ids = append(ids, device.ID)
	}
	devices, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	if len(devices) != 2 {
		return nil, errConfigurationDeviceAdmission
	}
	return devices, nil
}
