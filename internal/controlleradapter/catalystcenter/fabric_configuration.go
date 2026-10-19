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

type fabricConfiguration struct {
	configurationExecutor
	reports  []func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
	gateways []*fabricGatewayDeployment
}

func (c *client) compileFabricConfiguration(raw []byte) (*fabricConfiguration, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("fabric requires a section-specific model")
	}
	fabric, ok := root["fabric"].(map[string]any)
	if !ok || len(fabric) == 0 {
		return nil, errors.New("invalid fabric model")
	}
	for key := range fabric {
		if key != "transits" && key != "l3_virtual_networks" && key != "fabric_sites" && key != "extranet_policies" {
			return nil, errors.New("unsupported fabric collection")
		}
	}
	anchors := map[string]string{}
	if value, present := fabric["l3_virtual_networks"]; present {
		definitions, parsed, err := projectFabricL3Anchors(value)
		if err != nil {
			return nil, err
		}
		fabric["l3_virtual_networks"], anchors = definitions, parsed
	}
	out := &fabricConfiguration{}
	parts := []configurationPart{}
	definitionsConsumed := false
	multicast := []fabricMulticastSiteIntent{}
	wirelessMappings := []fabricVLANSSIDIntent{}
	authentication := []fabricAuthIntent{}
	for _, key := range []string{"fabric_sites", "l3_virtual_networks", "transits", "extranet_policies"} {
		value, ok := fabric[key]
		if !ok {
			continue
		}
		projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{key: value}}})
		switch key {
		case "fabric_sites":
			base, overrides, err := projectFabricAuthentication(value)
			if err != nil {
				return nil, err
			}
			value = base
			authentication = append(authentication, overrides...)
			model, multicastSites, mappingSites, err := parseFabricSitesWithWirelessSSIDs(value)
			if err != nil {
				return nil, err
			}
			multicast = append(multicast, multicastSites...)
			wirelessMappings = append(wirelessMappings, mappingSites...)
			desired, zones, networks, associations := model.sites, model.zones, model.l2, model.associations
			names := []string{}
			e := &fabricSiteExecutor{client: c, desired: desired}
			var executor configurationExecutor = e
			report := e.report
			if len(zones) > 0 {
				combined := &fabricSiteZoneExecutor{sites: e, zones: &fabricZoneExecutor{client: c, desired: zones}}
				executor, report = combined, combined.report
			}
			if len(associations) > 0 || len(model.gateways) > 0 {
				if value, present := fabric["l3_virtual_networks"]; present {
					raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"l3_virtual_networks": value}}})
					names, err = parseFabricL3Networks(raw)
					if err != nil {
						return nil, err
					}
					definitionsConsumed = true
				}
				combined := &fabricAssociatedDeployment{parents: executor, parentReport: report, definitions: &fabricL3Executor{client: c, names: names, anchors: anchors}, associations: &fabricL3AssociationExecutor{client: c, desired: associations}, l2: &fabricL2Executor{client: c, desired: networks}}
				executor, report = combined, combined.report
			} else if len(networks) > 0 {
				combined := &fabricSiteL2Executor{parents: executor, parentReport: report, networks: &fabricL2Executor{client: c, desired: networks}}
				executor, report = combined, combined.report
			}
			if len(model.gateways) > 0 {
				combined := &fabricGatewayDeployment{foundation: executor, foundationReport: report, gateways: &anycastGatewayExecutor{client: c, desired: model.gateways}, sites: desired, zones: zones, definitions: names, associations: associations}
				out.gateways = append(out.gateways, combined)
				executor, report = combined, combined.report
			}
			parts = append(parts, configurationPart{name: key, executor: executor})
			out.reports = append(out.reports, report)
		case "l3_virtual_networks":
			if definitionsConsumed {
				continue
			}
			names, err := parseFabricL3Networks(projection)
			if err != nil {
				return nil, err
			}
			e := &fabricL3Executor{client: c, names: names, anchors: anchors}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		case "extranet_policies":
			desired, err := parseFabricExtranets(value)
			if err != nil {
				return nil, err
			}
			e := &fabricExtranetExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		case "transits":
			desired, err := parseFabricTransits(projection)
			if err != nil {
				return nil, err
			}
			e := &fabricTransitExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		}
	}
	if len(parts) == 1 {
		out.configurationExecutor = parts[0].executor
	} else {
		out.configurationExecutor = &configurationGroup{parts: parts}
	}
	if len(multicast) > 0 {
		gated := &fabricMulticastConfiguration{client: c, foundation: out.configurationExecutor, desired: multicast}
		out.configurationExecutor = gated
		out.reports = append(out.reports, gated.report)
	}
	if len(wirelessMappings) > 0 {
		gated := &fabricVLANSSIDConfiguration{client: c, foundation: out.configurationExecutor, desired: wirelessMappings}
		out.configurationExecutor = gated
		out.reports = append(out.reports, gated.report)
	}
	if len(anchors) > 0 {
		out.configurationExecutor = &fabricAnchorAdmissionGate{configurationExecutor: out.configurationExecutor}
	}
	if _, present := fabric["extranet_policies"]; present {
		out.configurationExecutor = &fabricExtranetAdmissionGate{configurationExecutor: out.configurationExecutor}
	}
	if len(authentication) > 0 {
		gated := &fabricAuthConfiguration{client: c, foundation: out.configurationExecutor, desired: authentication}
		out.configurationExecutor = gated
		out.reports = append(out.reports, gated.report)
	}
	return out, nil
}
func (e *fabricConfiguration) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
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
