// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

var errWirelessSSIDModel = errors.New("invalid SSID model")

type wirelessSSIDIntent struct {
	name        string
	metadata    map[string]any
	credentials []configurationCredentialIntent
}

var wirelessSSIDRadioLabels = map[string]string{
	"Triple Band": "Triple band operation(2.4GHz, 5GHz and 6GHz)",
	"5GHz":        "5GHz only", "2.4GHz": "2.4GHz only", "6GHz": "6GHz only",
	"2.4GHz and 5GHz": "2.4 and 5 GHz", "2.4GHz and 6GHz": "2.4 and 6 GHz", "5GHz and 6GHz": "5 and 6 GHz",
}

var wirelessSSIDSets = map[string]string{"nas_options": "nasOptions", "auth_servers": "authServers", "acct_servers": "acctServers"}

func wirelessSSIDStringSet(value any) ([]string, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errWirelessSSIDModel
	}
	out := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		s, ok := row.(string)
		if !ok || s == "" || strings.ContainsAny(s, "\x00\r\n") {
			return nil, errWirelessSSIDModel
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, nil
}

func parseWirelessSSIDs(raw []byte) ([]wirelessSSIDIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errWirelessSSIDModel
	}
	section, ok := root["wireless"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errWirelessSSIDModel
	}
	rows, ok := section["ssids"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errWirelessSSIDModel
	}
	out := []wirelessSSIDIntent{}
	seen := map[string]bool{}
	for index, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, errWirelessSSIDModel
		}
		for key, v := range row {
			if key == "passphrase" || key == "passphrase_wo" {
				return nil, errConfigurationSecret
			}
			if v == nil {
				return nil, errWirelessSSIDModel
			}
			_, scalar := wirelessSSIDFields[key]
			_, set := wirelessSSIDSets[key]
			if !scalar && !set && key != "multi_psk_settings" && key != "passphrase_version" {
				return nil, errWirelessSSIDModel
			}
		}
		// This is the only SSID default inherited from the pinned module's
		// enterprise_ssids defaults. Do not invent defaults for other fields.
		intent := wirelessSSIDIntent{metadata: map[string]any{"clientExclusionTimeout": int64(180)}}
		for key, field := range wirelessSSIDFields {
			v, present := row[key]
			if !present {
				continue
			}
			if key == "auth_type" && v == "OPEN_SECURED" {
				v = "OPEN-SECURED"
			}
			if key == "ssid_radio_type" {
				label, ok := v.(string)
				if !ok {
					return nil, errWirelessSSIDModel
				}
				mapped, ok := wirelessSSIDRadioLabels[label]
				if !ok {
					return nil, errWirelessSSIDModel
				}
				v = mapped
			}
			if key == "cckm_tsf_tolerance" || key == "client_rate_limit" || key == "session_timeout" {
				n, err := wirelessAPInteger(v)
				if err != nil {
					return nil, errWirelessSSIDModel
				}
				if n == 0 {
					continue
				}
			}
			normalized, err := wirelessAPValue(field, v)
			if err != nil {
				return nil, errWirelessSSIDModel
			}
			intent.metadata[field.path] = normalized
		}
		for _, required := range []string{"ssid", "authType", "wlanType"} {
			if _, ok := intent.metadata[required]; !ok {
				return nil, errWirelessSSIDModel
			}
		}
		intent.name = intent.metadata["ssid"].(string)
		if seen[intent.name] {
			return nil, errWirelessSSIDModel
		}
		seen[intent.name] = true
		for key, path := range wirelessSSIDSets {
			if value, ok := row[key]; ok {
				set, err := wirelessSSIDStringSet(value)
				if err != nil {
					return nil, err
				}
				intent.metadata[path] = set
			}
		}
		if value, ok := row["passphrase_version"]; ok {
			version, err := wirelessAPInteger(value)
			if err != nil || version < 1 {
				return nil, errConfigurationSecret
			}
			intent.credentials = append(intent.credentials, configurationCredentialIntent{path: "/ssids/" + strconv.Itoa(index) + "/passphrase", field: "passphrase", key: "passphrase", version: version})
		}
		if value, ok := row["multi_psk_settings"]; ok {
			settings, credentials, err := parseWirelessSSIDMultiPSK(value, index)
			if err != nil {
				return nil, err
			}
			intent.metadata["multiPSKSettings"] = settings
			intent.credentials = append(intent.credentials, credentials...)
		}
		sort.Slice(intent.credentials, func(i, j int) bool { return intent.credentials[i].path < intent.credentials[j].path })
		out = append(out, intent)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func parseWirelessSSIDMultiPSK(value any, sourceIndex int) ([]map[string]any, []configurationCredentialIntent, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 5 {
		return nil, nil, errWirelessSSIDModel
	}
	out := []map[string]any{}
	credentials := []configurationCredentialIntent{}
	seen := map[int64]bool{}
	for index, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, nil, errWirelessSSIDModel
		}
		for key, v := range row {
			if key == "passphrase" || key == "passphrase_wo" {
				return nil, nil, errConfigurationSecret
			}
			if v == nil || (key != "priority" && key != "passphrase_type" && key != "passphrase_wo_version") {
				return nil, nil, errWirelessSSIDModel
			}
		}
		priority, err := wirelessAPInteger(row["priority"])
		if err != nil || priority < 0 || priority > 4 || seen[priority] {
			return nil, nil, errWirelessSSIDModel
		}
		seen[priority] = true
		metadata := map[string]any{"priority": priority}
		if v, ok := row["passphrase_type"]; ok {
			if v != "ASCII" && v != "HEX" {
				return nil, nil, errWirelessSSIDModel
			}
			metadata["passphraseType"] = v
		}
		if value, ok := row["passphrase_wo_version"]; ok {
			version, err := wirelessAPInteger(value)
			if err != nil || version < 1 {
				return nil, nil, errConfigurationSecret
			}
			credentials = append(credentials, configurationCredentialIntent{path: "/ssids/" + strconv.Itoa(sourceIndex) + "/multi_psk_settings/" + strconv.Itoa(index) + "/passphrase_wo", field: "multiPSKSettings." + strconv.Itoa(index) + ".passphrase", key: "multi-psk:" + strconv.FormatInt(priority, 10), version: version})
		}
		out = append(out, metadata)
	}
	return out, credentials, nil
}
