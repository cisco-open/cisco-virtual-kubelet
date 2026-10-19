// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveDeviceRoleNoOp(t *testing.T) {
	if os.Getenv("CATC_DEVICE_ROLE_READ") != "existing-lab" {
		t.Skip("explicit read-only qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	desired := []inventoryDeviceIntent{}
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		d, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, devices)
		if err != nil || d.Serial == "" || !validDeviceRole(d.Role) || !validDeviceRoleSource(d.RoleSource) {
			t.Fatal("role inventory incomplete", ip, err)
		}
		desired = append(desired, inventoryDeviceIntent{reference: configurationDeviceReference{Name: d.Hostname, IP: ip, Serial: d.Serial}, role: d.Role, roleSource: d.RoleSource})
		t.Log(ip, d.Role, d.RoleSource)
	}
	e := &deviceRoleExecutor{client: c, desired: desired}
	if write, err := e.next(ctx); err != nil || write != nil {
		t.Fatal("existing role configuration did not converge without mutation", err)
	}
}
