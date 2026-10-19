// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"strconv"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func wirelessSSIDCredentialKey(source *configSourceSnapshot, path string, version int64) (string, error) {
	parts, err := configurationSecretPointer(path)
	if err != nil || len(parts) != 3 && len(parts) != 5 || parts[0] != "ssids" || source == nil {
		return "", errConfigurationSecret
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return "", errConfigurationSecret
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return "", errConfigurationSecret
	}
	wireless, _ := root["wireless"].(map[string]any)
	rows, _ := wireless["ssids"].([]any)
	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 0 || index >= len(rows) || strconv.Itoa(index) != parts[1] {
		return "", errConfigurationSecret
	}
	row, ok := rows[index].(map[string]any)
	if !ok {
		return "", errConfigurationSecret
	}
	if len(parts) == 3 {
		n, err := wirelessAPInteger(row["passphrase_version"])
		if parts[2] != "passphrase" || err != nil || n != version || n < 1 {
			return "", errConfigurationSecret
		}
		return "passphrase", nil
	}
	if parts[2] != "multi_psk_settings" || parts[4] != "passphrase_wo" {
		return "", errConfigurationSecret
	}
	entries, _ := row["multi_psk_settings"].([]any)
	i, err := strconv.Atoi(parts[3])
	if err != nil || i < 0 || i >= len(entries) || strconv.Itoa(i) != parts[3] {
		return "", errConfigurationSecret
	}
	entry, ok := entries[i].(map[string]any)
	if !ok {
		return "", errConfigurationSecret
	}
	priority, err := wirelessAPInteger(entry["priority"])
	if err != nil || priority < 0 || priority > 4 {
		return "", errConfigurationSecret
	}
	n, err := wirelessAPInteger(entry["passphrase_wo_version"])
	if err != nil || n != version || n < 1 {
		return "", errConfigurationSecret
	}
	return "multi-psk:" + strconv.FormatInt(priority, 10), nil
}

// Project copies to semantic paths before using the established monotonic
// version/source checks. Persisted receipts retain their real source paths.
func wirelessSSIDCredentialRotation(prior, desired []ciskov1.NetworkControllerConfigurationCredentialVersion) (bool, error) {
	project := func(in []ciskov1.NetworkControllerConfigurationCredentialVersion) ([]ciskov1.NetworkControllerConfigurationCredentialVersion, error) {
		out := append([]ciskov1.NetworkControllerConfigurationCredentialVersion(nil), in...)
		for i := range out {
			key := out[i].Key
			if key != "passphrase" {
				value := strings.TrimPrefix(key, "multi-psk:")
				n, err := strconv.Atoi(value)
				if !strings.HasPrefix(key, "multi-psk:") || err != nil || n < 0 || n > 4 || strconv.Itoa(n) != value {
					return nil, errConfigurationSecret
				}
			}
			out[i].Path = key
		}
		return out, nil
	}
	old, err := project(prior)
	if err != nil {
		return false, err
	}
	next, err := project(desired)
	if err != nil {
		return false, err
	}
	return configurationCredentialRotation(old, next, func(path string) string { return path })
}
