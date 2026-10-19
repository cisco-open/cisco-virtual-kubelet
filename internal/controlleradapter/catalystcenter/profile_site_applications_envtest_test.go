//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestProfileSiteApplications(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, wireless := range []bool{false, true} {
		for _, mode := range profileSiteReceiptModes {
			name := fmt.Sprintf("wireless-%t-%s", wireless, mode)
			t.Run(name, func(t *testing.T) {
				exerciseProfileSiteRecovery(t, wireless, "complete", installWirelessFeatureQualificationAPI(t, api, "application-"+name), &profileSiteReceiptQualification{mode: mode})
			})
		}
	}
}
