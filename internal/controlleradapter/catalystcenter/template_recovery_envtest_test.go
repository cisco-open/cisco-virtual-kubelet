// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestTemplateKnownTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range templateNoDispatchStages {
		for _, mode := range templateRecoveryModes {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseTemplateNoDispatchReplan(t, stage, "recovery-"+mode, installWirelessFeatureQualificationAPI(t, api, "template-task-"+stage+"-"+mode))
			})
		}
	}
}
