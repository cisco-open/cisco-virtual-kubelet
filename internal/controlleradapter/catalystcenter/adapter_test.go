// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"reflect"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

func TestDescriptor(t *testing.T) {
	d := Descriptor()
	if d.Type != TypeName || d.WorkerClusterRole != controlleradapter.CatalystCenterWorkerClusterRole {
		t.Fatalf("unexpected descriptor: %+v", d)
	}
	if !reflect.DeepEqual(d.NetAsCode.Sections, []string{"sites", "network_settings", "network_profiles", "fabric", "templates", "inventory", "wireless", "lan_automation", "system_settings", "authentication_templates", "feature_templates"}) {
		t.Fatalf("unexpected sections: %v", d.NetAsCode.Sections)
	}
	if !reflect.DeepEqual(d.Capabilities, []string{CapabilityHealth, CapabilityInventory, CapabilitySWIM, CapabilityPnPRegistration, ciskov1.NetworkControllerMetadataHandoffProtocol, "private-input-comparison", "lan-device-verification", "lan-link-apply", "lan-link-delete", "lan-link-compose", "feature-config-deletion", "discovery-config-deletion", "tag-config-deletion"}) {
		t.Fatalf("unexpected capabilities: %v", d.Capabilities)
	}
}
