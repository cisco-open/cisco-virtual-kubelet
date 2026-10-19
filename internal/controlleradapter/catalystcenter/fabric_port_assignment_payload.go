// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "encoding/json"

// This mapper grants no submission authority. The caller must still establish
// fabric/VLAN/security-group dependencies, ownership and physical admission.
// One request is restricted to a single device and fabric, as required by the API.
func fabricPortAssignmentPayload(deviceID, fabricID string, wants []fabricPortAssignmentIntent, ports []lanLinkInterface) ([]byte, error) {
	if !validAPIID(deviceID) || !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	rows, err := expandFabricPortAssignments(wants)
	if err != nil || len(rows) == 0 {
		return nil, errInvalidResponse
	}
	type payload struct {
		Device         string  `json:"networkDeviceId"`
		Fabric         string  `json:"fabricId"`
		Interface      string  `json:"interfaceName"`
		Type           string  `json:"connectedDeviceType"`
		DataVLAN       *string `json:"dataVlanName,omitempty"`
		VoiceVLAN      *string `json:"voiceVlanName,omitempty"`
		SecurityGroup  *string `json:"securityGroupName,omitempty"`
		Authentication *string `json:"authenticateTemplateName,omitempty"`
		Description    *string `json:"interfaceDescription,omitempty"`
	}
	out := []payload{}
	bound := map[string]bool{}
	for _, row := range rows {
		port, err := bindLANLinkInterface(deviceID, *row.Interface, ports)
		if err != nil {
			return nil, err
		}
		if bound[port.ID] {
			return nil, errInvalidResponse
		}
		bound[port.ID] = true
		out = append(out, payload{deviceID, fabricID, port.PortName, row.Type, row.DataVLAN, row.VoiceVLAN, row.SecurityGroup, row.Authentication, row.Description})
	}
	return json.Marshal(out)
}
