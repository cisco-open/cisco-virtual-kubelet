// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type fabricL3IPHandoffDeploymentIntent struct {
	handoff fabricL3IPHandoffIntent
	device  fabricDeviceIntent
}

// Extract only implemented handoff fields and delegate all remaining device and
// border validation to the existing strict parser. This private compiler is not
// a public execution gate; callers must schedule every returned resource.
func parseAllFabricHandoffDeployment(raw []byte) ([]fabricDeviceIntent, []fabricL2HandoffDeploymentIntent, []fabricL3IPHandoffDeploymentIntent, []fabricSDAHandoffDeploymentIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, nil, nil, nil, errInvalidResponse
	}
	l2 := map[string][]fabricL2HandoffIntent{}
	l3 := map[string][]fabricL3IPHandoffIntent{}
	sda := map[string]fabricSDAHandoffIntent{}
	if value, present := root["fabric"]; present {
		fabric, ok := value.(map[string]any)
		if !ok {
			return nil, nil, nil, nil, errInvalidResponse
		}
		if value, present := fabric["border_devices"]; present {
			borders, ok := value.([]any)
			if !ok || len(borders) > 1000 {
				return nil, nil, nil, nil, errInvalidResponse
			}
			seen := map[string]bool{}
			for _, value := range borders {
				border, ok := value.(map[string]any)
				if !ok {
					return nil, nil, nil, nil, errInvalidResponse
				}
				name, ok := border["name"].(string)
				if !ok || !validProfileName(name) || seen[name] {
					return nil, nil, nil, nil, errInvalidResponse
				}
				seen[name] = true
				if _, present := border["sda_transit"]; present {
					settings := map[string]any{}
					for _, field := range []string{"sda_transit", "affinity_id_prime", "affinity_id_decider", "connected_to_internet", "multicast_over_transit"} {
						if value, found := border[field]; found {
							settings[field] = value
							delete(border, field)
						}
					}
					handoff, err := parseFabricSDAHandoff(name, settings)
					if err != nil {
						return nil, nil, nil, nil, err
					}
					sda[name] = *handoff
				}
				if value, present := border["l2_handoffs"]; present {
					l2[name], err = parseFabricL2Handoffs(name, value)
					if err != nil {
						return nil, nil, nil, nil, err
					}
					delete(border, "l2_handoffs")
				}
				if value, present := border["l3_handoffs"]; present {
					pool := ""
					if value := border["external_handoff_pool"]; value != nil {
						pool, ok = value.(string)
						if !ok || !validPoolName(pool) {
							return nil, nil, nil, nil, errInvalidResponse
						}
					}
					l3[name], err = parseFabricL3IPHandoffs(name, pool, value)
					if err != nil {
						return nil, nil, nil, nil, err
					}
					delete(border, "l3_handoffs")
					delete(border, "external_handoff_pool")
				}
			}
		}
	}
	projected, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return nil, nil, nil, nil, errInvalidResponse
	}
	devices, err := parseFabricDeviceIntents(projected)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	out2, out3 := []fabricL2HandoffDeploymentIntent{}, []fabricL3IPHandoffDeploymentIntent{}
	outSDA := []fabricSDAHandoffDeploymentIntent{}
	for _, device := range devices {
		name := device.inventory.reference.Name
		tags := map[string]bool{}
		key := func(port string, vlan int) string { return strings.ToLower(port) + "\x00" + strconv.Itoa(vlan) }
		if rows, present := l2[name]; present {
			if device.border == nil || !slices.Contains(device.border.Types, "LAYER_2") {
				return nil, nil, nil, nil, errInvalidResponse
			}
			for _, row := range rows {
				tags[key(row.port, row.externalVLAN)] = true
				out2 = append(out2, fabricL2HandoffDeploymentIntent{handoff: row, device: device})
			}
			delete(l2, name)
		}
		if rows, present := l3[name]; present {
			if device.border == nil || !slices.Contains(device.border.Types, "LAYER_3") {
				return nil, nil, nil, nil, errInvalidResponse
			}
			for _, row := range rows {
				if tags[key(row.port, row.vlan)] {
					return nil, nil, nil, nil, errInvalidResponse
				}
				out3 = append(out3, fabricL3IPHandoffDeploymentIntent{handoff: row, device: device})
			}
			delete(l3, name)
		}
		if row, present := sda[name]; present {
			if device.border == nil || !slices.Contains(device.border.Types, "LAYER_3") || device.fabricSite == "" || device.fabricZone != "" {
				return nil, nil, nil, nil, errInvalidResponse
			}
			outSDA = append(outSDA, fabricSDAHandoffDeploymentIntent{handoff: row, device: device})
			delete(sda, name)
		}
		if len(out2)+len(out3)+len(outSDA) > 1000 {
			return nil, nil, nil, nil, errInvalidResponse
		}
	}
	if len(l2) != 0 || len(l3) != 0 || len(sda) != 0 {
		return nil, nil, nil, nil, errInvalidResponse
	}
	return devices, out2, out3, outSDA, nil
}

// Existing IP/Layer 2 callers cannot silently discard SDA transit work.
func parseFabricHandoffDeployment(raw []byte) ([]fabricDeviceIntent, []fabricL2HandoffDeploymentIntent, []fabricL3IPHandoffDeploymentIntent, error) {
	devices, l2, l3, sda, err := parseAllFabricHandoffDeployment(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(sda) != 0 {
		return nil, nil, nil, errConfigurationTransition
	}
	return devices, l2, l3, nil
}

type fabricSDAHandoffDeploymentIntent struct {
	handoff fabricSDAHandoffIntent
	device  fabricDeviceIntent
}
