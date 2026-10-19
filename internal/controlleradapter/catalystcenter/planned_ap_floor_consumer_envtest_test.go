// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestPlannedAPFloorConsumers(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"keyed-floor-consumer-private", "keyed-floor-consumer-recover-private", "keyed-floor-consumer-drift-private", "keyed-floor-consumer-missing-private", "keyed-floor-consumer-keyed-result-drift-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "consumer-"+mode))
		})
	}
}
