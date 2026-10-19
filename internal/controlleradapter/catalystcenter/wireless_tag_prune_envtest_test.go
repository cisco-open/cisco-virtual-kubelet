//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestWirelessTagPrune(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, cohort := range []bool{false, true} {
		for _, mode := range wirelessTagPruneModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) {
				exerciseWirelessTagReceipts(t, cohort, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("tag-prune-%t-%s", cohort, mode)))
			})
		}
	}
}

func TestEnvtestWirelessTagRecreationCohorts(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, cohort := range []bool{false, true} {
		t.Run(fmt.Sprintf("cohort-%t", cohort), func(t *testing.T) {
			exerciseWirelessTagReceipts(t, cohort, "membership-prune-complete", installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("tag-recreation-cohort-%t", cohort)))
		})
	}
}

func TestEnvtestWirelessTagRecreationExecution(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, cohort := range []bool{false, true} {
		for _, mode := range wirelessTagRecreationExecutionModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) {
				if !cohort && mode == "membership-prune-recreate-partial" {
					t.Skip("partial bulk producer requires a cohort")
				}
				exerciseWirelessTagReceipts(t, cohort, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("tag-recreation-%t-%s", cohort, mode)))
			})
		}
	}
}
