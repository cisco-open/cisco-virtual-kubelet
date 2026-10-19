// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

var tagConfigDeletionModes = []string{"prune", "deletion-only", "retirement-refused", "retirement-lost-ack", "retirement-resurrection", "retirement-policy-retain", "retain", "source-gone", "inflight-update", "inflight-taskless", "member-device", "member-interface", "member-template", "unknown-type", "native-drift", "unknown-field-drift", "foreign-id", "protected", "external-absence", "missing-count", "list-error", "count-list-disagree", "lost-ack", "late-consumer", "old-task"}

func TestTagConfigDeletion(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined"} {
		for _, mode := range tagConfigDeletionModes {
			if mode == "source-gone" {
				t.Run(section+"/"+mode, func(t *testing.T) { exerciseTagPrune(t, section, "finalize-"+mode+"-configmap", nil) })
				continue
			}
			t.Run(section+"/"+mode, func(t *testing.T) { exerciseTagPrune(t, section, "finalize-"+mode, nil) })
		}
	}
}
