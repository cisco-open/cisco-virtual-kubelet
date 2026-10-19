// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Public journal bookkeeping only. Product adapters normalize their active
// result identity in a copy to avoid hashing a seal into itself. Native values
// and credentials do not belong in this structure or its digest.
func configurationBookkeepingDigest(state *cvk.NetworkControllerConfigurationStatus, normalize func(*cvk.NetworkControllerConfigurationActionApplication)) string {
	if state == nil {
		state = &cvk.NetworkControllerConfigurationStatus{}
	}
	copy := state.DeepCopy()
	copy.Operation = nil
	if normalize != nil {
		for i := range copy.ActionApplications {
			normalize(&copy.ActionApplications[i])
		}
	}
	raw, _ := json.Marshal(copy)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
