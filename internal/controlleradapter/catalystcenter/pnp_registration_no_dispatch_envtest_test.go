//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestPnPRegistrationNoDispatchReplan(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"read-error", "cohort", "source", "configmap", "owner", "operation", "inner-read-error", "inner-cohort", "inner-owner", "inner-operation", "inner-managed", "inner-network-error"} {
		t.Run(mode, func(t *testing.T) {
			exercisePnPRegistrationNoDispatch(t, mode, installWirelessFeatureQualificationAPI(t, api, "pnp-no-dispatch-"+mode))
		})
	}
}
