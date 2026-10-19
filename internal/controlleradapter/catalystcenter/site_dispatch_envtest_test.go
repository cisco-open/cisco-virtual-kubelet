// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"strings"
	"testing"
)

func TestEnvtestSiteSingularDispatch(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"area-create", "building-create", "floor-create", "building-update", "floor-update"} {
		for _, mode := range []string{"native-edit", "source-edit", "owner-edit", "lost-ack"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseSiteSingularPublic(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "site-dispatch-"+stage+"-"+mode))
			})
		}
	}
}

func TestEnvtestSiteSingularTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"area-create", "building-create", "floor-create", "building-update", "floor-update"} {
		for _, mode := range []string{"recover", "recover-pending", "recover-failed", "recover-foreign", "recover-unavailable", "recover-source", "recover-owner", "recover-readback", "recover-missing-result", "recover-foreign-result"} {
			if strings.HasSuffix(stage, "update") && strings.HasSuffix(mode, "result") {
				continue
			}
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseSiteSingularPublic(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "site-task-"+stage+"-"+mode))
			})
		}
	}
}

func TestEnvtestSiteCreatorIdentityFence(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"area-create", "building-create", "floor-create"} {
		for _, mode := range []string{"creator-replaced", "creator-missing"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseSiteSingularPublic(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "creator-identity-"+stage+"-"+mode))
			})
		}
	}
}

func TestEnvtestSitePrivateCreatorBaseline(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"area-create", "building-create", "floor-create"} {
		for _, mode := range []string{"private-known", "private-native-edit", "private-key-change", "private-option-loss", "recover-private"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseSiteSingularPublic(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "site-private-"+stage+"-"+mode))
			})
		}
	}
}
