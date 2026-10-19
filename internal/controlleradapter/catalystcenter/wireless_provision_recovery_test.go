// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"testing"
)

func TestWirelessRecoveryResolvesOriginalModelBinding(t *testing.T) {
	raw := `{"catalyst_center":{"inventory":{"devices":[{"name":"WLC","device_ip":"198.51.100.10","serial_number":"serial","site":"Global/Lab","state":"REPROVISION","type":"WirelessController","primary_managed_ap_locations":["Global/Lab"],"secondary_managed_ap_locations":[]}]}}}`
	before := wirelessProvisionBaseline{device: Device{ID: "wlc", Serial: "serial", ManagementIP: "198.51.100.10"}, siteID: "site", lastProvisioned: "100", modelConfigRequired: true}
	record, err := wirelessProvisionRecordFor("WLC", "Update", before)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := record.resource()
	if err != nil {
		t.Fatal(err)
	}
	op := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Phase: "Verifying", Resource: resource, Action: "Update", TargetID: "wlc", TargetSerial: "serial"}
	name, err := wirelessProvisionRecoveryName([]byte(raw), op)
	if err != nil || name != "WLC" {
		t.Fatal("model-bound recovery failed", name, err)
	}
	restored, err := restoreWirelessProvisionBaseline(name, op)
	if err != nil || restored.lastProvisioned != "100" || !restored.modelConfigRequired {
		t.Fatal("recovery replaced baseline", err)
	}
	for _, pair := range [][2]string{{`"name":"WLC"`, `"name":"other"`}, {`"serial_number":"serial"`, `"serial_number":"other"`}, {`"198.51.100.10"`, `"198.51.100.11"`}, {`"REPROVISION"`, `"PROVISION"`}} {
		if _, err := wirelessProvisionRecoveryName([]byte(strings.Replace(raw, pair[0], pair[1], 1)), op); err == nil {
			t.Fatal("changed source binding accepted", pair[0])
		}
	}
	changed := op.DeepCopy()
	changed.TargetID = "other"
	if _, err := wirelessProvisionRecoveryName([]byte(raw), changed); err == nil {
		t.Fatal("changed target accepted")
	}
	changed = op.DeepCopy()
	changed.Phase = "Unknown"
	if _, err := wirelessProvisionRecoveryName([]byte(raw), changed); err == nil {
		t.Fatal("uncertain outcome promoted to verification")
	}
}
