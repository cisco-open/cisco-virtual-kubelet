// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

type fabricL2HandoffDeploymentIntent struct {
	handoff fabricL2HandoffIntent
	device  fabricDeviceIntent
}

// Keep the existing L2-only caller strict: mixed models must use the combined
// compiler so an L3 declaration can never be silently discarded.
func parseFabricL2HandoffDeployment(raw []byte) ([]fabricDeviceIntent, []fabricL2HandoffDeploymentIntent, error) {
	devices, l2, l3, err := parseFabricHandoffDeployment(raw)
	if err != nil {
		return nil, nil, err
	}
	if len(l3) != 0 {
		return nil, nil, errInvalidResponse
	}
	return devices, l2, nil
}
