// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestFabricVLANSSIDCompletionReceipt(t *testing.T) {
	for _, mode := range []string{"verified", "running", "task-error", "read-error", "wrong-phase", "no-task", "future-submission", "bad-group", "missing-mapping", "changed-tag", "wrong-claim", "undeclared", "missing-member", "changed-serial", "late-change"} {
		t.Run(mode, func(t *testing.T) {
			binding := fabricVLANSSID{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}
			desired := []fabricVLANSSID{binding}
			deps := &fabricVLANSSIDDependencies{siteID: "site", fabricID: "fabric", before: []fabricVLANSSID{}, payload: desired, claim: &binding, digest: strings.Repeat("b", 64), physical: []Device{{ID: "wlc", Serial: "foc12345678"}}}
			resource, _ := fabricVLANSSIDResource("Global/Lab", deps)
			op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config-uid", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Action: "Update", Phase: "Prepared", ParentID: "site", TargetID: "fabric", Resource: resource}, []contract.GroupTargetIdentity{{InventoryID: "wlc", PhysicalIdentity: "FOC12345678", Device: ops.ConfigurationReservationReference{Name: "wlc", UID: "device-uid"}, Node: ops.ConfigurationReservationReference{Name: "wlc", UID: "node-uid"}}})
			if err != nil {
				t.Fatal(err)
			}
			submitted := metav1.NewTime(time.Unix(100, 0))
			now := time.Unix(200, 0)
			op.Phase = "Verifying"
			op.TaskID = "task"
			op.SubmittedAt = &submitted
			deps.before = desired
			switch mode {
			case "wrong-phase":
				op.Phase = "Accepted"
			case "no-task":
				op.TaskID = ""
			case "future-submission":
				now = time.Unix(99, 0)
			case "bad-group":
				op.PhysicalGroup.Digest = strings.Repeat("e", 64)
			case "missing-mapping":
				deps.before = []fabricVLANSSID{}
			case "changed-tag":
				tag := "Changed"
				deps.before = []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}}}}
			case "undeclared":
				desired = []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Other"}}}}
			case "wrong-claim":
				wrong := fabricVLANSSID{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Other"}}}
				input := *deps
				input.before = []fabricVLANSSID{}
				input.claim = &wrong
				op.Resource, _ = fabricVLANSSIDResource("Global/Lab", &input)
				op.PhysicalGroup.Digest, err = contract.GroupOperationDigest(op, op.PhysicalGroup.Targets)
				if err != nil {
					t.Fatal(err)
				}
			case "missing-member":
				deps.physical = nil
			case "changed-serial":
				deps.physical[0].Serial = "foc99999999"
			}
			reads, tasks := 0, 0
			receipt, err := verifyFabricVLANSSIDReceipt(context.Background(), "Global/Lab", desired, op, now, func(context.Context, string, time.Time, time.Time) (bool, error) {
				tasks++
				if mode == "task-error" {
					return false, errors.New("failed task")
				}
				return mode != "running", nil
			}, func(context.Context) (*fabricVLANSSIDDependencies, error) {
				reads++
				if mode == "read-error" {
					return nil, errors.New("unavailable")
				}
				copy := *deps
				if mode == "late-change" && reads > 1 {
					copy.digest = strings.Repeat("c", 64)
				}
				return &copy, nil
			})
			if mode != "verified" {
				if err == nil || receipt != nil {
					t.Fatal("unverified receipt escaped", err)
				}
				if (mode == "running" || mode == "task-error") && reads != 0 {
					t.Fatal("readback before task completion")
				}
				return
			}
			if err != nil || receipt == nil || reads != 2 || tasks != 1 || receipt.Operation.Phase != "Verified" || op.Phase != "Verifying" {
				t.Fatal("receipt completion", err)
			}
			owned, err := fabricVLANSSIDOwnership("Global/Lab", "site", "fabric", "config-uid", deps.before, []cvk.NetworkControllerConfigurationActionApplication{*receipt})
			if err != nil || len(owned) != 1 || owned["Staff"].VLAN != "Clients" {
				t.Fatal("receipt ownership recovery", owned, err)
			}
		})
	}
}
