//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestTagRecreationPublicCycles(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for si, section := range []string{"inventory", "templates", "combined"} {
		for mi, mode := range []string{"recreate", "recreate-twice", "recreate-foreign", "recreate-resurrected", "recreate-reused-id", "recreate-missing-cohort", "recreate-missing-archive", "recreate-tampered-archive"} {
			t.Run(section+"/"+mode, func(t *testing.T) {
				exerciseTagPrune(t, section, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("tag-recreate-%d-%d", si, mi)))
			})
		}
	}
}
