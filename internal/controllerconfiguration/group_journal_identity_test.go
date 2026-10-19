// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"testing"
)

func TestGroupJournalIdentityRoundTrip(t *testing.T) {
	op, targets := groupIdentityFixture()
	prepared, err := WithGroupIdentity(op, targets)
	if err != nil {
		t.Fatal(err)
	}
	if op.PhysicalGroup != nil {
		t.Fatal("mutated source operation")
	}
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var restored cvk.NetworkControllerConfigurationOperation
	if json.Unmarshal(raw, &restored) != nil || ValidateGroupJournalIdentity(&restored) != nil {
		t.Fatal("group identity lost through JSON")
	}
	restored.Phase = "Accepted"
	restored.TaskID = "task"
	if err := ValidateGroupJournalIdentity(&restored); err != nil {
		t.Fatal("phase advancement changed binding", err)
	}
	if _, err := OperationDigest(&restored); err == nil {
		t.Fatal("group accepted by v1")
	}
	clone := prepared.DeepCopy()
	clone.PhysicalGroup.Targets[0].Device.UID = "replacement"
	if prepared.PhysicalGroup.Targets[0].Device.UID == "replacement" || ValidateGroupJournalIdentity(clone) == nil {
		t.Fatal("aliased or mutable target binding")
	}
	targets[0].PhysicalIdentity = "replacement"
	if _, err := WithGroupIdentity(prepared, targets); err == nil {
		t.Fatal("rebound existing prepared group")
	}
	if _, err := WithGroupIdentity(&restored, prepared.PhysicalGroup.Targets); err == nil {
		t.Fatal("accepted operation rebound")
	}
}
func TestGroupJournalPrunedFieldsRefused(t *testing.T) {
	for _, mode := range []string{"group", "digest", "version", "target", "source", "legacy-serial"} {
		t.Run(mode, func(t *testing.T) {
			op, targets := groupIdentityFixture()
			record, err := WithGroupIdentity(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "group":
				record.PhysicalGroup = nil
			case "digest":
				record.PhysicalGroup.Digest = ""
			case "version":
				record.PhysicalGroup.Version = "future"
			case "target":
				record.PhysicalGroup.Targets = record.PhysicalGroup.Targets[:1]
			case "source":
				record.ConfigUID = "replacement"
			case "legacy-serial":
				record.TargetSerial = "serial-a"
			}
			if ValidateGroupJournalIdentity(record) == nil {
				t.Fatal("pruned/changed group accepted")
			}
		})
	}
}
