// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestPrivateSiteDesignComposition(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"normal-private", "optional-private", "image-private", "recover-private", "image-recover-private", "ambiguous-private", "image-ambiguous-private", "image-refuse-private", "recover-refuse-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "design-"+mode))
		})
	}
}

func TestEnvtestPlannedAPObservedIdentity(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"identity-replaced", "identity-missing", "identity-floor", "identity-pruned", "identity-replaced-private", "identity-missing-private", "identity-floor-private", "identity-pruned-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "ap-"+mode))
		})
	}
}

func TestEnvtestPlannedAPNativeState(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"native-unknown", "native-late", "native-sensor", "native-publish-late", "native-unknown-private", "native-late-private", "native-sensor-private", "native-publish-late-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "native-"+mode))
		})
	}
}

func TestEnvtestPlannedAPKeyedBaselines(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"keyed-private", "keyed-recover-private", "keyed-recover-refuse-private", "image-keyed-private", "keyed-native-sensor-private", "keyed-native-late-private", "keyed-drift-private", "keyed-geometry-private", "keyed-geometry-removed-private", "keyed-disable-private", "keyed-pruned-private", "keyed-result-drift-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "keyed-"+mode))
		})
	}
}

func TestEnvtestPlannedAPTypeUpdates(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"model-type", "model-type-recover", "model-type-omitted", "model-type-ignored", "model-type-incompatible", "model-type-keyed-private", "model-type-keyed-recover-private", "image-model-type-keyed-private", "model-type-keyed-ignored-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "type-"+mode))
		})
	}
}

func TestEnvtestPlannedAPRetainedBoundaries(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"same", "drift", "missing", "replaced", "renamed", "dispatch"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, "image-keyed-retained-"+mode+"-private", installWirelessFeatureQualificationAPI(t, api, "retained-"+mode))
		})
	}
}
