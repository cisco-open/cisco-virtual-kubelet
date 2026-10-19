//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestProfileSitePrune(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, wireless := range []bool{false, true} {
		modes := append([]string{}, profileSitePruneModes...)
		if wireless {
			modes = append(modes, profileSiteWirelessPruneModes...)
		}
		for _, mode := range modes {
			t.Run(fmt.Sprintf("wireless-%t/%s", wireless, mode), func(t *testing.T) {
				exerciseProfileSitePrune(t, wireless, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("prune-%t-%s", wireless, mode)))
			})
		}
	}
}
