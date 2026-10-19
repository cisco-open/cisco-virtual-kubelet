// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAUnmarkPruneIntent(t *testing.T) {
	for _, mode := range []string{"removed", "retain", "report", "still-marked", "replace", "missing-device", "missing-serial", "reprovision", "deleted", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			_, config := journalFixture(t)
			config.Spec.Mode = configapi.NetworkControllerApplyModeApply
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			raw := wiredInventoryModel()
			name := "edge"
			switch mode {
			case "retain":
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
			case "report":
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "still-marked":
				raw = rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`)
			case "replace":
				raw = rmaModel(`{"action":"REPLACE","replacement_serial_number":"SPARE"}`)
			case "missing-device":
				name = "absent"
			case "missing-serial":
				raw = strings.Replace(raw, `,"serial_number":"FOC12345678"`, "", 1)
			case "reprovision":
				raw = strings.Replace(raw, `"PROVISION"`, `"REPROVISION"`, 1)
			case "deleted":
				now := metav1.Now()
				config.DeletionTimestamp = &now
			case "malformed":
				raw = rmaModel(`null`)
			}
			ref, want, err := rmaUnmarkPruneIntent(config, &configSourceSnapshot{raw: []byte(raw)}, name)
			if (err == nil) != (mode == "removed") {
				t.Fatal("incorrect cleanup authorization", err)
			}
			if mode == "removed" && (ref.Serial != "FOC12345678" || want.site != "Global/Lab/Building" || want.action != rmaMark) {
				t.Fatal("lost removed-mark scope")
			}
		})
	}
}
