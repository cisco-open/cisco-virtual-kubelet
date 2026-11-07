// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"net/url"
)

var attachmentQoSLeaves = []struct{ definition, suffix string }{
	{"qos-adaptive.period", "qos-adaptive/period"},
	{"qos-adaptive.downstream.bandwidth-down", "qos-adaptive/downstream/bandwidth-down"},
	{"qos-adaptive.downstream.range.dmin", "qos-adaptive/downstream/range/dmin"},
	{"qos-adaptive.downstream.range.dmax", "qos-adaptive/downstream/range/dmax"},
	{"qos-adaptive.upstream.bandwidth-up", "qos-adaptive/upstream/bandwidth-up"},
	{"qos-adaptive.upstream.range.umin", "qos-adaptive/upstream/range/umin"},
	{"qos-adaptive.upstream.range.umax", "qos-adaptive/upstream/range/umax"},
}

// Manager's Adaptive QoS config-input titles can omit the variable name. The
// referenced interface feature template is authoritative for that mapping.
func (c *Client) attachmentQoSMappings(ctx context.Context, templateID string) map[string]string {
	mappings := map[string]string{}
	var device map[string]any
	if err := c.Request(ctx, "GET", deviceTemplatePath+"object/"+url.PathEscape(templateID), nil, &device); err != nil {
		return mappings
	}
	ids := map[string]bool{}
	var collect func([]any)
	collect = func(rows []any) {
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typeName, _ := row["templateType"].(string)
			if typeName == "cisco_vpn_interface" || typeName == "vpn-cedge-interface-cellular" {
				id, _ := row["templateId"].(string)
				if validID.MatchString(id) {
					ids[id] = true
				}
			}
			if children, ok := row["subTemplates"].([]any); ok {
				collect(children)
			}
		}
	}
	if general, ok := device["generalTemplates"].([]any); ok {
		collect(general)
	}
	for id := range ids {
		var feature map[string]any
		if err := c.Request(ctx, "GET", "/dataservice/template/feature/object/"+url.PathEscape(id), nil, &feature); err != nil {
			continue
		}
		definition, ok := feature["templateDefinition"].(map[string]any)
		if !ok {
			continue
		}
		ifNode, ok := definition["if-name"].(map[string]any)
		if !ok {
			continue
		}
		ifName, _ := ifNode["vipValue"].(string)
		if ifName == "" {
			ifName, _ = ifNode["vipVariableName"].(string)
		}
		if ifName == "" {
			continue
		}
		for _, leaf := range attachmentQoSLeaves {
			node, ok := classicInput(definition, leaf.definition).(map[string]any)
			if !ok {
				continue
			}
			name, _ := node["vipVariableName"].(string)
			if name == "" {
				continue
			}
			mappings["/"+ifName+"/interface/"+leaf.suffix] = name
		}
	}
	return mappings
}
