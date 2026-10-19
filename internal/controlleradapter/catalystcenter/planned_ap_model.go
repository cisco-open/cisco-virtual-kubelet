// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"errors"
	"math"
	"net"
	"sort"
	"strings"
	"unicode"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

var errPlannedAPModel = errors.New("invalid or unresolved planned access point model")

type plannedAPIntent struct {
	floor   string
	payload map[string]any
}

// Split floor-design resources from geometry without weakening the existing site
// parser. The decoded tree is private; the original source and secret pointers
// are never rewritten. Floor images retain their separate unsupported contract.
func parseSitesWithPlannedAPs(raw []byte) ([]desiredSite, []plannedAPIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, nil, errPlannedAPModel
	}
	sites, ok := root["sites"].(map[string]any)
	if !ok {
		return nil, nil, errPlannedAPModel
	}
	out := []plannedAPIntent{}
	if value, present := sites["floors"]; present {
		floors, ok := value.([]any)
		if !ok {
			return nil, nil, errPlannedAPModel
		}
		for _, value := range floors {
			floor, ok := value.(map[string]any)
			if !ok {
				return nil, nil, errPlannedAPModel
			}
			value, present := floor["planned_access_points"]
			if !present {
				continue
			}
			entries, ok := value.([]any)
			if !ok || len(entries) > 100 {
				return nil, nil, errPlannedAPModel
			}
			name, nok := floor["name"].(string)
			parent, pok := floor["parent_name"].(string)
			if !nok || !pok {
				return nil, nil, errPlannedAPModel
			}
			seen := map[string]bool{}
			for _, entry := range entries {
				ap, ok := entry.(map[string]any)
				if !ok {
					return nil, nil, errPlannedAPModel
				}
				payload, err := parsePlannedAPPayload(ap)
				if err != nil {
					return nil, nil, err
				}
				nameAP := payload["name"].(string)
				if seen[nameAP] {
					return nil, nil, errPlannedAPModel
				}
				seen[nameAP] = true
				out = append(out, plannedAPIntent{floor: parent + "/" + name, payload: payload})
			}
			delete(floor, "planned_access_points")
		}
	}
	geometry, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return nil, nil, errPlannedAPModel
	}
	desired, err := parseSites(geometry)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].floor != out[j].floor {
			return out[i].floor < out[j].floor
		}
		return out[i].payload["name"].(string) < out[j].payload["name"].(string)
	})
	return desired, out, nil
}

func plannedAPText(value any) (string, error) {
	s, ok := value.(string)
	if !ok || s == "" || len(s) > 256 || strings.TrimSpace(s) != s || strings.ContainsFunc(s, unicode.IsControl) {
		return "", errPlannedAPModel
	}
	return s, nil
}
func plannedAPNumber(value any, integer bool) (float64, error) {
	raw, err := json.Marshal(value)
	var number float64
	if err != nil || value == nil || json.Unmarshal(raw, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) || (integer && math.Trunc(number) != number) {
		return 0, errPlannedAPModel
	}
	return number, nil
}
func parsePlannedAPPayload(entry map[string]any) (map[string]any, error) {
	for key := range entry {
		switch key {
		case "name", "ap_type", "position_x", "position_y", "position_z", "mac_address", "radios":
		default:
			return nil, errPlannedAPModel
		}
	}
	name, err := plannedAPText(entry["name"])
	if err != nil {
		return nil, err
	}
	kind, err := plannedAPText(entry["ap_type"])
	if err != nil {
		return nil, err
	}
	position := map[string]any{}
	for _, axis := range []string{"x", "y", "z"} {
		value, present := entry["position_"+axis]
		if axis == "z" && !present {
			continue
		}
		number, err := plannedAPNumber(value, false)
		if err != nil {
			return nil, err
		}
		position[axis] = number
	}
	out := map[string]any{"name": name, "type": kind, "position": position}
	if value, present := entry["mac_address"]; present {
		text, err := plannedAPText(value)
		if err != nil {
			return nil, err
		}
		mac, err := net.ParseMAC(text)
		if err != nil || len(mac) != 6 {
			return nil, errPlannedAPModel
		}
		out["macAddress"] = mac.String()
	}
	if value, present := entry["radios"]; present {
		radios, ok := value.([]any)
		if !ok || len(radios) > 4 {
			return nil, errPlannedAPModel
		}
		mapped := []any{}
		for _, value := range radios {
			radio, ok := value.(map[string]any)
			if !ok {
				return nil, errPlannedAPModel
			}
			parsed, err := parsePlannedAPRadio(radio)
			if err != nil {
				return nil, err
			}
			mapped = append(mapped, parsed)
		}
		out["radios"] = mapped
	}
	return out, nil
}
func parsePlannedAPRadio(radio map[string]any) (map[string]any, error) {
	out, antenna := map[string]any{}, map[string]any{}
	for key, value := range radio {
		switch key {
		case "band":
			band, err := plannedAPNumber(value, false)
			if err != nil || (band != 2.4 && band != 5 && band != 6) {
				return nil, errPlannedAPModel
			}
			out["bands"] = []float64{band}
		case "channel", "tx_power", "antenna_azimuth", "antenna_elevation":
			number, err := plannedAPNumber(value, true)
			if err != nil {
				return nil, err
			}
			switch key {
			case "channel":
				out["channel"] = number
			case "tx_power":
				out["txPower"] = number
			case "antenna_azimuth":
				if number < 0 || number > 360 {
					return nil, errPlannedAPModel
				}
				antenna["azimuth"] = number
			case "antenna_elevation":
				if number < -90 || number > 90 {
					return nil, errPlannedAPModel
				}
				antenna["elevation"] = number
			}
		case "antenna_name":
			text, err := plannedAPText(value)
			if err != nil {
				return nil, err
			}
			antenna["name"] = text
		default:
			return nil, errPlannedAPModel
		}
	}
	if len(antenna) > 0 {
		out["antenna"] = antenna
	}
	return out, nil
}
