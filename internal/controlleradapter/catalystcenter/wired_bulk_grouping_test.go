// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"fmt"
	"reflect"
	"testing"
)

func TestWiredBulkGroupingPinnedSelectionAndChunks(t *testing.T) {
	for _, mode := range []string{"default", "root", "managed", "deep-managed", "prefix-boundary", "ambiguous", "root-precedence", "unknown-root", "deep-root", "unknown-site", "dangling-state", "bad-state", "duplicate-name", "wireless"} {
		t.Run(mode, func(t *testing.T) {
			sites := []string{"Global", "Global/Lab", "Global/Lab/A", "Global/Lab/B", "Global/Lab2", "Global/Lab2/A"}
			intent := inventoryIntent{}
			states := map[string]string{}
			for i := 0; i < 23; i++ {
				name := fmt.Sprintf("edge-%02d", i)
				site := "Global/Lab/A"
				if i >= 12 {
					site = "Global/Lab/B"
				}
				intent.devices = append(intent.devices, inventoryDeviceIntent{reference: configurationDeviceReference{Name: name}, site: site})
				states[name] = "PROVISION"
				if i >= 20 {
					states[name] = "REPROVISION"
				}
			}
			options := wiredBulkGrouping{}
			switch mode {
			case "root":
				options.root = "Global/Lab"
			case "managed":
				options.managedSites = []string{"Global/Lab"}
			case "deep-managed":
				deep := "Global/A/B/C/D/E"
				options.managedSites = []string{deep}
				sites = append(sites, deep, deep+"/Building")
				for i := range intent.devices {
					intent.devices[i].site = deep + "/Building"
				}
			case "prefix-boundary":
				options.root = "Global/Lab"
				intent.devices[0].site = "Global/Lab2/A"
			case "ambiguous":
				options.managedSites = []string{"Global/Lab", "Global/Lab/A"}
			case "root-precedence":
				options.root = "Global/Lab"
				options.managedSites = []string{"Global/Lab", "Global/Lab/A"}
			case "unknown-root":
				options.root = "Global/Unknown"
			case "deep-root":
				options.root = "Global/A/B/C/D/E"
				sites = append(sites, options.root)
			case "unknown-site":
				intent.devices[0].site = "Global/Unknown"
			case "dangling-state":
				states["missing"] = "PROVISION"
			case "bad-state":
				states["edge-00"] = "DELETE"
			case "duplicate-name":
				intent.devices[1].reference.Name = "edge-00"
			case "wireless":
				intent.devices[0].wireless = &wirelessDevicePlacement{}
			}
			batches, err := groupWiredBulkMembers(intent, states, sites, options)
			invalid := mode == "ambiguous" || mode == "unknown-root" || mode == "deep-root" || mode == "unknown-site" || mode == "dangling-state" || mode == "bad-state" || mode == "duplicate-name" || mode == "wireless"
			if invalid {
				if err == nil || batches != nil {
					t.Fatal("invalid sequence returned batches")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			seen := map[string]bool{}
			for _, batch := range batches {
				if len(batch.members) == 0 || len(batch.members) > wiredBulkProvisionLimit {
					t.Fatal("invalid shard size")
				}
				state := batch.members[0].state
				for _, member := range batch.members {
					if member.state != state || seen[member.desired.reference.Name] || !siteWithin(member.desired.site, batch.path) {
						t.Fatal("mixed, repeated or out-of-root member")
					}
					seen[member.desired.reference.Name] = true
					count++
				}
			}
			expected := 23
			if mode == "prefix-boundary" {
				expected = 22
			}
			if count != expected {
				t.Fatal("selection lost or widened members", count)
			}
			reverse := intent
			reverse.devices = append([]inventoryDeviceIntent(nil), intent.devices...)
			for i, j := 0, len(reverse.devices)-1; i < j; i, j = i+1, j-1 {
				reverse.devices[i], reverse.devices[j] = reverse.devices[j], reverse.devices[i]
			}
			other, err := groupWiredBulkMembers(reverse, states, sites, options)
			if err != nil || !reflect.DeepEqual(other, batches) {
				t.Fatal("source order changed chunk identity", err)
			}
			if mode == "root" || mode == "managed" || mode == "deep-managed" || mode == "root-precedence" {
				if len(batches) != 3 || len(batches[0].members) != 10 || len(batches[1].members) != 10 || len(batches[2].members) != 3 || batches[2].members[0].state != "REPROVISION" {
					t.Fatal("root split/order differs from native method contract")
				}
			}
		})
	}
}
