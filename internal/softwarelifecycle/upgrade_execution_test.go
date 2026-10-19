// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package softwarelifecycle

import "testing"

func TestUpgradeExecutionRoutes(t *testing.T) {
	direct, err := (UpgradeExecution{}).Resolve()
	if err != nil || direct.Method != UpgradeDirect {
		t.Fatalf("default: %+v %v", direct, err)
	}
	cc := UpgradeExecution{Method: UpgradeCatalystCenter, ControllerNamespace: "lab", ControllerName: "catc", ControllerUID: "uid"}
	if _, err := cc.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err := cc.ValidatePinned(direct); err == nil {
		t.Fatal("Direct operation switched to controller")
	}
	if err := direct.ValidatePinned(cc); err == nil {
		t.Fatal("controller operation fell back to Direct")
	}
	replacement := cc
	replacement.ControllerUID = "replacement"
	if err := replacement.ValidatePinned(cc); err == nil {
		t.Fatal("controller replacement accepted")
	}
	for _, invalid := range []UpgradeExecution{{Method: "Auto"}, {Method: UpgradeCatalystCenter}, {ControllerName: "catc"}} {
		if _, err := invalid.Resolve(); err == nil {
			t.Fatalf("invalid route accepted: %+v", invalid)
		}
	}
}
