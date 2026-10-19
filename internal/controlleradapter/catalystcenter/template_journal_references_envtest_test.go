// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestRetainedTemplateDeploymentReferences(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"composite-member", "same", "empty", "deleted-source", "source-edited", "owner-removed", "late-journal", "journal-cleared", "late-version-alias", "target-replaced", "unavailable-history", "controller-paused"} {
		t.Run(mode, func(t *testing.T) {
			exerciseRetainedTemplateReferences(t, mode, installWirelessFeatureQualificationAPI(t, api, "template-journal-"+mode))
		})
	}
}
