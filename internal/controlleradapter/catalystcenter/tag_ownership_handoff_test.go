// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

var tagHandoffModes = []string{"handoff", "handoff-configmap", "handoff-to-inventory", "handoff-to-templates", "handoff-to-combined", "handoff-same-name", "handoff-repeated", "handoff-old-present", "handoff-missing-retirement", "handoff-resurrection", "handoff-missing-carrier", "handoff-missing-history", "handoff-corrupt-history", "handoff-reused-id"}

func TestTagOwnershipHandoff(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined"} {
		for _, mode := range tagHandoffModes {
			t.Run(section+"/"+mode, func(t *testing.T) { exerciseTagPrune(t, section, "finalize-"+mode, nil) })
		}
	}
}
