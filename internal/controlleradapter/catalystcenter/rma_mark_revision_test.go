// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAMarkReceiptPolicy(t *testing.T) {
	for _, mode := range []string{"same", "new-source", "new-replacement", "no-record-no-receipt", "unowned", "deleted", "replaced-record", "replacement-device-id", "moved-site", "recreated-provision", "foreign-owner", "missing-owner", "duplicate-owner", "foreign-receipt", "duplicate-receipt", "missing-time", "invalid-resource", "invalid-key", "old-record", "new-record", "workflow", "failed-step", "nonfaulty", "missing-serial"} {
		t.Run(mode, func(t *testing.T) {
			ref, want, devices, records := rmaBindingFixture()
			now := time.Now().UTC().Truncate(time.Second)
			submitted := now.Add(-time.Minute)
			records[0].CreatedAt = submitted.UnixMilli()
			snapshot, err := bindDeviceReplacement(ref, want, devices, records)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.siteID, snapshot.provisionID = "site", "provision"
			resource, err := deviceReplacementMarkResource(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "config", UID: "config-uid", Generation: 1}}
			op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: 1, Section: "inventory", Resource: resource, Action: "Create", Phase: "Verified", TargetID: "old-id", TargetSerial: "old-serial", ParentID: "site", TaskID: "mark-task", SubmittedAt: &metav1.Time{Time: submitted}}
			state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: "rma-mark-record:record", Operation: op, VerifiedAt: &metav1.Time{Time: now}}}}
			switch mode {
			case "new-source":
				config.Generation++
			case "new-replacement":
				snapshot.replacementSerial = "another-spare"
				snapshot.replacement = nil
			case "no-record-no-receipt":
				snapshot.record = nil
				state.ActionApplications = nil
			case "unowned":
				state.ActionApplications = nil
			case "deleted":
				snapshot.record = nil
			case "replaced-record":
				snapshot.record.ID = "new-record"
			case "replacement-device-id":
				snapshot.target.ID = "new-id"
			case "moved-site":
				snapshot.siteID = "new-site"
			case "recreated-provision":
				snapshot.provisionID = "new-provision"
			case "foreign-owner":
				state.Owners[0].ConfigUID = "other"
			case "missing-owner":
				state.Owners = nil
			case "duplicate-owner":
				state.Owners = append(state.Owners, state.Owners[0])
			case "foreign-receipt":
				state.ActionApplications[0].Operation.ConfigUID = "other"
			case "duplicate-receipt":
				state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
			case "missing-time":
				state.ActionApplications[0].VerifiedAt = nil
			case "invalid-resource":
				state.ActionApplications[0].Operation.Resource = "rma-mark:bad"
			case "invalid-key":
				state.ActionApplications[0].Resource = "rma-mark-record:bad/id"
			case "old-record":
				snapshot.record.CreatedAt = submitted.Add(-time.Hour).UnixMilli()
			case "new-record":
				snapshot.record.CreatedAt = now.Add(time.Hour).UnixMilli()
			case "failed-step":
				snapshot.record.WorkflowFailedStep = "CONFIGURE"
			case "workflow":
				snapshot.record.WorkflowID = "foreign-workflow"
			case "nonfaulty":
				snapshot.record.Status = "NON-FAULTY"
			case "missing-serial":
				snapshot.record.FaultySerial = ""
			}
			applied, err := deviceReplacementMarkApplied(state, config, snapshot, now)
			success := mode == "same" || mode == "new-source" || mode == "new-replacement"
			empty := mode == "no-record-no-receipt"
			if applied != success || (err == nil) != (success || empty) {
				t.Fatal("incorrect receipt policy", applied, err)
			}
		})
	}
}
