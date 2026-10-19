// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/hex"
	"strings"
)

// Reserve prefixes independently of schema preservation. A pruned or malformed
// group operation must never fall through to the single-device lifecycle.
func isReservedConfigurationGroupResource(section, resource string) bool {
	if section == "fabric" && strings.HasPrefix(resource, sdaTransitCreateFamily) {
		// Staged SDA transit operations cannot fall through to metadata
		// settlement if their physical group is absent, malformed or pruned.
		return true
	}
	if section == "fabric" && strings.HasPrefix(resource, fabricAuthResourceFamily) {
		// Private staged operations cannot fall through to metadata/single-device
		// completion if group fields are missing or pruned. Public group action
		// validation intentionally remains disabled until admission is wired.
		return true
	}
	if isExtranetGroupResource(section, resource) {
		return true
	}
	if section == "lan_automation" && (strings.HasPrefix(resource, lanLinkAddFamily) || strings.HasPrefix(resource, lanLinkDeleteFamily)) {
		return true
	}
	if section == "fabric" && strings.HasPrefix(resource, fabricDevicesBulkFamily) {
		return true
	}
	if section == "inventory" && strings.HasPrefix(resource, wiredBulkProvisionPrefix) {
		return true
	}
	if section == "inventory" && strings.HasPrefix(resource, "fabric-ewlc-update:") {
		return true
	}
	return section == "fabric" && (strings.HasPrefix(resource, "fabric-replication-update:") || strings.HasPrefix(resource, "fabric-multicast-vn:")) || isPendingFabricEventOperation(section, resource) || section == "fabric" && (strings.HasPrefix(resource, "fabric-l2-handoff-create:") || strings.HasPrefix(resource, "fabric-l3-ip-handoff-create:") || strings.HasPrefix(resource, "fabric-sda-handoff-create:") || strings.HasPrefix(resource, "fabric-port-channel-create:") || strings.HasPrefix(resource, "fabric-port-assignment-create:") || strings.HasPrefix(resource, "fabric-wireless-multicast-update:") || strings.HasPrefix(resource, "fabric-vlan-ssid-update:"))
}

func validConfigurationGroupAction(section, resource, action string) bool {
	if section == "fabric" && strings.HasPrefix(resource, sdaTransitCreateFamily) {
		_, err := sdaTransitOperationTarget(resource, action)
		return err == nil
	}
	if section == "fabric" && strings.HasPrefix(resource, fabricAuthManagedPrefix) {
		_, err := parseFabricAuthManagedResource(resource)
		return action == "Update" && err == nil
	}
	if isExtranetGroupResource(section, resource) {
		_, err := extranetOperationTarget(resource, action)
		return err == nil
	}
	if section == "lan_automation" && (strings.HasPrefix(resource, lanLinkAddFamily) || strings.HasPrefix(resource, lanLinkDeleteFamily)) {
		return validLANLinkMutationResource(resource, action)
	}
	if section == "fabric" && strings.HasPrefix(resource, fabricDevicesBulkFamily) {
		_, _, err := parseFabricDevicesBulkResource(resource, action)
		return err == nil
	}
	if section == "inventory" && strings.HasPrefix(resource, wiredBulkProvisionPrefix) {
		_, _, _, err := parseWiredBulkResource(resource, action)
		return err == nil
	}
	if section == "fabric" && strings.HasPrefix(resource, "fabric-replication-update:") {
		parts := strings.Split(strings.TrimPrefix(resource, "fabric-replication-update:"), ":")
		if action != "Update" || len(parts) != 2 {
			return false
		}
		for _, part := range parts {
			if len(part) != 64 || strings.ToLower(part) != part {
				return false
			}
			if _, err := hex.DecodeString(part); err != nil {
				return false
			}
		}
		return true
	}
	if section == "fabric" && strings.HasPrefix(resource, "fabric-multicast-vn:") {
		parts := strings.Split(strings.TrimPrefix(resource, "fabric-multicast-vn:"), ":")
		if len(parts) != 3 || (action != "Create" && action != "Update") {
			return false
		}
		for _, part := range parts {
			if len(part) != 64 || strings.ToLower(part) != part {
				return false
			}
			if _, err := hex.DecodeString(part); err != nil {
				return false
			}
		}
		return true
	}
	if section == "inventory" && strings.HasPrefix(resource, "fabric-ewlc-update:") {
		_, _, err := parseFabricEWLCResource(resource)
		return action == "Update" && err == nil
	}
	if section == "fabric" && strings.HasPrefix(resource, "fabric-vlan-ssid-update:") {
		_, err := parseFabricVLANSSIDResource(resource)
		return action == "Update" && err == nil
	}
	if isPendingFabricEventOperation(section, resource) {
		return action == "Update"
	}
	if section == "fabric" && strings.HasPrefix(resource, "fabric-wireless-multicast-update:") {
		suffix := strings.TrimPrefix(resource, "fabric-wireless-multicast-update:")
		_, err := hex.DecodeString(suffix)
		return action == "Update" && len(suffix) == 64 && strings.ToLower(suffix) == suffix && err == nil
	}
	if section != "fabric" || action != "Create" {
		return false
	}
	suffix, ok := strings.CutPrefix(resource, "fabric-l2-handoff-create:")
	count := 2
	if !ok {
		suffix, ok = strings.CutPrefix(resource, "fabric-l3-ip-handoff-create:")
		count = 1
	}
	if !ok {
		suffix, ok = strings.CutPrefix(resource, "fabric-sda-handoff-create:")
	}
	if !ok {
		suffix, ok = strings.CutPrefix(resource, "fabric-port-channel-create:")
	}
	if !ok {
		suffix, ok = strings.CutPrefix(resource, "fabric-port-assignment-create:")
	}
	if !ok {
		return false
	}
	parts := strings.Split(suffix, ":")
	if len(parts) != count {
		return false
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return false
		}
		if _, err := hex.DecodeString(part); err != nil {
			return false
		}
	}
	return true
}
