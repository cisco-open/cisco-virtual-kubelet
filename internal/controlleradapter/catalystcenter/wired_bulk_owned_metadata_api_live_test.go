// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest && live

package catalystcenter

import (
	"os"
	"testing"
)

func TestLivePrivateAPIOwnedMetadataComposition(t *testing.T) {
	if os.Getenv("CATC_OWNED_METADATA_API_EXECUTION") != "private-api-native-unassigned-credential" {
		t.Skip("private API/native metadata composition not enabled")
	}
	if os.Getenv("CATC_WIRED_BULK_OWNED_METADATA_EXECUTION") != "native-public-manifest-simulated-kubernetes" {
		t.Fatal("owned metadata dependency checks must be enabled")
	}
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	qualifyLiveGlobalCredentialNoAuthManifest(t, api)
	t.Log("actual private API persistence and native public-manifest producer/dependency receipt consumption composed; reconciler is invoked by test process, not installed workers")
}
