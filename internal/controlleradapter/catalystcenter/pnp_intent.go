// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

type pnpTemplateIntent struct {
	Name      string `json:"name"`
	Variables []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"variables"`
}
type pnpStackIntent struct {
	TopSerial string `json:"top_of_stack_serial_number"`
	Cabling   string `json:"cabling_scheme"`
}
type pnpDeviceIntent struct {
	Name      string             `json:"name"`
	FQDN      string             `json:"fqdn_name,omitempty"`
	IP        string             `json:"device_ip,omitempty"`
	State     string             `json:"state"`
	Serial    string             `json:"serial_number"`
	Site      string             `json:"site"`
	PID       string             `json:"pid"`
	Type      string             `json:"type,omitempty"`
	RFProfile string             `json:"rf_profile,omitempty"`
	ImageID   string             `json:"image_id,omitempty"`
	ImageName string             `json:"image_name,omitempty"`
	ImageSkip *bool              `json:"image_skip,omitempty"`
	Template  *pnpTemplateIntent `json:"onboarding_template,omitempty"`
	Stack     *pnpStackIntent    `json:"stack,omitempty"`
}

func (pnpDeviceIntent) String() string   { return "<PnP device intent>" }
func (pnpDeviceIntent) GoString() string { return "<PnP device intent>" }

func parsePnPInventory(raw []byte) ([]pnpDeviceIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errInvalidResponse
	}
	section, ok := root["inventory"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errInvalidResponse
	}
	rows, ok := section["devices"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	wants := []pnpDeviceIntent{}
	common := []map[string]any{}
	serials := map[string]bool{}
	for _, row := range rows {
		body, err := json.Marshal(row)
		if err != nil {
			return nil, errInvalidResponse
		}
		var want pnpDeviceIntent
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&want) != nil {
			return nil, errInvalidResponse
		}
		members, err := validatePnPDeviceIntent(want)
		if err != nil {
			return nil, err
		}
		for _, serial := range members {
			if serials[serial] {
				return nil, errInvalidResponse
			}
			serials[serial] = true
		}
		// Reuse common name/site/FQDN/IP validation after validating all PnP
		// fields. The first serial is the upstream registration identity.
		device := map[string]any{"name": want.Name, "site": want.Site, "state": "ASSIGN", "serial_number": strings.Split(want.Serial, ",")[0]}
		if want.FQDN != "" {
			device["fqdn_name"] = want.FQDN
		}
		if want.IP != "" {
			device["device_ip"] = want.IP
		}
		common = append(common, device)
		wants = append(wants, want)
	}
	projection, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": common}}})
	if err != nil {
		return nil, err
	}
	if _, err := parseInventoryIntent(projection); err != nil {
		return nil, err
	}
	return wants, nil
}

func validatePnPDeviceIntent(w pnpDeviceIntent) ([]string, error) {
	if w.State != "PNP" || !validProfileName(w.Name) || !validProfileName(w.PID) || !validSitePath(w.Site) || w.Site == "Global" {
		return nil, errInvalidResponse
	}
	switch w.Type {
	case "", "Default", "StackSwitch", "AccessPoint", "Sensor", "CatalystWLC", "MobilityExpress":
	default:
		return nil, errInvalidResponse
	}
	members := strings.Split(w.Serial, ",")
	if len(members) > 100 {
		return nil, errInvalidResponse
	}
	seen := map[string]bool{}
	canonical := []string{}
	for _, serial := range members {
		key, err := topologyidentity.CanonicalPhysicalIdentity(serial)
		if err != nil || strings.TrimSpace(serial) != serial || seen[key] {
			return nil, errInvalidResponse
		}
		seen[key] = true
		canonical = append(canonical, key)
	}
	if len(members) == 1 && (w.Stack != nil || w.Type == "StackSwitch") || len(members) > 1 && w.Type != "" && w.Type != "Default" && w.Type != "StackSwitch" {
		return nil, errInvalidResponse
	}
	if w.Stack != nil {
		if w.Stack.TopSerial != "" {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(w.Stack.TopSerial)
			if err != nil || !seen[serial] {
				return nil, errInvalidResponse
			}
		}
		if w.Stack.Cabling != "" && w.Stack.Cabling != "1A" && w.Stack.Cabling != "1B" {
			return nil, errInvalidResponse
		}
	}
	if w.ImageID != "" && !validAPIID(w.ImageID) || w.ImageName != "" && !validProfileName(w.ImageName) || w.RFProfile != "" && !validProfileName(w.RFProfile) {
		return nil, errInvalidResponse
	}
	if w.Template != nil {
		parts := strings.Split(w.Template.Name, "#")
		if len(parts) > 2 || len(w.Template.Variables) > 1000 {
			return nil, errInvalidResponse
		}
		for _, part := range parts {
			if !validProfileName(part) {
				return nil, errInvalidResponse
			}
		}
		names := map[string]bool{}
		for _, v := range w.Template.Variables {
			if !validProfileName(v.Name) || names[v.Name] || len(v.Value) > 65536 || strings.ContainsRune(v.Value, 0) {
				return nil, errInvalidResponse
			}
			names[v.Name] = true
		}
	}
	return canonical, nil
}

func pnpRegistrationPayload(want pnpDeviceIntent) ([]byte, error) {
	members, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	return pnpRegistrationIdentityPayload(strings.Split(want.Serial, ",")[0], want.Name, want.PID, len(members) > 1)
}

// Shared native registration shape for declared intent and retained canonical
// identity. Validation/ownership remains with each caller; no site is invented.
func pnpRegistrationIdentityPayload(serial, name, pid string, stack bool) ([]byte, error) {
	info := map[string]any{"serialNumber": serial, "hostname": name, "pid": pid}
	if stack {
		info["stack"] = true
	}
	return json.Marshal(map[string]any{"deviceInfo": info})
}
