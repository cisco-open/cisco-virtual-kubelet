// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestLiveCatalystCenterManagementAddressGuardRefusal(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		device, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal("lab identity unavailable", err)
		}
		ref := configurationDeviceReference{Name: device.Hostname, Serial: device.Serial, IP: "203.0.113.249"}
		calls := 0
		denied := errors.New("endpoint/handoff proof deliberately unavailable")
		write, err := c.planGuardedManagementAddressChange(ctx, ref, func(_ context.Context, target Device, requested string) error {
			calls++
			if target.ID != device.ID || !sameManagementIP(target.ManagementIP, device.ManagementIP) || requested != ref.IP {
				t.Error("guard lost native baseline")
			}
			return denied
		})
		if err != nil || write == nil {
			t.Fatal("guarded plan unavailable", err)
		}
		if _, err := write.submit(ctx); !errors.Is(err, denied) || calls != 1 {
			t.Fatal("missing endpoint proof did not refuse dispatch", err)
		}
		if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) || calls != 1 {
			t.Fatal("refused closure replayed", err)
		}
		after, err := c.configurationDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observed, err := resolveConfigurationDevice(configurationDeviceReference{Name: device.Hostname, IP: device.ManagementIP, Serial: device.Serial}, after)
		if err != nil || observed.ID != device.ID {
			t.Fatal("native identity/address changed after refused dispatch", err)
		}
		checked++
	}
	t.Logf("PASS: %d native plans refused before API mutation; one-use replay refusal and unchanged UUID/serial/address verified", checked)
}

// Synthetic new addresses remain local to the planner. No management-address
// update, interface configuration or physical mutation authority is requested.
func TestLiveCatalystCenterManagementAddressPlanning(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var synthetic string
	for suffix := 240; suffix < 250; suffix++ {
		candidate := fmt.Sprintf("203.0.113.%d", suffix)
		occupied := false
		for _, device := range inventory {
			occupied = occupied || sameManagementIP(device.ManagementIP, candidate)
		}
		if !occupied {
			synthetic = candidate
			break
		}
	}
	if synthetic == "" {
		t.Fatal("no unused synthetic planner address")
	}
	checked := 0
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		device, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal("lab device identity unavailable", err)
		}
		ref := configurationDeviceReference{Name: device.Hostname, IP: device.ManagementIP, Serial: device.Serial}
		if change, err := c.managementAddressCandidate(ctx, ref); err != nil || change != nil {
			t.Fatal("unchanged live address did not converge", err)
		}
		ref.IP = synthetic
		change, err := planManagementAddressChange(ref, inventory)
		if err != nil || change == nil || change.device.ID != device.ID || change.device.Serial != device.Serial || !sameManagementIP(change.before, device.ManagementIP) || change.after != synthetic {
			t.Fatal("synthetic new-address planning lost native UUID/serial/baseline", err)
		}
		checked++
	}
	t.Logf("PASS: %d live device no-ops and local synthetic address changes; UUID/serial/baseline preserved; no writes", checked)
}
