//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestDiscoveryCredentialNoDispatchProof(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for i, kind := range credentialNoDispatchKinds {
		for j, mode := range credentialNoDispatchModes {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseCredentialNoDispatchProof(t, kind, mode, installWirelessFeatureQualificationAPI(t, api, fmt.Sprintf("credential-no-dispatch-%d-%d", i, j)))
			})
		}
	}
}
