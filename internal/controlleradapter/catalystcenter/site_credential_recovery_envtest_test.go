//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSelectedSiteCredentialUnknownRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	siteCredentialManifestCycle(t, false, siteCredentialManifestQualification{selected: true, recover: true, install: installWirelessFeatureQualificationAPI(t, api, "site-credential-recovery")})
}
