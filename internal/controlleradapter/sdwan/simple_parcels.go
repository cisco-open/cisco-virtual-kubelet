// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"strings"
)

type ParcelMetadata struct {
	Name        *string `json:"name,omitempty"`
	Description string  `json:"description,omitempty"`
}
type CLIConfig struct {
	ParcelMetadata
	Configuration string `json:"cli_configuration"`
}
type FlexiblePortSpeed struct {
	ParcelMetadata
	PortType *string `json:"port_type,omitempty"`
	Variable *string `json:"port_type_variable,omitempty"`
}
type MRF struct {
	ParcelMetadata
	Migration         *string `json:"migration_to_mrf,omitempty"`
	Community         *int64  `json:"migration_bgp_community,omitempty"`
	Role              *string `json:"role,omitempty"`
	RoleVariable      *string `json:"role_variable,omitempty"`
	SecondaryRegion   *int64  `json:"secondary_region_id,omitempty"`
	SecondaryVariable *string `json:"secondary_region_id_variable,omitempty"`
}
type PerformanceMonitoring struct {
	ParcelMetadata
	AppGroups         *[]string `json:"app_perf_monitor_app_groups,omitempty"`
	AppEnabled        *bool     `json:"app_perf_monitor_enabled,omitempty"`
	EventEnabled      *bool     `json:"event_driven_config_enabled,omitempty"`
	Events            *[]string `json:"event_driven_events,omitempty"`
	MonitoringEnabled *bool     `json:"monitoring_config_enabled,omitempty"`
	Interval          *string   `json:"monitoring_config_interval,omitempty"`
}

func option[T any](value *T, variable *string, defaultValue any, hasDefault bool) map[string]any {
	if variable != nil {
		return map[string]any{"optionType": "variable", "value": "{{" + *variable + "}}"}
	}
	if value != nil {
		return map[string]any{"optionType": "global", "value": *value}
	}
	out := map[string]any{"optionType": "default"}
	if hasDefault {
		out["value"] = defaultValue
	}
	return out
}
func simpleParcels(p FeatureProfile, collection, key, managerVersion string) ([]Resource, error) {
	out := []Resource{}
	add := func(field, path string, meta ParcelMetadata, data map[string]any) error {
		name := field
		if meta.Name != nil {
			name = *meta.Name
		}
		if name == "" || len(name) > 128 {
			return errors.New("invalid parcel name")
		}
		out = append(out, Resource{Key: key + "/" + field, Name: name, Section: "feature_profiles", Kind: "parcel", Type: collection + "_" + field, Collection: "/dataservice/v1/feature-profile/sdwan/" + collection, ParentName: p.Name, ParcelPath: path, Body: map[string]any{"name": name, "description": meta.Description, "data": data}})
		return nil
	}
	for _, feature := range []struct {
		public, family, kind, path string
		input                      map[string]any
	}{
		{"ca_certificate", "system", "ca_certificate", "ca-cert", p.CACertificate},
		{"cisco", "sse", "sse_cisco", "cisco", p.CiscoSSE},
		{"zscaler", "sse", "sse_zscaler", "zscaler", p.ZscalerSSE},
	} {
		if feature.input == nil {
			continue
		}
		if collection != feature.family {
			return nil, errors.New("feature does not belong to this profile family")
		}
		meta, data, err := scalarParcelData(feature.kind, feature.input)
		if err != nil {
			return nil, err
		}
		if (feature.kind == "sse_cisco" || feature.kind == "sse_zscaler") && meta.Name == nil {
			return nil, errors.New("SSE feature requires a name")
		}
		if err := add(feature.public, feature.path, meta, data); err != nil {
			return nil, err
		}
	}
	if (p.IPv4DeviceAccess != nil || p.IPv6DeviceAccess != nil) && collection != "system" {
		return nil, errors.New("device access policies require a system profile")
	}
	mesh, err := compileMeshPolicies(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, mesh...)
	hubSpoke, err := compileHubSpokePolicies(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, hubSpoke...)
	ipv4ACLs, err := compileIPv4ACLs(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, ipv4ACLs...)
	ipv6ACLs, err := compileIPv6ACLs(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, ipv6ACLs...)
	routePolicies, err := compileRoutePolicies(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, routePolicies...)
	managementVPN, err := compileManagementVPN(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, managementVPN...)
	wanVPN, err := compileWANVPN(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, wanVPN...)
	managementInterfaces, err := compileManagementInterfaces(p, collection, key, managerVersion)
	if err != nil {
		return nil, err
	}
	out = append(out, managementInterfaces...)
	serviceLANVPNs, err := compileServiceLANVPNs(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, serviceLANVPNs...)
	dualRouterHA, err := compileDualRouterHA(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, dualRouterHA...)
	associations, err := compileVPNRoutingAssociations(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, associations...)
	svis, err := compileServiceSVIs(p, collection, key)
	if err != nil {
		return nil, err
	}
	out = append(out, svis...)
	for _, item := range []struct {
		ip    string
		input map[string]any
	}{{"ipv4", p.IPv4DeviceAccess}, {"ipv6", p.IPv6DeviceAccess}} {
		if item.input == nil {
			continue
		}
		meta, data, err := deviceAccessData(item.input, item.ip)
		if err != nil {
			return nil, err
		}
		name := item.ip + "_device_access_policy"
		if meta.Name != nil {
			name = *meta.Name
		}
		if err := add(item.ip+"_device_access_policy", item.ip+"-device-access-policy", meta, data); err != nil {
			return nil, err
		}
		out[len(out)-1].Name = name
		out[len(out)-1].Type = "system_" + item.ip + "_device_access"
		out[len(out)-1].ParcelRefs = deviceAccessReferences(item.input, item.ip)
	}
	if p.OSPF != nil && collection != "service" && collection != "transport" {
		return nil, errors.New("OSPF requires a service or transport profile")
	}
	if (p.OSPFv3IPv4 != nil || p.OSPFv3IPv6 != nil) && collection != "service" {
		return nil, errors.New("OSPFv3 requires a service profile")
	}
	if (p.EIGRP != nil || p.Switchports != nil || p.Multicasts != nil) && collection != "service" {
		return nil, errors.New("EIGRP, switchport and multicast require a service profile")
	}
	var registry struct{ Parcels map[string]scalarDefinition }
	if len(p.OSPF)+len(p.OSPFv3IPv4)+len(p.OSPFv3IPv6)+len(p.EIGRP)+len(p.Switchports)+len(p.Multicasts) > 0 {
		if json.Unmarshal(scalarDefinitions, &registry) != nil {
			return nil, ErrResponse
		}
	}
	for _, routing := range []struct {
		public, kind, path string
		inputs             []map[string]any
	}{
		{"ospf_features", collection + "_routing_ospf", "routing/ospf", p.OSPF},
		{"ospfv3_ipv4_features", "service_routing_ospfv3_ipv4", "routing/ospfv3/ipv4", p.OSPFv3IPv4},
		{"ospfv3_ipv6_features", "service_routing_ospfv3_ipv6", "routing/ospfv3/ipv6", p.OSPFv3IPv6},
		{"eigrp_features", "service_routing_eigrp", "routing/eigrp", p.EIGRP},
		{"switchport_features", "service_switchport", "switchport", p.Switchports},
		{"multicast_features", "service_multicast", "routing/multicast", p.Multicasts},
	} {
		for _, input := range routing.inputs {
			meta, data, err := scalarParcelData(routing.kind, input)
			if err != nil {
				return nil, err
			}
			if meta.Name == nil || *meta.Name == "" {
				return nil, errors.New("routing feature requires a name")
			}
			if err = add(routing.public+"/"+*meta.Name, routing.path, meta, data); err != nil {
				return nil, err
			}
			out[len(out)-1].Type = routing.kind
			out[len(out)-1].ParcelRefs = collectScalarReferences(registry.Parcels[routing.kind], input, []string{"data"})
		}
	}
	if (p.DHCPServers != nil || p.ObjectTrackers != nil || p.ObjectTrackerGroups != nil) && collection != "service" {
		return nil, errors.New("service trackers and DHCP servers require service profile")
	}
	if (p.IPv4Trackers != nil || p.IPv4TrackerGroups != nil) && collection != "service" && collection != "transport" {
		return nil, errors.New("IPv4 trackers require service or transport profile")
	}
	if (p.IPv6Trackers != nil || p.IPv6TrackerGroups != nil) && collection != "transport" {
		return nil, errors.New("IPv6 trackers require transport profile")
	}
	for _, family := range []struct {
		public, kind, path string
		inputs             []map[string]any
	}{
		{"ipv4_trackers", collection + "_tracker", "tracker", p.IPv4Trackers},
		{"ipv6_trackers", "transport_ipv6_tracker", "ipv6-tracker", p.IPv6Trackers},
		{"object_trackers", "service_object_tracker", "objecttracker", p.ObjectTrackers},
		{"dhcp_servers", "service_dhcp_server", "dhcp-server", p.DHCPServers},
	} {
		seen := map[string]bool{}
		for _, input := range family.inputs {
			meta, data, e := scalarParcelData(family.kind, input)
			if e != nil {
				return nil, e
			}
			if meta.Name == nil || *meta.Name == "" || seen[*meta.Name] {
				return nil, errors.New("service parcels require unique names")
			}
			seen[*meta.Name] = true
			if e = add(family.public+"/"+*meta.Name, family.path, meta, data); e != nil {
				return nil, e
			}
		}
	}
	trackerGroups, e := compileTrackerGroups(p, collection, key)
	if e != nil {
		return nil, e
	}
	out = append(out, trackerGroups...)
	if (p.CellularControllers != nil || p.CellularProfiles != nil) && collection != "transport" {
		return nil, errors.New("cellular features require transport profile")
	}
	for _, family := range []struct {
		public, kind, path string
		inputs             []map[string]any
	}{
		{"cellular_controllers", "transport_cellular_controller", "cellular-controller", p.CellularControllers},
		{"cellular_profiles", "transport_cellular_profile", "cellular-profile", p.CellularProfiles},
	} {
		seen := map[string]bool{}
		for _, input := range family.inputs {
			meta, data, e := scalarParcelData(family.kind, input)
			if e != nil {
				return nil, e
			}
			if meta.Name == nil || *meta.Name == "" || seen[*meta.Name] {
				return nil, errors.New("cellular features require unique names")
			}
			seen[*meta.Name] = true
			if e = add(family.public+"/"+*meta.Name, family.path, meta, data); e != nil {
				return nil, e
			}
		}
	}
	if p.GPS != nil && collection != "transport" {
		return nil, errors.New("GPS requires transport profile")
	}
	seenGPS := map[string]bool{}
	for _, input := range p.GPS {
		meta, data, e := scalarParcelData("transport_gps", input)
		if e != nil {
			return nil, e
		}
		if meta.Name == nil || *meta.Name == "" || seenGPS[*meta.Name] {
			return nil, errors.New("GPS requires unique names")
		}
		seenGPS[*meta.Name] = true
		if e = add("gps/"+*meta.Name, "gps", meta, data); e != nil {
			return nil, e
		}
	}
	if p.TrustSec != nil {
		return nil, errors.New("TrustSec is not qualified: the pinned provider omits Manager 20.18 sxpNodeId")
	}
	if (p.ThousandEyes != nil || p.UCSE != nil) && collection != "other" {
		return nil, errors.New("ThousandEyes and UCSE require other profile")
	}
	for kind, input := range map[string]map[string]any{"thousandeyes": p.ThousandEyes, "ucse": p.UCSE} {
		if input == nil {
			continue
		}
		meta, data, e := scalarParcelData("other_"+kind, input)
		if e != nil {
			return nil, e
		}
		if e = add(kind, kind, meta, data); e != nil {
			return nil, e
		}
	}
	if p.Config != nil {
		if collection != "cli" {
			return nil, errors.New("config requires CLI profile")
		}
		if p.Config.Configuration == "" {
			return nil, errors.New("cli_configuration is required")
		}
		if e := add("config", "config", p.Config.ParcelMetadata, map[string]any{"config": p.Config.Configuration}); e != nil {
			return nil, e
		}
	}
	if (p.AAA != nil || p.SNMP != nil || p.Security != nil || p.NTP != nil || p.Logging != nil || p.Basic != nil || p.Global != nil || p.OMP != nil || p.BFD != nil || p.FlexiblePortSpeed != nil || p.MRF != nil || p.PerformanceMonitoring != nil) && collection != "system" {
		return nil, errors.New("system feature requires system profile")
	}
	for kind, input := range map[string]map[string]any{"aaa": p.AAA, "snmp": p.SNMP, "security": p.Security, "ntp": p.NTP, "logging": p.Logging, "basic": p.Basic, "global": p.Global, "omp": p.OMP} {
		if input != nil {
			meta, data, e := scalarParcelData(kind, input)
			if e != nil {
				return nil, e
			}
			if e = add(kind, kind, meta, data); e != nil {
				return nil, e
			}
		}
	}
	if b := p.BFD; b != nil {
		data, e := bfdBody(b)
		if e != nil {
			return nil, e
		}
		if e = add("bfd", "bfd", b.ParcelMetadata, data); e != nil {
			return nil, e
		}
	}
	if f := p.FlexiblePortSpeed; f != nil {
		if e := add("flexible_port_speed", "flexible-port-speed", f.ParcelMetadata, map[string]any{"portType": option(f.PortType, f.Variable, "12 ports of 1/10GE + 3 ports 40GE", true)}); e != nil {
			return nil, e
		}
	}
	if m := p.MRF; m != nil {
		data := map[string]any{"secondaryRegion": option(m.SecondaryRegion, m.SecondaryVariable, nil, false), "role": option(m.Role, m.RoleVariable, nil, false), "enableMrfMigration": option(m.Migration, nil, nil, false), "migrationBgpCommunity": option(m.Community, nil, nil, false), "enableManagementRegion": map[string]any{"optionType": "default", "value": false}, "managementRegion": map[string]any{"vrfId": map[string]any{"optionType": "default"}, "gatewayPreference": map[string]any{"optionType": "default"}, "managementGateway": map[string]any{"optionType": "default", "value": false}}}
		if e := add("mrf", "mrf", m.ParcelMetadata, data); e != nil {
			return nil, e
		}
	}
	if m := p.PerformanceMonitoring; m != nil {
		app := map[string]any{"enabled": option(m.AppEnabled, nil, false, true)}
		if m.AppGroups != nil {
			app["policyFilters"] = map[string]any{"appGroups": option(m.AppGroups, nil, nil, false)}
		}
		monitoring := map[string]any{"enabled": option(m.MonitoringEnabled, nil, false, true)}
		if m.Interval != nil {
			monitoring["interval"] = option(m.Interval, nil, nil, false)
		}
		event := map[string]any{"enabled": option(m.EventEnabled, nil, false, true)}
		if m.Events != nil {
			events := []string{}
			seen := map[string]bool{}
			for _, v := range *m.Events {
				v = strings.ToUpper(v)
				if !seen[v] {
					events = append(events, v)
					seen[v] = true
				}
			}
			if len(events) > 0 {
				event["events"] = map[string]any{"optionType": "global", "value": events}
			}
		}
		if e := add("performance_monitoring", "perfmonitor", m.ParcelMetadata, map[string]any{"appPerfMonitorConfig": app, "umtsConfig": map[string]any{"monitoringConfig": monitoring, "eventDrivenConfig": event}}); e != nil {
			return nil, e
		}
	}
	return out, nil
}

type BFD struct {
	ParcelMetadata
	DefaultDSCP        *int64     `json:"default_dscp,omitempty"`
	DSCPVariable       *string    `json:"dscp_variable,omitempty"`
	Multiplier         *int64     `json:"multiplier,omitempty"`
	MultiplierVariable *string    `json:"multiplier_variable,omitempty"`
	PollInterval       *int64     `json:"poll_interval,omitempty"`
	PollVariable       *string    `json:"poll_interval_variable,omitempty"`
	Colors             []BFDColor `json:"colors,omitempty"`
}
type BFDColor struct {
	Color              *string `json:"color,omitempty"`
	ColorVariable      *string `json:"color_variable,omitempty"`
	DefaultDSCP        *int64  `json:"default_dscp,omitempty"`
	DSCPVariable       *string `json:"dscp_variable,omitempty"`
	HelloInterval      *int64  `json:"hello_interval,omitempty"`
	HelloVariable      *string `json:"hello_interval_variable,omitempty"`
	Multiplier         *int64  `json:"multiplier,omitempty"`
	MultiplierVariable *string `json:"multiplier_variable,omitempty"`
	PMTU               *bool   `json:"path_mtu_discovery,omitempty"`
	PMTUVariable       *string `json:"path_mtu_discovery_variable,omitempty"`
}

func bfdBody(b *BFD) (map[string]any, error) {
	colors := []any{}
	for _, c := range b.Colors {
		if c.Color == nil && c.ColorVariable == nil {
			return nil, errors.New("BFD color requires color or variable")
		}
		colors = append(colors, map[string]any{"color": option(c.Color, c.ColorVariable, nil, false), "dscp": option(c.DefaultDSCP, c.DSCPVariable, 48, true), "helloInterval": option(c.HelloInterval, c.HelloVariable, 1000, true), "multiplier": option(c.Multiplier, c.MultiplierVariable, 7, true), "pmtuDiscovery": option(c.PMTU, c.PMTUVariable, true, true)})
	}
	return map[string]any{"multiplier": option(b.Multiplier, b.MultiplierVariable, 6, true), "pollInterval": option(b.PollInterval, b.PollVariable, 600000, true), "defaultDscp": option(b.DefaultDSCP, b.DSCPVariable, 48, true), "colors": colors}, nil
}
