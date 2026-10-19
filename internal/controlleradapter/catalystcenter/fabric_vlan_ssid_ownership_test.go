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

func TestFabricVLANSSIDOwnership(t *testing.T) {
	for _, mode := range []string{"owned", "no-receipt", "other-fabric", "deleted", "foreign-config", "unverified", "no-time", "early-time", "bad-group", "bad-key", "duplicate", "changed-tag", "changed-vlan"} {
		t.Run(mode, func(t *testing.T) {
			tag := "Employees"
			binding := fabricVLANSSID{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}}}
			resource, err := fabricVLANSSIDResource("Global/Lab", &fabricVLANSSIDDependencies{siteID: "site", fabricID: "fabric", before: []fabricVLANSSID{}, payload: []fabricVLANSSID{binding}, claim: &binding})
			if err != nil {
				t.Fatal(err)
			}
			op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config-uid", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Action: "Update", Phase: "Prepared", ParentID: "site", TargetID: "fabric", Resource: resource}, []contract.GroupTargetIdentity{{InventoryID: "wlc", PhysicalIdentity: "FOC12345678", Device: ops.ConfigurationReservationReference{Name: "wlc", UID: "device-uid"}, Node: ops.ConfigurationReservationReference{Name: "wlc", UID: "node-uid"}}})
			if err != nil {
				t.Fatal(err)
			}
			submitted := metav1.NewTime(time.Unix(100, 0))
			verified := metav1.NewTime(time.Unix(200, 0))
			op.Phase = "Verified"
			op.TaskID = "task"
			op.SubmittedAt = &submitted
			key, _ := fabricVLANSSIDApplicationKey(resource)
			receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: key, Operation: *op, VerifiedAt: &verified}
			observed := []fabricVLANSSID{binding, {VLAN: "Guest", SSIDs: []fabricSSIDBinding{{Name: "Guest"}}}}
			switch mode {
			case "foreign-config":
				receipt.Operation.ConfigUID = "replacement"
			case "unverified":
				receipt.Operation.Phase = "Accepted"
			case "no-time":
				receipt.VerifiedAt = nil
			case "early-time":
				early := metav1.NewTime(time.Unix(99, 0))
				receipt.VerifiedAt = &early
			case "bad-group":
				receipt.Operation.PhysicalGroup.Digest = strings.Repeat("f", 64)
			case "bad-key":
				receipt.Resource += "extra"
			case "other-fabric":
				receipt.Resource = "fabric-vlan-ssid:other:other"
			case "deleted":
				observed = observed[1:]
			case "changed-tag":
				tag = "Changed"
			case "changed-vlan":
				observed[0].VLAN = "Other"
			}
			applications := []cvk.NetworkControllerConfigurationActionApplication{receipt}
			if mode == "no-receipt" {
				applications = nil
			}
			if mode == "duplicate" {
				applications = append(applications, receipt)
			}
			owned, err := fabricVLANSSIDOwnership("Global/Lab", "site", "fabric", "config-uid", observed, applications)
			valid := mode == "owned" || mode == "no-receipt" || mode == "other-fabric" || mode == "deleted"
			if (err == nil) != valid {
				t.Fatal("ownership result", err)
			}
			if !valid {
				if owned != nil {
					t.Fatal("partial ownership escaped")
				}
				return
			}
			if mode != "owned" {
				if len(owned) != 0 {
					t.Fatal("unowned row adopted")
				}
				return
			}
			if len(owned) != 1 || owned["Staff"].VLAN != "Clients" || *owned["Staff"].SecurityGroup != "Employees" {
				t.Fatal("ownership lost", owned)
			}
			*owned["Staff"].SecurityGroup = "copy"
			if tag != "Employees" {
				t.Fatal("ownership aliases native input")
			}
		})
	}
}
