// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

var credentialCleanupExecutionModes = []string{"inline", "configmap", "active", "known-unknown", "lost-permit-ack", "lost-accepted-ack", "lost-receipt-ack", "receipt-conflict", "pruned-retirement", "ambiguous", "network-error", "failed-task", "pending-task", "foreign-task", "retained-native", "foreign-after-delete", "late-native-drift", "late-source", "after-source-loss", "after-source-change", "after-config-change", "permit-native-drift", "permit-source-race", "permit-journal-race", "projection-return", "after-source-identity-change", "legacy-preparation", "unsupported-version"}

func TestDiscoveryCredentialConfigCleanupExecution(t *testing.T) {
	for _, mode := range credentialCleanupExecutionModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, "execute/"+mode, nil) })
	}
}
