// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const (
	rmaMark    = "MARK_FOR_REPLACEMENT"
	rmaReplace = "REPLACE"
)

type deviceReplacementIntent struct {
	site              string
	action            string
	replacementSerial string
}

// RMA is an independent operation on a PROVISION device, not a new device
// provisioning state. Preserve the original source for eventual journal binding;
// this projection only reuses existing inventory field validation. It grants no
// execution authority and does not imply that removing rma authorizes an unmark.
func parseRMAInventory(raw []byte) (inventoryIntent, map[string]string, map[string]deviceReplacementIntent, error) {
	return parseRMAInventoryProjection(raw, true)
}

func parseRMAInventoryProjection(raw []byte, requireActions bool) (inventoryIntent, map[string]string, map[string]deviceReplacementIntent, error) {
	var zero inventoryIntent
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return zero, nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return zero, nil, nil, errInvalidResponse
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return zero, nil, nil, errInvalidResponse
	}
	rows, ok := inventory["devices"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return zero, nil, nil, errInvalidResponse
	}
	actions := map[string]deviceReplacementIntent{}
	for _, row := range rows {
		device, ok := row.(map[string]any)
		if !ok {
			return zero, nil, nil, errInvalidResponse
		}
		value, present := device["rma"]
		if !present {
			continue
		}
		state, _ := device["state"].(string)
		name, _ := device["name"].(string)
		fields, ok := value.(map[string]any)
		if !ok || state != "PROVISION" || name == "" {
			return zero, nil, nil, errInvalidResponse
		}
		action, ok := fields["action"].(string)
		if !ok || (action != rmaMark && action != rmaReplace) {
			return zero, nil, nil, errInvalidResponse
		}
		for key := range fields {
			if key != "action" && key != "replacement_serial_number" {
				return zero, nil, nil, errInvalidResponse
			}
		}
		want := deviceReplacementIntent{action: action}
		if replacement, exists := fields["replacement_serial_number"]; exists {
			want.replacementSerial, ok = replacement.(string)
			if !ok {
				return zero, nil, nil, errInvalidResponse
			}
			if _, err := topologyidentity.CanonicalPhysicalIdentity(want.replacementSerial); err != nil {
				return zero, nil, nil, errInvalidResponse
			}
		}
		serial, _ := device["serial_number"].(string)
		if action == rmaReplace && (serial == "" || want.replacementSerial == "") {
			return zero, nil, nil, errInvalidResponse
		}
		if serial != "" {
			canonical, err := topologyidentity.CanonicalPhysicalIdentity(serial)
			if err != nil {
				return zero, nil, nil, errInvalidResponse
			}
			if want.replacementSerial != "" {
				replacement, _ := topologyidentity.CanonicalPhysicalIdentity(want.replacementSerial)
				if canonical == replacement {
					return zero, nil, nil, errInvalidResponse
				}
			}
		}
		if _, exists := actions[name]; exists {
			return zero, nil, nil, errInvalidResponse
		}
		actions[name] = want
		delete(device, "rma")
	}
	if requireActions && len(actions) == 0 {
		return zero, nil, nil, errInvalidResponse
	}
	projected, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return zero, nil, nil, err
	}
	common, states, err := parseWiredInventoryIntent(projected)
	if err != nil {
		return zero, nil, nil, err
	}
	// A replacement must not also be another declared device or be reused by
	// another operation in this model. Compare the same canonical hardware IDs
	// used by CVK's physical admission rather than API/display casing.
	serials := map[string]bool{}
	for _, device := range common.devices {
		if action, exists := actions[device.reference.Name]; exists {
			action.site = device.site
			actions[device.reference.Name] = action
		}
		if device.reference.Serial == "" {
			continue
		}
		serial, err := topologyidentity.CanonicalPhysicalIdentity(device.reference.Serial)
		if err != nil || serials[serial] {
			return zero, nil, nil, errInvalidResponse
		}
		serials[serial] = true
	}
	for _, action := range actions {
		if action.replacementSerial == "" {
			continue
		}
		serial, _ := topologyidentity.CanonicalPhysicalIdentity(action.replacementSerial)
		if serials[serial] {
			return zero, nil, nil, errInvalidResponse
		}
		serials[serial] = true
	}
	return common, states, actions, nil
}
