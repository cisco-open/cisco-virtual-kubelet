//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestFloorImageOmissionReintroduction(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"omit", "null", "legacy", "selected-omit", "pending-operation", "foreign-owner", "stale-source", "replaced-floor", "cas-conflict"} {
		t.Run(mode, func(t *testing.T) {
			exerciseFloorImageOmission(t, mode, installWirelessFeatureQualificationAPI(t, api, "image-lifetime-"+mode))
		})
	}
}
