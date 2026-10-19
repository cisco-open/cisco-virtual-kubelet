// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type inventoryDeviceIntent struct {
	wireless         *wirelessDevicePlacement
	role, roleSource string
	reference        configurationDeviceReference
	site             string
	tags             []string
}
type inventoryIntent struct {
	discoveries []discoveryIntent
	tags        []desiredTag
	devices     []inventoryDeviceIntent
}

// All fields are checked before observation. Unsupported provisioning intent
// cannot be silently discarded while tags are reported or applied.
func parseInventoryIntent(raw []byte) (inventoryIntent, error) {
	var result inventoryIntent
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return result, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return result, errors.New("inventory requires a section-specific model")
	}
	section, ok := root["inventory"].(map[string]any)
	if !ok {
		return result, errors.New("invalid inventory section")
	}
	emptyDiscovery := len(section) == 0
	for key, value := range section {
		switch key {
		case "discovery":
			result.discoveries, err = parseDiscoveryModels(value)
			if err != nil {
				return result, err
			}
			emptyDiscovery = len(result.discoveries) == 0
		case "tags":
			result.tags, err = parseTemplateTags(value)
			if err != nil {
				return result, err
			}
		case "devices":
			list, ok := value.([]any)
			if !ok || len(list) > 10000 {
				return result, errors.New("invalid inventory devices")
			}
			names := map[string]bool{}
			for _, entry := range list {
				m, ok := entry.(map[string]any)
				if !ok {
					return result, errors.New("invalid inventory device")
				}
				var d inventoryDeviceIntent
				state := ""
				placementFields := map[string]any{}
				for field, value := range m {
					switch field {
					case "type", "fabric_site", "fabric_roles", "primary_managed_ap_locations", "secondary_managed_ap_locations", "anchor_managed_ap_locations":
						placementFields[field] = value
						continue
					}
					if field == "tags" {
						values, ok := value.([]any)
						if !ok || len(values) > 1000 {
							return result, errors.New("invalid device tag references")
						}
						seen := map[string]bool{}
						for _, value := range values {
							name, ok := value.(string)
							if !ok || !validTemplateTagName(name) || seen[name] {
								return result, errors.New("invalid or duplicate device tag reference")
							}
							seen[name] = true
							d.tags = append(d.tags, name)
						}
						continue
					}
					str, ok := value.(string)
					if !ok || str == "" || strings.TrimSpace(str) != str || strings.ContainsAny(str, "\x00\r\n") {
						return result, errors.New("invalid inventory device field")
					}
					switch field {
					case "name":
						d.reference.Name = str
					case "fqdn_name":
						d.reference.FQDN = str
					case "device_ip":
						d.reference.IP = str
					case "serial_number":
						d.reference.Serial = str
					case "site":
						d.site = str
					case "device_role":
						d.role = str
					case "role_source":
						d.roleSource = str
					case "state":
						state = str
					default:
						return result, errors.New("unsupported inventory device field")
					}
				}
				if (d.role != "" || d.roleSource != "") && (!validDeviceRole(d.role) || !validDeviceRoleSource(d.roleSource)) {
					return result, errors.New("device role and source must both be explicitly resolved")
				}
				if d.reference.Name == "" || len(d.reference.Name) > 256 || names[d.reference.Name] || !validSitePath(d.site) || d.site == "Global" {
					return result, errors.New("inventory requires unique named devices with explicit sites")
				}
				if state == "ASSIGN" {
					if len(placementFields) > 0 {
						return result, errors.New("ASSIGN cannot discard wireless provisioning fields")
					}
				} else {
					placementFields["state"] = state
					placement, err := parseWirelessDevicePlacement(placementFields)
					if err != nil {
						return result, err
					}
					d.wireless = &placement
				}
				if d.reference.IP != "" {
					ip, err := netip.ParseAddr(d.reference.IP)
					if err != nil || ip.Zone() != "" {
						return result, errors.New("device reference requires a literal management IP")
					}
				}
				names[d.reference.Name] = true
				result.devices = append(result.devices, d)
			}
		default:
			return result, errors.New("unsupported inventory field")
		}
	}
	_, tagSelection := section["tags"]
	emptyTags := len(section) == 1 && tagSelection
	if len(result.tags) == 0 && len(result.devices) == 0 && len(result.discoveries) == 0 && !emptyDiscovery && !emptyTags {
		return result, errors.New("empty inventory intent")
	}
	return result, nil
}

func (c *client) reportInventoryIntent(ctx context.Context, intent inventoryIntent, outcomes ...*discoveryOutcomeSummary) ([]configapi.NetworkControllerDriftEntry, error) {
	// Provision/reprovision is an execution request, not just site/tag drift.
	// Until its report includes the whole intent, reject before any API read.
	for _, d := range intent.devices {
		if d.wireless != nil {
			return nil, errors.New("wireless provisioning report is not yet implemented")
		}
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	if len(intent.discoveries) > 0 {
		var summary *discoveryOutcomeSummary
		if len(outcomes) > 0 {
			summary = outcomes[0]
		}
		d, err := (&discoveryExecutor{client: c, desired: intent.discoveries}).reportWithOutcomes(ctx, summary)
		if err != nil {
			return nil, err
		}
		drift = append(drift, d...)
	}
	if len(intent.tags) > 0 {
		tags, err := c.reportTags(ctx, intent.tags)
		if err != nil {
			return nil, err
		}
		for i := range tags {
			tags[i].Section = "inventory"
		}
		drift = append(drift, tags...)
	}
	if len(intent.devices) == 0 {
		return drift, nil
	}
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	assigned := map[string]map[string]bool{}
	seen := map[string]bool{}
	memberships := &deviceMembershipExecutor{client: c, declared: intent.tags}
	for _, desired := range intent.devices {
		device, err := resolveConfigurationDevice(desired.reference, devices)
		if err != nil {
			return nil, err
		}
		if seen[device.ID] {
			return nil, errors.New("multiple model entries select one inventory device")
		}
		seen[device.ID] = true
		if desired.role != "" {
			if !validDeviceRole(device.Role) || !validDeviceRoleSource(device.RoleSource) {
				return nil, errInvalidResponse
			}
			if device.Role != desired.role || device.RoleSource != desired.roleSource {
				drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "inventory", Resource: desired.reference.Name, Path: "/device_role", Change: configapi.NetworkControllerDriftChangeUpdate})
			}
		}
		site, exists := byPath[desired.site]
		if !exists {
			return nil, errors.New("inventory target site is not present")
		}
		if _, read := assigned[site.ID]; !read {
			assigned[site.ID], err = c.assignedDevices(ctx, site.ID)
			if err != nil {
				return nil, err
			}
		}
		if !assigned[site.ID][device.ID] {
			drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "inventory", Resource: desired.reference.Name, Path: "/site_assignment", Change: configapi.NetworkControllerDriftChangeUpdate})
		}
		if len(desired.tags) > 0 {
			memberships.devices = append(memberships.devices, desiredDeviceMembership{device: desired.reference, tags: desired.tags})
		}
	}
	if memberships.count() > 0 {
		observed, err := memberships.observe(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range observed {
			if !m.present {
				drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "inventory", Resource: m.resource, Path: "/tag_membership", Change: configapi.NetworkControllerDriftChangeAdd})
			}
		}
	}
	return drift, nil
}
