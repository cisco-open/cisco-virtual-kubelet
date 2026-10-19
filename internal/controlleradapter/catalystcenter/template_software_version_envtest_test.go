// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestTemplateSoftwareVersionPublicNoOp(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"inherited", "cleared", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			exerciseTemplateMetadataPublic(t, mode, installWirelessFeatureQualificationAPI(t, api, "template-version-setting-"+mode))
		})
	}
}
