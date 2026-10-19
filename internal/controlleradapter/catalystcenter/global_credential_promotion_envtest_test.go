//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestGlobalCredentialMetadataPromotion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseGlobalCredentialMetadataPromotion(t, installWirelessFeatureQualificationAPI(t, api, "snmp-metadata-promotion"))
}
