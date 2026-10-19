// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
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

func TestFabricVLANSSIDRecoveryOwnership(t *testing.T) {
	for _, mode := range []string{"moved", "unknown", "new", "not-converged", "external-change", "bad-prior-receipt", "bad-active-group", "wrong-phase", "no-task"} {
		t.Run(mode, func(t *testing.T) {
			old := fabricVLANSSID{VLAN: "Old", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}
			changed := fabricVLANSSID{VLAN: "New", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}
			guest := fabricVLANSSID{VLAN: "Guest", SSIDs: []fabricSSIDBinding{{Name: "Guest"}}}
			build := func(before, after []fabricVLANSSID, claim *fabricVLANSSID, phase string) *cvk.NetworkControllerConfigurationOperation {
				resource, err := fabricVLANSSIDResource("Global/Lab", &fabricVLANSSIDDependencies{siteID: "site", fabricID: "fabric", before: before, payload: after, claim: claim})
				if err != nil {
					t.Fatal(err)
				}
				op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Action: "Update", Phase: "Prepared", TargetID: "fabric", ParentID: "site", Resource: resource}, []contract.GroupTargetIdentity{{InventoryID: "wlc", PhysicalIdentity: "FOC12345678", Device: ops.ConfigurationReservationReference{Name: "wlc", UID: "device"}, Node: ops.ConfigurationReservationReference{Name: "wlc", UID: "node"}}})
				if err != nil {
					t.Fatal(err)
				}
				at := metav1.NewTime(time.Unix(100, 0))
				op.SubmittedAt = &at
				op.TaskID = "task"
				op.Phase = phase
				return op
			}
			receipt := func(row fabricVLANSSID) cvk.NetworkControllerConfigurationActionApplication {
				op := build([]fabricVLANSSID{}, []fabricVLANSSID{row}, &row, "Verified")
				key, _ := fabricVLANSSIDApplicationKey(op.Resource)
				at := metav1.NewTime(time.Unix(200, 0))
				return cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: key, Operation: *op, VerifiedAt: &at}
			}
			apps := []cvk.NetworkControllerConfigurationActionApplication{receipt(old), receipt(guest)}
			before := []fabricVLANSSID{guest, old}
			after := []fabricVLANSSID{guest, changed}
			// Native canonical ordering is part of the persisted full-result hash.
			before, _ = canonicalFabricVLANSSIDs(before)
			after, _ = canonicalFabricVLANSSIDs(after)
			active := build(before, after, &changed, "Accepted")
			observed := after
			switch mode {
			case "unknown":
				active.Phase = "Unknown"
			case "new":
				apps = apps[1:]
			case "not-converged":
				observed = before
			case "external-change":
				observed = append(append([]fabricVLANSSID{}, after...), fabricVLANSSID{VLAN: "Extra", SSIDs: []fabricSSIDBinding{{Name: "Extra"}}})
			case "bad-prior-receipt":
				apps[0].VerifiedAt = nil
			case "bad-active-group":
				active.PhysicalGroup.Digest = strings.Repeat("f", 64)
			case "wrong-phase":
				active.Phase = "Prepared"
			case "no-task":
				active.TaskID = ""
			}
			owned, err := fabricVLANSSIDRecoveryOwnership("Global/Lab", "site", "fabric", "config", observed, apps, active)
			valid := mode == "moved" || mode == "unknown" || mode == "new"
			if (err == nil) != valid {
				t.Fatal("recovery ownership", err)
			}
			if !valid {
				if owned != nil {
					t.Fatal("partial ownership")
				}
				return
			}
			if len(owned) != 1 || owned["Guest"].VLAN != "Guest" {
				t.Fatal("adopted active binding or lost retained ownership", owned)
			}
		})
	}
}
