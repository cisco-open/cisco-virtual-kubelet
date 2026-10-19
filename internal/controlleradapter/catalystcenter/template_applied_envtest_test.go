// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestTemplateAppliedState(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range templateAppliedStages {
		for _, mode := range templateAppliedModes {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseTemplateNoDispatchReplan(t, stage, "applied-"+mode, installWirelessFeatureQualificationAPI(t, api, "template-latest-"+stage+"-"+mode))
			})
		}
	}
}
