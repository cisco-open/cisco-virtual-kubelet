//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestTagPrunePublicLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for si, section := range []string{"inventory", "templates", "combined"} {
		for mi, mode := range tagPruneModes {
			t.Run(section+"/"+mode, func(t *testing.T) {
				exerciseTagPrune(t, section, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("tag-prune-%d-%d", si, mi)))
			})
		}
	}
}
