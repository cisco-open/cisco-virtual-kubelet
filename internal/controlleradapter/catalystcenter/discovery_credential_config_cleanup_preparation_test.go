// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

var credentialCleanupPreparationModes = []string{"inline", "configmap", "active", "missing-native", "native-drift", "foreign-uuid", "wrong-name", "source-race", "config-race", "journal-race", "projection-lost", "owner-revoked", "legacy", "missing-pair", "family-conflict", "future-generation", "foreign-config-name", "unknown-receipt"}

func TestDiscoveryCredentialConfigCleanupPreparation(t *testing.T) {
	for _, mode := range credentialCleanupPreparationModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, "prepare/"+mode, nil) })
	}
}
