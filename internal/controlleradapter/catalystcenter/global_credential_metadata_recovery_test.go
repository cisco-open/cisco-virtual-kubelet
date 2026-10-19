// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func metadataRecoveryFixture(t *testing.T, update bool) (*globalCredentialExecutor, *cvk.NetworkControllerConfigurationOperation) {
	t.Helper()
	wants, _, err := splitNetworkCredentialConfiguration(noAuthCredentialModel("fixture", "tester"))
	if err != nil {
		t.Fatal(err)
	}
	e := &globalCredentialExecutor{desired: wants, owner: "owner", configName: "config"}
	receipt := noAuthCredentialReceipt(wants[0], "owner", "config")
	op := receipt.Operation.DeepCopy()
	op.Phase = "Unknown"
	if update {
		e.metadataReceipts = []cvk.NetworkControllerConfigurationActionApplication{noAuthCredentialReceipt(wants[0], "owner", "config")}
		op.ID = strings.Repeat("b", 32)
		op.Action, op.TargetID = "Update", "owned-snmp-id"
	}
	return e, op
}

func TestGlobalCredentialMetadataKnownTaskSelection(t *testing.T) {
	for _, update := range []bool{false, true} {
		action := "create"
		if update {
			action = "update"
		}
		t.Run(action, func(t *testing.T) {
			for _, mode := range []string{"valid", "taskless", "foreign-owner", "foreign-config", "changed-model", "duplicate-intent", "versioned-receipt", "future", "prepared", "history", "credentials", "bad-target", "missing-prior", "duplicate-prior", "foreign-prior", "same-operation", "invalid-phase", "source-half"} {
				t.Run(mode, func(t *testing.T) {
					e, op := metadataRecoveryFixture(t, update)
					switch mode {
					case "taskless":
						op.TaskID = ""
					case "foreign-owner":
						op.ConfigUID = "foreign"
					case "foreign-config":
						op.ConfigName = "foreign"
					case "changed-model":
						e.desired[0].metadata.Username = "changed"
					case "duplicate-intent":
						e.desired = append(e.desired, e.desired[0])
					case "versioned-receipt":
						e.receipts = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: e.desired[0].resource()}}
					case "future":
						op.SubmittedAt = &metav1.Time{Time: time.Now().Add(time.Hour)}
					case "prepared":
						op.PreparedAt = op.SubmittedAt.DeepCopy()
					case "history":
						op.SSIDCredentialHistory = append(op.SSIDCredentialHistory, cvk.NetworkControllerConfigurationSSIDCredentialHistory{})
					case "credentials":
						op.CredentialVersions = append(op.CredentialVersions, cvk.NetworkControllerConfigurationCredentialVersion{})
					case "bad-target":
						op.TargetID = "other-id"
					case "missing-prior":
						e.metadataReceipts = nil
					case "duplicate-prior":
						e.metadataReceipts = append(e.metadataReceipts, noAuthCredentialReceipt(e.desired[0], "owner", "config"), noAuthCredentialReceipt(e.desired[0], "owner", "config"))
					case "foreign-prior":
						e.metadataReceipts = []cvk.NetworkControllerConfigurationActionApplication{noAuthCredentialReceipt(e.desired[0], "foreign", "config")}
					case "same-operation":
						if update {
							op.ID = e.metadataReceipts[0].Operation.ID
						} else {
							e.metadataReceipts = []cvk.NetworkControllerConfigurationActionApplication{noAuthCredentialReceipt(e.desired[0], "owner", "config")}
						}
					case "invalid-phase":
						op.Phase = "Prepared"
					case "source-half":
						op.SourceUID = "source"
					}
					expected := mode == "valid" || mode == "missing-prior" && !update
					var plan configurationApplyPlan
					(&adapter{}).bindGlobalCredentialTaskRecovery(&plan, e)
					if got := plan.recoverKnownTask(op); got != expected {
						t.Fatalf("recovery=%v want=%v", got, expected)
					}
				})
			}
		})
	}
}
