//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestEnvtestWirelessAcceptedMetadataCreates(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	entries := append(append([]definitionRecoveryCase{}, definitionRecoveryCases...), wirelessAPDispatchCase)
	for _, entry := range entries {
		for _, mode := range []string{"known", "child-failed", "child-pending", "child-foreign", "tree-unavailable", "changed-source"} {
			t.Run(entry.name+"/"+mode, func(t *testing.T) {
				exerciseWirelessDefinitionRecoveryPublic(t, entry, mode, installWirelessFeatureQualificationAPI(t, api, "accepted-"+strings.ReplaceAll(entry.name, "_", "-")+"-"+mode), true)
			})
		}
	}
}
