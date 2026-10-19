//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestWirelessProfileRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, cohort := range []bool{false, true} {
		for _, mode := range []string{"complete", "no-dispatch"} {
			name := fmt.Sprintf("cohort-%t-%s", cohort, mode)
			t.Run(name, func(t *testing.T) {
				qualifyWirelessProfileManifestRestart(t, cohort, "", &wirelessProfileRecoveryQualification{mode: mode, target: "all", install: installWirelessFeatureQualificationAPI(t, api, "profile-recovery-"+name)})
			})
		}
	}
	for _, target := range []string{"profile", "site-tag", "policy-tag", "policy-cohort"} {
		for _, mode := range []string{"lost-ack", "child-failed", "tree-missing", "pending", "managed-drift", "source", "owner"} {
			name := target + "-" + mode
			t.Run(name, func(t *testing.T) {
				qualifyWirelessProfileManifestRestart(t, target == "policy-cohort", "", &wirelessProfileRecoveryQualification{mode: mode, target: target, install: installWirelessFeatureQualificationAPI(t, api, "profile-recovery-"+name)})
			})
		}
	}
}
