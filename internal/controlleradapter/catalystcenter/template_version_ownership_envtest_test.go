// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestTemplateVersionOwnership(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"commit", "composite-commit"} {
		for _, mode := range templateVersionPublicModes {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseTemplateNoDispatchReplan(t, stage, "applied-"+mode, installWirelessFeatureQualificationAPI(t, api, "template-version-"+stage+"-"+mode))
			})
		}
	}
}
