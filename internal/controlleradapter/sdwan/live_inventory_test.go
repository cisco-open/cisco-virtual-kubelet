// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// TestLiveWANEdgeInventory is read-only. A private snapshot can be retained
// outside the repository when CVK_SDWAN_INVENTORY_PATH is set.
func TestLiveWANEdgeInventory(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	paths := []string{
		"/dataservice/system/device/vedges",
		"/dataservice/template/device/",
		"/dataservice/v1/config-group",
		"/dataservice/v1/policy-group",
		"/dataservice/template/policy/vsmart/",
		"/dataservice/v1/topology-group",
	}
	snapshot := map[string]any{}
	for _, path := range paths {
		var result any
		if err := c.Request(ctx, "GET", path, nil, &result); err != nil {
			t.Logf("inventory %s: %v", path, err)
			continue
		}
		snapshot[path] = result
		if obj, ok := result.(map[string]any); ok {
			if data, ok := obj["data"].([]any); ok {
				t.Logf("inventory %s: %d entries", path, len(data))
				for _, item := range data {
					if m, ok := item.(map[string]any); ok && path == "/dataservice/system/device/vedges" {
						t.Logf("edge chassis=%v system-ip=%v model=%v device-type=%v", m["uuid"], m["system-ip"], m["deviceModel"], m["device-type"])
					}
				}
				continue
			}
		}
		if arr, ok := result.([]any); ok {
			t.Logf("inventory %s: %d entries", path, len(arr))
		}
	}
	for _, groupPath := range []string{"/dataservice/v1/config-group", "/dataservice/v1/policy-group"} {
		groups, ok := snapshot[groupPath].([]any)
		if !ok {
			continue
		}
		for _, item := range groups {
			group, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, ok := group["id"].(string)
			if !ok {
				continue
			}
			path := groupPath + "/" + id + "/device/associate/"
			var result any
			if err := c.Request(ctx, "GET", path, nil, &result); err != nil {
				t.Logf("association %s: %v", group["name"], err)
				continue
			}
			snapshot[path] = result
			if arr, ok := result.([]any); ok {
				t.Logf("association %s: %d devices", group["name"], len(arr))
			}
		}
	}
	if path := os.Getenv("CVK_SDWAN_INVENTORY_PATH"); path != "" {
		data, err := json.MarshalIndent(snapshot, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
