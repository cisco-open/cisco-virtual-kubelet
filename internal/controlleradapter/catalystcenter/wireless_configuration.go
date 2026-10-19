// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type wirelessConfiguration struct {
	configurationExecutor
	ap           *wirelessAPExecutor
	ssid         *wirelessSSIDExecutor
	plannedACL   map[string]bool
	plannedPower map[string]bool
	reports      []func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
}

func (c *client) compileWirelessConfiguration(raw []byte, bind ...func(*wirelessConfiguration) error) (*wirelessConfiguration, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("wireless requires a section-specific model")
	}
	wireless, ok := root["wireless"].(map[string]any)
	if !ok || len(wireless) == 0 {
		return nil, errors.New("invalid wireless model")
	}
	for key := range wireless {
		if key != "security_settings" && key != "interfaces" && key != "power_profiles" && key != "dot11be_profiles" && key != "anchor_groups" && key != "ap_profiles" && key != "rf_profiles" && key != "ssids" {
			return nil, errors.New("unsupported wireless collection")
		}
	}
	out := &wirelessConfiguration{plannedPower: map[string]bool{}, plannedACL: map[string]bool{}}
	parts := []configurationPart{}
	for _, key := range []string{"interfaces", "security_settings", "power_profiles", "dot11be_profiles", "anchor_groups", "rf_profiles", "ap_profiles", "ssids"} {
		value, ok := wireless[key]
		if !ok {
			continue
		}
		projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": map[string]any{key: value}}})
		switch key {
		case "ssids":
			desired, err := parseWirelessSSIDs(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessSSIDExecutor{client: c, desired: desired}
			out.ssid = e
			out.reports = append(out.reports, e.report)
		case "rf_profiles":
			desired, err := parseWirelessRFProfiles(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessRFExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)

		case "ap_profiles":
			desired, err := parseWirelessAPProfiles(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessAPExecutor{client: c, desired: desired}
			out.ap = e
			out.reports = append(out.reports, e.report)

		case "anchor_groups":
			desired, err := parseWirelessAnchors(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessAnchorExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)

		case "interfaces":
			desired, err := parseWirelessInterfaces(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessInterfaceExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		case "dot11be_profiles":
			desired, err := parseWirelessDot11be(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessDot11beExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		case "power_profiles":
			desired, err := parseWirelessPower(projection)
			if err != nil {
				return nil, err
			}
			for _, profile := range desired {
				out.plannedPower[profile.Name] = true
			}
			e := &wirelessPowerExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		case "security_settings":
			desired, err := parseWirelessACLs(projection)
			if err != nil {
				return nil, err
			}
			for _, acl := range desired {
				out.plannedACL[acl.Name] = true
			}
			e := &wirelessACLExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		}
	}
	if len(bind) > 1 {
		return nil, errConfigurationSecret
	}
	if len(bind) == 1 {
		if err := bind[0](out); err != nil {
			return nil, err
		}
	} else if len(out.credentialLeaves()) > 0 {
		return nil, errConfigurationSecret
	}
	if len(parts) == 1 {
		out.configurationExecutor = parts[0].executor
	} else {
		out.configurationExecutor = &configurationGroup{parts: parts}
	}
	return out, nil
}
func (e *wirelessConfiguration) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	out := []configapi.NetworkControllerDriftEntry{}
	for _, report := range e.reports {
		drift, err := report(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, drift...)
	}
	return out, nil
}
