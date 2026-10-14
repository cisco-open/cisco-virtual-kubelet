// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
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
func simpleParcels(p FeatureProfile, collection, key string) ([]Resource, error) {
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
	if (p.NTP != nil || p.Logging != nil || p.Basic != nil || p.Global != nil || p.OMP != nil || p.BFD != nil || p.FlexiblePortSpeed != nil || p.MRF != nil || p.PerformanceMonitoring != nil) && collection != "system" {
		return nil, errors.New("system feature requires system profile")
	}
	for kind, input := range map[string]map[string]any{"ntp": p.NTP, "logging": p.Logging, "basic": p.Basic, "global": p.Global, "omp": p.OMP} {
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
