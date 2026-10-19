// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWirelessInventoryModelMapping(t *testing.T) {
	for _, state := range []string{"PROVISION", "REPROVISION"} {
		model := map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []any{map[string]any{"name": "WLC", "serial_number": "serial", "device_ip": "198.51.100.10", "state": state, "type": "WirelessController", "site": "Global/Lab", "primary_managed_ap_locations": []any{"Global/Lab"}, "secondary_managed_ap_locations": []any{}}}}}}
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		intent, err := parseInventoryIntent(raw)
		if err != nil || len(intent.devices) != 1 {
			t.Fatal("parse", err)
		}
		d := intent.devices[0]
		if d.wireless == nil || d.wireless.state != state || d.reference.Serial != "serial" || d.site != "Global/Lab" || len(d.wireless.locations.primary) != 1 || d.wireless.locations.secondary == nil || d.wireless.locations.anchor != nil {
			t.Fatal("lost placement fields")
		}
		c := &client{}
		if _, err := c.compileConfigurationExecutor(raw, "inventory"); err == nil {
			t.Fatal("device execution bypassed admission")
		}
		if _, err := c.compileConfigReports(raw, []string{"inventory"}); err == nil {
			t.Fatal("partial reporting silently accepted")
		}
		if _, err := c.reportInventoryIntent(context.Background(), intent); err == nil {
			t.Fatal("direct reporting discarded placement")
		}
	}
}
func TestWirelessInventoryRejectsDiscardedFields(t *testing.T) {
	for _, state := range []string{"ASSIGN", "PROVISION"} {
		d := map[string]any{"name": "WLC", "state": state, "type": "WirelessController", "site": "Global/Lab", "primary_managed_ap_locations": []any{"Global/Lab"}}
		if state == "PROVISION" {
			d["unimplemented_device_field"] = true
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []any{d}}}})
		if _, err := parseInventoryIntent(raw); err == nil {
			t.Fatal("discarded intent accepted", state)
		}
	}
}
