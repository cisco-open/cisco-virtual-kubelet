// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"reflect"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

func TestDescriptor(t *testing.T) {
	d := Descriptor()
	if d.Type != TypeName || d.WorkerClusterRole != controlleradapter.DefaultWorkerClusterRole {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if !reflect.DeepEqual(d.NetAsCode.Sections, []string{"sites", "network_settings", "network_profiles", "fabric", "templates", "inventory", "wireless", "lan_automation", "system_settings"}) {
		t.Fatalf("unexpected sections: %v", d.NetAsCode.Sections)
	}
	if !reflect.DeepEqual(d.Capabilities, []string{CapabilityHealth, CapabilityInventory}) {
		t.Fatalf("unexpected capabilities: %v", d.Capabilities)
	}
}
