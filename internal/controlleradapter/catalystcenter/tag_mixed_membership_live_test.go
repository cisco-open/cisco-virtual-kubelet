// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Called only for an isolated tag whose initial pure dynamic member and native
// UUID were independently verified. It restores that exact definition and
// removes only the manual association created here before returning.
func qualifyLiveMixedTagMembership(t *testing.T, c *client, ctx context.Context, name, id string, target Device, entry map[string]any, settled *bool, wait func(context.Context, Task, time.Time) error) {
	t.Helper()
	initial, err := c.listTags(ctx)
	if err != nil || initial[name].ID != id {
		t.Fatal("mixed fixture tag unavailable", err)
	}
	expectedTag := initial[name]
	delete(initial, name)
	ref := configurationDeviceReference{Name: target.Hostname, IP: target.ManagementIP, Serial: target.Serial}
	manual := &deviceMembershipExecutor{client: c, devices: []desiredDeviceMembership{{device: ref, tags: []string{name}}}}
	require := func(class string) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for {
			devices, err := c.configurationDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			currentDevice, err := resolveConfigurationDevice(ref, devices)
			if err != nil || currentDevice.ID != target.ID {
				t.Fatal("mixed fixture device replaced", err)
			}
			tags, err := c.listTags(ctx)
			if err != nil || !reflect.DeepEqual(tags[name], expectedTag) {
				t.Fatal("mixed fixture definition changed", err)
			}
			delete(tags, name)
			if !reflect.DeepEqual(tags, initial) {
				t.Fatal("unrelated tag inventory changed")
			}
			counts := map[string]int{}
			for _, association := range []string{"", "DYNAMIC", "STATIC", "MIXED"} {
				members, err := c.tagMembersByAssociation(ctx, id, "networkdevice", association)
				if err != nil {
					t.Fatal(err)
				}
				for member := range members {
					if member != target.ID {
						t.Fatal("foreign mixed fixture member; retain")
					}
				}
				counts[association] = len(members)
			}
			templates, err := c.templateTagMembers(ctx, id)
			if err != nil || len(templates) != 0 {
				t.Fatal("mixed fixture has template assignments", err)
			}
			matches := counts[""] == 1 && counts[class] == 1
			if class == "DYNAMIC" {
				matches = matches && counts["STATIC"] == 0 && counts["MIXED"] == 0
			} else if class == "STATIC" {
				matches = matches && counts["DYNAMIC"] == 0 && counts["MIXED"] == 0
			}
			if matches {
				t.Log("association class verified", class, counts)
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("association class did not converge; retain", class, counts)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	require("DYNAMIC")
	write, err := manual.next(ctx)
	if err != nil || write == nil || write.identity.target != target.ID || write.identity.parent != id {
		t.Fatal("dynamic-only membership incorrectly satisfied explicit intent", err)
	}
	*settled = false
	submitted := time.Now()
	task, err := write.submit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(ctx, task, submitted); err != nil {
		t.Fatal(err)
	}
	operation := &cvk.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: target.ID, ParentID: id}
	require("MIXED")
	if ok, err := manual.verify(ctx, operation); err != nil || !ok {
		t.Fatal("mixed membership did not verify explicit intent", err)
	}
	setPredicate := func(value string) {
		t.Helper()
		raw, _ := json.Marshal(entry)
		var source map[string]any
		json.Unmarshal(raw, &source)
		source["dynamic_rules"] = []any{map[string]any{"member_type": "networkdevice", "operation": "EQ", "name": "hostname", "value": value}}
		model, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"tags": []any{source}}}})
		e, err := c.compileConfigurationExecutor(model, "inventory")
		if err != nil {
			t.Fatal(err)
		}
		write, err := e.next(ctx)
		if err != nil || write == nil || write.action != "Update" || write.identity.target != id {
			t.Fatal("mixed fixture predicate update lost identity", err)
		}
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := wait(ctx, task, submitted); err != nil {
			t.Fatal(err)
		}
		if ok, err := e.verify(ctx, &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: id}); err != nil || !ok {
			t.Fatal("mixed fixture predicate readback failed", err)
		}
		tags, err := c.listTags(ctx)
		if err != nil || tags[name].ID != id {
			t.Fatal("mixed fixture tag changed", err)
		}
		expectedTag = tags[name]
	}
	nonmatch := name + "-manual-only"
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if strings.EqualFold(device.Hostname, nonmatch) {
			t.Fatal("manual-only predicate is not isolated")
		}
	}
	setPredicate(nonmatch)
	require("STATIC")
	if ok, err := manual.verify(ctx, operation); err != nil || !ok {
		t.Fatal("explicit association disappeared when the rule changed", err)
	}
	if next, err := manual.next(ctx); err != nil || next != nil {
		t.Fatal("explicit association replayed after rule change", err)
	}
	setPredicate(target.Hostname)
	require("MIXED")
	submitted = time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, fmt.Sprintf("%s/%s/member/%s", tagsPath, id, target.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("manual fixture removal acknowledgement unavailable")
	}
	if err := wait(ctx, receipt.Response, submitted); err != nil {
		t.Fatal(err)
	}
	require("DYNAMIC")
	*settled = true
	t.Log("explicit membership survived dynamic rule changes; manual fixture removed with dynamic membership preserved")
}
