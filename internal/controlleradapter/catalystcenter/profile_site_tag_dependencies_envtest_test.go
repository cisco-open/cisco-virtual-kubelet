//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestProfileSiteTagDependencies(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, cohort := range []bool{false, true} {
		for _, mode := range profileSiteTagDependencyModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) {
				exerciseWirelessTagReceipts(t, cohort, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("tag-dependency-%t-%s", cohort, mode)))
			})
		}
	}
}
