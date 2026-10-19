// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func qualifyLiveDeviceMembership(t *testing.T, c *client, ctx context.Context, tagName, tagID string, settled *bool, wait func(context.Context, Task, time.Time) error) {
	t.Helper()
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	device, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, devices)
	if err != nil || device.Serial == "" {
		t.Fatal("lab device identity unavailable", err)
	}
	ref := configurationDeviceReference{Name: device.Hostname, IP: device.ManagementIP, Serial: device.Serial}
	executor := &deviceMembershipExecutor{client: c, devices: []desiredDeviceMembership{{device: ref, tags: []string{tagName}}}}
	initial, err := c.tagMembers(ctx, tagID, "networkdevice")
	if err != nil || len(initial) != 0 {
		t.Fatal("isolated tag has existing members", err)
	}
	t.Cleanup(func() {
		if !*settled {
			t.Log("retained uncertain device association", tagID)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tags, err := c.listTags(cleanup)
		if err != nil {
			t.Error(err)
			*settled = false
			return
		}
		tag := tags[tagName]
		if tag.ID != tagID || tag.SystemTag == nil || *tag.SystemTag {
			t.Error("cleanup tag identity changed")
			*settled = false
			return
		}
		members, err := c.tagMembers(cleanup, tagID, "networkdevice")
		if err != nil || len(members) != 1 || !members[device.ID] {
			t.Error("cleanup membership changed", err)
			*settled = false
			return
		}
		*settled = false
		submitted := time.Now()
		raw, err := c.mutate(cleanup, http.MethodDelete, tagsPath+"/"+tagID+"/member/"+device.ID, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("cleanup receipt missing")
			return
		}
		if err = wait(cleanup, receipt.Response, submitted); err != nil {
			t.Error(err)
			return
		}
		members, err = c.tagMembers(cleanup, tagID, "networkdevice")
		if err != nil || len(members) != 0 {
			t.Error("device membership remains", err)
			return
		}
		*settled = true
		t.Log("device association cleanup verified", tagID)
	})
	write, err := executor.next(ctx)
	if err != nil || write == nil || write.identity.parent != tagID || write.identity.target != device.ID {
		t.Fatal("device membership plan failed", err)
	}
	*settled = false
	submitted := time.Now()
	task, err := write.submit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("device association task", task.ID)
	if err = wait(ctx, task, submitted); err != nil {
		t.Fatal(err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
	if ok, err := executor.verify(ctx, op); err != nil || !ok {
		t.Fatal("device association readback failed", err)
	}
	*settled = true
	if next, err := executor.next(ctx); err != nil || next != nil {
		t.Fatal("device association did not converge", err)
	}
	t.Log("device association verified without device configuration change")
}
