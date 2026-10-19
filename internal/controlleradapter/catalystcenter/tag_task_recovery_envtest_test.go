//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestTagKnownTaskPublicRestart(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, section := range []string{"inventory", "templates", "combined", "inventory-configmap", "templates-configmap", "combined-configmap"} {
		for _, update := range []bool{false, true} {
			name := fmt.Sprintf("tag-recovery-%s-%t", section, update)
			t.Run(name, func(t *testing.T) {
				exerciseTagKnownTaskRestart(t, section, update, installWirelessFeatureQualificationAPI(t, api, name))
			})
		}
	}
}
