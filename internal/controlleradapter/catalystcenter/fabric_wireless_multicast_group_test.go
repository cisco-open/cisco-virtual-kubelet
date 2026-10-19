// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"strings"
	"testing"
)

func TestFabricWirelessMulticastGroupResource(t *testing.T) {
	prefix := "fabric-wireless-multicast-update:"
	for _, suffix := range []string{strings.Repeat("a", 64), "", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 64) + ":extra"} {
		resource := prefix + suffix
		if !isReservedConfigurationGroupResource("fabric", resource) {
			t.Fatal("malformed or pruned resource fell through")
		}
		valid := suffix == strings.Repeat("a", 64)
		if validConfigurationGroupAction("fabric", resource, "Update") != valid {
			t.Fatal("invalid group action accepted", suffix)
		}
		if validConfigurationGroupAction("fabric", resource, "Create") || validConfigurationGroupAction("wireless", resource, "Update") {
			t.Fatal("wrong action/section admitted")
		}
	}
}
