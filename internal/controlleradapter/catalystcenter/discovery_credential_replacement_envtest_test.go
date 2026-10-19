// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialReplacement(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "configmap", "known-unknown", "lost-accepted-ack", "lost-receipt-ack", "conflict", "pruned-retirement", "ambiguous-delete", "lost-permit-ack", "retained-native", "foreign-native", "failed-task", "capacity", "source-change", "source-loss-successor", "delete-source-complete", "delete-source-lost-receipt-ack", "delete-source-conflict", "delete-source-ambiguous-delete", "delete-source-retained-native", "delete-source-foreign-native", "delete-source-failed-task"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialReplacementScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "discovery-replace-"+mode))
		})
	}
}
