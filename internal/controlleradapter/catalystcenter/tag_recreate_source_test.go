// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

var tagProjectedLifecycleModes = []string{"prune-configmap", "recreate-twice-configmap", "late-consumer-configmap", "old-task-configmap"}

func TestTagProjectedPruneAndRecreation(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined"} {
		for _, mode := range tagProjectedLifecycleModes {
			t.Run(section+"/"+mode, func(t *testing.T) { exerciseTagPrune(t, section, mode, nil) })
		}
	}
}
