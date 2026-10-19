// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGroupIdentityRecordRecovery(t *testing.T) {
	for _, mode := range []string{"valid", "phase", "changed-source", "removed-target", "changed-serial", "unknown-version", "unknown-field", "truncated", "trailing", "oversized", "missing-digest", "recomputed-subset", "missing-expected-digest"} {
		t.Run(mode, func(t *testing.T) {
			op, targets := groupIdentityFixture()
			raw, err := EncodeGroupIdentityRecord(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if json.Unmarshal(raw, &record) != nil {
				t.Fatal("invalid generated record")
			}
			expectedDigest := record["digest"].(string)
			switch mode {
			case "missing-expected-digest":
				expectedDigest = ""
			case "recomputed-subset":
				smaller, _ := EncodeGroupIdentityRecord(op, targets[:1])
				if json.Unmarshal(smaller, &record) != nil {
					t.Fatal("smaller record")
				}
			case "phase":
				op.Phase = "Verifying"
				op.TaskID = "task"
			case "changed-source":
				op.SourceVersion = "new"
			case "removed-target":
				record["targets"] = record["targets"].([]any)[:1]
			case "changed-serial":
				record["targets"].([]any)[0].(map[string]any)["physicalIdentity"] = "replacement"
			case "unknown-version":
				record["version"] = "future"
			case "unknown-field":
				record["authority"] = "allowed"
			case "missing-digest":
				delete(record, "digest")
			}
			raw, _ = json.Marshal(record)
			switch mode {
			case "truncated":
				raw = raw[:len(raw)/2]
			case "trailing":
				raw = append(raw, []byte(" {}")...)
			case "oversized":
				raw = []byte(strings.Repeat(" ", MaxGroupIdentityRecordBytes+1))
			}
			got, err := DecodeGroupIdentityRecord(op, expectedDigest, raw)
			valid := mode == "valid" || mode == "phase"
			if (err == nil) != valid {
				t.Fatal("incorrect record recovery", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial recovery")
				}
				return
			}
			if len(got) != 2 || got[0].PhysicalIdentity != "serial-a" || got[1].PhysicalIdentity != "serial-b" {
				t.Fatal("noncanonical recovery")
			}
		})
	}
}
func TestGroupReferenceAmbiguity(t *testing.T) {
	for _, mode := range []string{"device-name-reuse", "node-name-reuse", "invalid-device-name", "invalid-node-name", "invalid-inventory", "invalid-device-uid", "invalid-node-uid"} {
		t.Run(mode, func(t *testing.T) {
			op, targets := groupIdentityFixture()
			switch mode {
			case "device-name-reuse":
				targets[1].Device.Name = targets[0].Device.Name
			case "node-name-reuse":
				targets[1].Node.Name = targets[0].Node.Name
			case "invalid-device-name":
				targets[0].Device.Name = "Not A Name"
			case "invalid-node-name":
				targets[0].Node.Name = "node/other"
			case "invalid-inventory":
				targets[0].InventoryID = "id\nother"
			case "invalid-device-uid":
				targets[0].Device.UID = strings.Repeat("a", 129)
			case "invalid-node-uid":
				targets[0].Node.UID = "uid other"
			}
			raw, err := EncodeGroupIdentityRecord(op, targets)
			if err == nil || raw != nil {
				t.Fatal("invalid identity encoded")
			}
		})
	}
}
