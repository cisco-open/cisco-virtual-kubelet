//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteRecreationExecution(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "latest", "unknown", "pending", "source", "history", "lost-ack", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			exerciseSiteRetirementBinding(t, "write-recreate-"+mode, true, installWirelessFeatureQualificationAPI(t, api, "site-recreate-"+mode))
		})
	}
}
