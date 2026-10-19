// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Consume one explicitly supported extension and validate the entire remaining
// device envelope through the existing fabric parser. Unknown effects cannot
// disappear through a projection. Empty declarations are not implicit deletes.
func parseFabricEdgeExtensions[T any](raw []byte, field string, normalize func([]T) ([]T, error)) ([]fabricDeviceIntent, map[string][]T, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, nil, errInvalidResponse
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok {
		return nil, nil, errInvalidResponse
	}
	devices, ok := inventory["devices"].([]any)
	if !ok || len(devices) > 1000 {
		return nil, nil, errInvalidResponse
	}
	extensions := map[string][]T{}
	count := 0
	for _, value := range devices {
		device, ok := value.(map[string]any)
		if !ok {
			return nil, nil, errInvalidResponse
		}
		name, ok := device["name"].(string)
		if !ok || !validProfileName(name) {
			return nil, nil, errInvalidResponse
		}
		value, present := device[field]
		if !present {
			continue
		}
		if _, duplicate := extensions[name]; duplicate {
			return nil, nil, errInvalidResponse
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, nil, errInvalidResponse
		}
		var rows []T
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if value == nil || decoder.Decode(&rows) != nil || len(rows) == 0 || len(rows) > 1000 {
			return nil, nil, errInvalidResponse
		}
		rows, err = normalize(rows)
		if err != nil || len(rows) == 0 || len(rows) > 1000 {
			return nil, nil, errInvalidResponse
		}
		count += len(rows)
		if count > 1000 {
			return nil, nil, errInvalidResponse
		}
		extensions[name] = rows
		delete(device, field)
	}
	encoded, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return nil, nil, errInvalidResponse
	}
	base, err := parseFabricDeviceIntents(encoded)
	if err != nil {
		return nil, nil, err
	}
	matched := 0
	for _, device := range base {
		if _, present := extensions[device.inventory.reference.Name]; present {
			if !slices.Contains(device.roles, "EDGE_NODE") {
				return nil, nil, errInvalidResponse
			}
			matched++
		}
	}
	if count == 0 || matched != len(extensions) {
		return nil, nil, errInvalidResponse
	}
	return base, extensions, nil
}
