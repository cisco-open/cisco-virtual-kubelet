// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func wiredBulkReceiptFixture(t *testing.T) (*cvk.NetworkControllerConfigurationOperation, *cvk.NetworkControllerConfigurationStatus, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	targets := []contract.GroupTargetIdentity{{InventoryID: "a", PhysicalIdentity: "serial-a", Device: ops.ConfigurationReservationReference{Name: "a", UID: "a-device"}, Node: ops.ConfigurationReservationReference{Name: "a", UID: "a-node"}}, {InventoryID: "b", PhysicalIdentity: "serial-b", Device: ops.ConfigurationReservationReference{Name: "b", UID: "b-device"}, Node: ops.ConfigurationReservationReference{Name: "b", UID: "b-node"}}}
	base := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "intent", ConfigUID: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: wiredBulkProvisionPrefix + "v1:update:2:003:" + strings.Repeat("c", 64), Action: "Update", TargetID: "area", ParentID: "area", Phase: "Prepared"}
	op, err := contract.WithGroupIdentity(base, targets)
	if err != nil {
		t.Fatal(err)
	}
	op.Phase = "Verified"
	op.TaskID = "task"
	op.SubmittedAt = &metav1.Time{Time: now.Add(-time.Second)}
	key, err := wiredBulkReceiptKey(op)
	if err != nil {
		t.Fatal(err)
	}
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: key, Operation: *op.DeepCopy(), VerifiedAt: &metav1.Time{Time: now}}}}
	desired := op.DeepCopy()
	desired.ID = strings.Repeat("b", 32)
	desired.Phase = "Prepared"
	desired.TaskID = ""
	desired.SubmittedAt = nil
	desired.PhysicalGroup = nil
	desired, err = contract.WithGroupIdentity(desired, targets)
	if err != nil {
		t.Fatal(err)
	}
	return desired, state, now
}

func TestWiredBulkOwnedReceiptSuppressesOnlyExactRevision(t *testing.T) {
	for _, mode := range []string{"applied", "missing", "new-generation", "new-controller", "new-source", "foreign", "no-owner", "duplicate-owner", "duplicate-receipt", "unverified", "missing-time", "future-time", "wrong-digest", "native-baseline-drift"} {
		t.Run(mode, func(t *testing.T) {
			desired, state, now := wiredBulkReceiptFixture(t)
			switch mode {
			case "missing":
				state.ActionApplications = nil
			case "new-generation":
				desired.ConfigGeneration++
			case "new-controller":
				desired.ControllerGeneration++
			case "new-source":
				desired.SourceVersion = "new"
			case "foreign":
				state.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "no-owner":
				state.Owners = nil
			case "duplicate-owner":
				state.Owners = append(state.Owners, state.Owners[0])
			case "duplicate-receipt":
				state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
			case "unverified":
				state.ActionApplications[0].Operation.Phase = "Verifying"
			case "missing-time":
				state.ActionApplications[0].VerifiedAt = nil
			case "future-time":
				state.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(time.Second)}
			case "wrong-digest":
				state.ActionApplications[0].Operation.PhysicalGroup.Digest = strings.Repeat("d", 64)
			case "native-baseline-drift":
				desired.Resource = strings.Replace(desired.Resource, strings.Repeat("c", 64), strings.Repeat("e", 64), 1)
			}
			if mode == "new-generation" || mode == "new-controller" || mode == "new-source" || mode == "native-baseline-drift" {
				targets := desired.PhysicalGroup.Targets
				desired.PhysicalGroup = nil
				var err error
				desired, err = contract.WithGroupIdentity(desired, targets)
				if err != nil {
					t.Fatal(err)
				}
			}
			applied, err := wiredBulkAlreadyApplied(state, desired, now)
			if mode == "applied" {
				if err != nil || !applied {
					t.Fatal("owned verified revision replayed", err)
				}
				return
			}
			if mode == "missing" || mode == "new-generation" || mode == "new-controller" || mode == "new-source" {
				if err != nil || applied {
					t.Fatal("older receipt suppressed new work", err)
				}
				return
			}
			if err == nil || applied {
				t.Fatal("unsafe receipt accepted")
			}
		})
	}
}

func TestWiredBulkDeclarationCannotBeGuessedFromBatch(t *testing.T) {
	_, members, _ := newWiredBulkFixture(t)
	source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION"},{"name":"b","site":"Global/Lab/Building","state":"PROVISION"}]}}}`)}
	batch := wiredBulkBatch{path: "Global/Lab", members: members}
	if err := declaredWiredBulkBatch(source, batch); err != nil {
		t.Fatal(err)
	}
	batch.members = append([]wiredBulkMember(nil), members...)
	batch.members[0].state = "REPROVISION"
	if err := declaredWiredBulkBatch(source, batch); err == nil {
		t.Fatal("changed action accepted as original source")
	}
}
