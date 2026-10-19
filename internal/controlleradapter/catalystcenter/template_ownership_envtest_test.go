// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestTemplateCreatorOwnership(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"project-create", "draft-create", "composite-draft"} {
		for _, mode := range templateOwnershipModes {
			if stage == "project-create" && mode == "kind-changed" {
				continue
			}
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseTemplateNoDispatchReplan(t, stage, "ownership-"+mode, installWirelessFeatureQualificationAPI(t, api, "template-owner-"+stage+"-"+mode))
			})
		}
	}
}

func TestEnvtestTemplateCreatorInlineSource(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseTemplateNoDispatchReplan(t, "project-create", "ownership-inline", installWirelessFeatureQualificationAPI(t, api, "template-inline-owner"))
}
