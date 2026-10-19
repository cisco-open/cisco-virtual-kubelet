// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestWirelessProvisionBaselineJournalRoundTrip(t *testing.T) {
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	before := wirelessProvisionBaseline{device: Device{ID: "wlc", Serial: "serial", ManagementIP: "198.51.100.10"}, siteID: "site", lastProvisioned: "1790000000000", modelConfigRequired: true}
	record, err := wirelessProvisionRecordFor("WLC", "Update", before)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := record.resource()
	if err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(context.Background(), config, nil, "inventory", resource, "Update", configurationResourceIdentity{target: "wlc", serial: "serial"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := *a
	stored := journalState(t, &restarted).Operation
	got, err := restoreWirelessProvisionBaseline("WLC", stored)
	if err != nil || stored.ID != op.ID || got.lastProvisioned != before.lastProvisioned || got.siteID != before.siteID || got.device.ManagementIP != before.device.ManagementIP || !got.modelConfigRequired {
		t.Fatal("baseline lost", err)
	}
	for _, scenario := range []string{"target", "serial", "action", "name", "truncated", "unknown-field"} {
		changed := stored.DeepCopy()
		name := "WLC"
		switch scenario {
		case "target":
			changed.TargetID = "other"
		case "serial":
			changed.TargetSerial = ""
		case "action":
			changed.Action = "Create"
		case "name":
			name = "OTHER"
		case "truncated":
			changed.Resource = changed.Resource[:len(changed.Resource)-5]
		case "unknown-field":
			raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(changed.Resource, wirelessProvisionRecordPrefix))
			raw = append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
			changed.Resource = wirelessProvisionRecordPrefix + base64.RawURLEncoding.EncodeToString(raw)
		}
		if _, err := restoreWirelessProvisionBaseline(name, changed); err == nil {
			t.Fatal("changed record accepted", scenario)
		}
	}
}
func TestWirelessProvisionRecordBounds(t *testing.T) {
	b := wirelessProvisionBaseline{device: Device{ID: "wlc", Serial: "serial", ManagementIP: "198.51.100.10"}, siteID: "site", lastProvisioned: "100"}
	for _, scenario := range []string{"address", "stamp", "oversize"} {
		copy := b
		switch scenario {
		case "address":
			copy.device.ManagementIP = "example.com"
		case "stamp":
			copy.lastProvisioned = "-1"
		case "oversize":
			copy.device.ID = strings.Repeat("d", 128)
			copy.device.Serial = strings.Repeat("s", 128)
			copy.siteID = strings.Repeat("p", 128)
		}
		if _, err := wirelessProvisionRecordFor("WLC", "Update", copy); err == nil {
			t.Fatal("invalid record accepted", scenario)
		}
	}
}

func TestWirelessProvisionDependencyDigestRoundTrip(t *testing.T) {
	record, err := wirelessProvisionRecordFor("wlc", "Update", wirelessProvisionBaseline{device: Device{ID: "device-id", Serial: "serial", ManagementIP: "198.51.100.10"}, siteID: "site", lastProvisioned: "100"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := record.resource()
	if err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{"", strings.Repeat("a", 64), "bad", strings.Repeat("A", 64)} {
		write, err := bindWirelessProvisionDependencies(&configurationWrite{resource: legacy}, digest)
		if digest == "bad" || digest == strings.Repeat("A", 64) {
			if err == nil {
				t.Fatal("invalid digest accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeWirelessProvisionRecord(write.resource)
		if err != nil || decoded.DependencyDigest != digest || decoded.Timestamp != "100" {
			t.Fatal("dependency or baseline lost", err)
		}
		if digest == "" && write.resource != legacy {
			t.Fatal("legacy record encoding changed")
		}
		if digest != "" {
			if _, err := bindWirelessProvisionDependencies(write, strings.Repeat("b", 64)); err == nil {
				t.Fatal("rewrote dependency binding")
			}
		}
	}
}
