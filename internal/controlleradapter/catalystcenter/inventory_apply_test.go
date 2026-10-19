// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strings"
	"testing"
)

func TestManagedInventoryCompilerPreservesGatesAndIntent(t *testing.T) {
	c := &client{}
	if _, err := c.compileConfigurationApply(roleModel(), []string{"inventory"}, nil); err == nil {
		t.Fatal("default compiler enabled device execution")
	}
	p, err := c.compileManagedInventoryApply(roleModel())
	if err != nil || p.section != "inventory" || p.executor.count() != 3 {
		t.Fatal("lost assignment, role or membership", err)
	}
	for _, raw := range [][]byte{
		[]byte(strings.Replace(string(roleModel()), `"serial_number":"serial",`, "", 1)),
		[]byte(strings.Replace(string(roleModel()), `"state":"ASSIGN"`, `"state":"REPROVISION","type":"WirelessController","primary_managed_ap_locations":["Global/Lab"],"secondary_managed_ap_locations":[]`, 1)),
	} {
		if _, err := c.compileManagedInventoryApply(raw); err == nil {
			t.Fatal("unsupported or unbound intent discarded")
		}
	}
}

func TestManagedInventoryGroupOrdersAssignmentThenRole(t *testing.T) {
	ctx := context.Background()
	assigned, roleSet := false, false
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case devicesPath:
			role, source := "ACCESS", "AUTO"
			if roleSet {
				role, source = "DISTRIBUTION", "MANUAL"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "device", Hostname: "switch", Serial: "serial", ManagementIP: "198.51.100.101", Role: role, RoleSource: source}}})
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "site", Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"}}})
		case assignedDevicesPath:
			if assigned {
				fmt.Fprint(w, `{"response":[{"deviceId":"device","siteId":"site"}]}`)
			} else {
				fmt.Fprint(w, `{"response":[]}`)
			}
		case assignDevicesPath:
			if assigned || roleSet || r.Method != "POST" {
				t.Error("unexpected assignment")
			}
			assigned = true
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"assign-task"}}`)
		case deviceRolePath:
			if !assigned || roleSet || r.Method != "PUT" {
				t.Error("role before assignment or replay")
			}
			roleSet = true
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"role-task"}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	raw := []byte(strings.Replace(string(roleModel()), `,"tags":["lab"]`, "", 1))
	for _, resource := range []string{"assignments/switch", "roles/switch"} {
		plan, err := c.compileManagedInventoryApply(raw)
		if err != nil {
			t.Fatal(err)
		}
		write, err := plan.executor.next(ctx)
		if err != nil || write == nil || write.resource != resource || write.identity.serial != "serial" {
			t.Fatal("wrong planned operation", write, err)
		}
		op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent, TargetSerial: write.identity.serial}
		if ok, err := plan.executor.verify(ctx, op); err != nil || ok {
			t.Fatal("premature readback", err)
		}
		if _, err := write.submit(ctx); err != nil {
			t.Fatal(err)
		}
		if ok, err := plan.executor.verify(ctx, op); err != nil || !ok {
			t.Fatal("readback failed", err)
		}
	}
	plan, err := c.compileManagedInventoryApply(raw)
	if err != nil {
		t.Fatal(err)
	}
	if write, err := plan.executor.next(ctx); err != nil || write != nil || writes != 2 {
		t.Fatal("restarted compiler replayed", err, writes)
	}
}
