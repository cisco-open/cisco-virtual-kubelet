// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestExtranetOwnershipReceipt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"valid", "absent", "foreign-config", "missing-owner", "foreign-owner", "duplicate-owner", "duplicate-receipt", "native-replacement", "bad-native-id", "unknown", "no-task", "missing-time", "future-time", "earlier-verification", "missing-verification", "bad-group", "bad-resource", "foreign-section"} {
		t.Run(mode, func(t *testing.T) {
			prepared := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Phase: "Prepared", Action: "Create", Resource: "extranet-create:" + strings.Repeat("b", 64)}
			op, err := contract.WithGroupIdentity(prepared, []contract.GroupTargetIdentity{{InventoryID: "device", PhysicalIdentity: "foc12345678", Device: ops.ConfigurationReservationReference{Name: "device", UID: "device-uid"}, Node: ops.ConfigurationReservationReference{Name: "node", UID: "node-uid"}}})
			if err != nil {
				t.Fatal(err)
			}
			submitted := metav1.NewTime(now.Add(-time.Minute))
			verified := metav1.NewTime(now)
			op.Phase = "Verified"
			op.TaskID = "task"
			op.SubmittedAt = &submitted
			prefix, err := extranetReceiptPrefix("Shared")
			if err != nil {
				t.Fatal(err)
			}
			state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: "config", ConfigUID: "uid"}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "fabric", Resource: prefix + "policy", Operation: *op, VerifiedAt: &verified}}}
			r := &state.ActionApplications[0]
			id := "policy"
			switch mode {
			case "absent":
				state.ActionApplications = nil
			case "foreign-config":
				r.Operation.ConfigUID = "other"
			case "missing-owner":
				state.Owners = nil
			case "foreign-owner":
				state.Owners[0].ConfigUID = "other"
			case "duplicate-owner":
				state.Owners = append(state.Owners, state.Owners[0])
			case "duplicate-receipt":
				state.ActionApplications = append(state.ActionApplications, *r)
			case "native-replacement":
				id = "replacement"
			case "bad-native-id":
				id = "bad/id"
			case "unknown":
				r.Operation.Phase = "Unknown"
			case "no-task":
				r.Operation.TaskID = ""
			case "missing-time":
				r.Operation.SubmittedAt = nil
			case "future-time":
				r.VerifiedAt = &metav1.Time{Time: now.Add(time.Hour)}
			case "earlier-verification":
				r.VerifiedAt = &metav1.Time{Time: submitted.Add(-time.Minute)}
			case "missing-verification":
				r.VerifiedAt = nil
			case "bad-group":
				r.Operation.PhysicalGroup.Digest = strings.Repeat("c", 64)
			case "bad-resource":
				r.Operation.Resource = "extranet-create:invalid"
			case "foreign-section":
				r.Operation.Section = "inventory"
			}
			owned, err := ownedExtranetPolicy(state, "config", "uid", "Shared", id, now)
			if mode == "valid" {
				if !owned || err != nil {
					t.Fatal(owned, err)
				}
				return
			}
			if mode == "absent" {
				if owned || err != nil {
					t.Fatal(owned, err)
				}
				return
			}
			if owned || err == nil {
				t.Fatal("invalid ownership accepted", owned, err)
			}
		})
	}
}
