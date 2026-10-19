// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestTemplateCreatorPrivateBaseline(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"project-create", "draft-create", "composite-draft"} {
		for _, mode := range templateBaselineModes {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseTemplateNoDispatchReplan(t, stage, "baseline-"+mode, installWirelessFeatureQualificationAPI(t, api, "template-mac-"+stage+"-"+mode))
			})
		}
	}
}
