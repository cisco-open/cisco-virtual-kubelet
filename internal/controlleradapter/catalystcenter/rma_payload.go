// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const deviceReplacementPath = "/dna/intent/api/v1/device-replacement"
const deviceReplacementWorkflowPath = deviceReplacementPath + "/workflow"

// Native wire mapping follows CiscoDevNet provider v0.6.1. These builders do
// not submit requests: physical fencing, retained ownership and workflow
// receipt/recovery must be established by the caller before any mutation.
func deviceReplacementMarkPayload(deviceID string) ([]byte, error) {
	if !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	return json.Marshal([]struct {
		DeviceID string `json:"faultyDeviceId"`
		Status   string `json:"replacementStatus"`
	}{{deviceID, "MARKED-FOR-REPLACEMENT"}})
}

func deviceReplacementWorkflowPayload(faultySerial, replacementSerial string) ([]byte, error) {
	faulty, err := topologyidentity.CanonicalPhysicalIdentity(faultySerial)
	if err != nil {
		return nil, errInvalidResponse
	}
	replacement, err := topologyidentity.CanonicalPhysicalIdentity(replacementSerial)
	if err != nil || faulty == replacement {
		return nil, errInvalidResponse
	}
	// Validate canonically but retain the operator/native serial spelling on wire.
	return json.Marshal(struct {
		Faulty      string `json:"faultyDeviceSerialNumber"`
		Replacement string `json:"replacementDeviceSerialNumber"`
	}{faultySerial, replacementSerial})
}
