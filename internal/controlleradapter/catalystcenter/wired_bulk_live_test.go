// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Live inventory/planning plus deliberate guard refusal. No provisioning API
// write, reservation, lease or device mutation is authorized by this test.
func TestLiveCatalystCenterWiredBulkPlanning(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	members, before, allExisting := liveWiredBulkMembers(t, c, ctx)
	calls := 0
	denied := errors.New("qualification deliberately refuses provisioning dispatch")
	guard := func(context.Context, []Device) error { calls++; return denied }
	write, err := c.planWiredBulkProvision(ctx, "Global", members, guard)
	if err != nil {
		t.Fatal("live bulk planning", err)
	}
	if allExisting {
		if write != nil || calls != 0 {
			t.Fatal("converged bulk planned a mutation")
		}
		for i := range members {
			members[i].state = "REPROVISION"
		}
		write, err = c.planWiredBulkProvision(ctx, "Global", members, guard)
		if err != nil {
			t.Fatal(err)
		}
	}
	if write == nil {
		t.Fatal("no candidate for deliberate refusal")
	}
	if _, err := write.submit(ctx); !errors.Is(err, denied) || calls != 1 {
		t.Fatal("live guard refusal did not fence request", err)
	}
	if _, err := write.submit(ctx); err == nil || calls != 1 {
		t.Fatal("refused live closure replayed")
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		device, err := resolveConfigurationDevice(member.desired.reference, inventory)
		if err != nil {
			t.Fatal(err)
		}
		record, _, err := c.wiredProvisionedDevice(ctx, device.ID)
		baseline, bound := before[device.ID]
		if err != nil || !bound || record != baseline {
			t.Fatal("native provisioning identity changed during refusal", err)
		}
	}
	t.Log("three live lab identities passed bulk planning and deliberate one-use dispatch refusal; provisioning state unchanged; no native writes")
}

func liveWiredBulkMembers(t *testing.T, c *client, ctx context.Context) ([]wiredBulkMember, map[string]wiredProvisionedDevice, bool) {
	t.Helper()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members := []wiredBulkMember{}
	before := map[string]wiredProvisionedDevice{}
	allExisting := true
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		device, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal("lab native identity unavailable", err)
		}
		record, exists, err := c.wiredProvisionedDevice(ctx, device.ID)
		if err != nil {
			t.Fatal("native provisioned-device query failed", err)
		}
		before[device.ID] = record
		allExisting = allExisting && exists
		path := ""
		for _, site := range sites {
			if site.Type == "global" {
				continue
			}
			assigned, err := c.assignedDevices(ctx, site.ID)
			if err != nil {
				t.Fatal("native assignment query failed", err)
			}
			if assigned[device.ID] {
				if path != "" {
					t.Fatal("lab device has multiple site assignments")
				}
				path = site.NameHierarchy
			}
		}
		if path == "" {
			t.Skip("native inventory/provisioned-device APIs read successfully; bulk planning is unqualified because a lab device has no settled site assignment")
		}
		if device.Reachability != "Reachable" {
			t.Skip("native APIs read successfully; bulk planning is unqualified while a lab device is unreachable")
		}
		members = append(members, wiredBulkMember{desired: inventoryDeviceIntent{reference: configurationDeviceReference{Name: device.Hostname, IP: ip, Serial: device.Serial}, site: path}, state: "PROVISION"})
	}
	return members, before, allExisting
}
