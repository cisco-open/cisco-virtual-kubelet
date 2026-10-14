// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
)

type PolicyListReference struct {
	Section, Name string
	Path          []string
}

type CentralizedPolicies struct {
	FeaturePolicies []CentralizedFeaturePolicy `json:"feature_policies,omitempty"`
	Definitions     struct {
		ControlPolicy struct {
			CustomControlTopology []CentralizedCustomControl `json:"custom_control_topology,omitempty"`
			VPNMembership         []CentralizedVPNMembership `json:"vpn_membership,omitempty"`
			MeshTopology          []CentralizedMeshTopology  `json:"mesh_topology,omitempty"`
			HubAndSpokeTopology   []CentralizedHubAndSpoke   `json:"hub_and_spoke_topology,omitempty"`
		} `json:"control_policy"`
		DataPolicy struct {
			Cflowd                  []CentralizedCflowd                  `json:"cflowd,omitempty"`
			TrafficData             []CentralizedTrafficData             `json:"traffic_data,omitempty"`
			ApplicationAwareRouting []CentralizedApplicationAwareRouting `json:"application_aware_routing,omitempty"`
		} `json:"data_policy"`
	} `json:"definitions"`
}
type CentralizedCustomControl struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	DefaultActionType string `json:"default_action_type,omitempty"`
	Sequences         []struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		Type          string `json:"type,omitempty"`
		IPType        string `json:"ip_type,omitempty"`
		BaseAction    string `json:"base_action,omitempty"`
		MatchCriteria struct {
			OMPTag *int `json:"omp_tag,omitempty"`
		} `json:"match_criterias"`
	} `json:"sequences"`
}
type CentralizedApplicationAwareRouting struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Sequences   []struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		IPType        string `json:"ip_type,omitempty"`
		MatchCriteria struct {
			SourceDataPrefix string `json:"source_data_prefix,omitempty"`
		} `json:"match_criterias"`
		Actions struct {
			Log *bool `json:"log,omitempty"`
		} `json:"actions"`
	} `json:"sequences"`
}
type CentralizedTrafficData struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	DefaultActionType string `json:"default_action_type,omitempty"`
	Sequences         []struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		Type          string `json:"type,omitempty"`
		IPType        string `json:"ip_type,omitempty"`
		BaseAction    string `json:"base_action,omitempty"`
		MatchCriteria struct {
			SourceDataPrefix string `json:"source_data_prefix,omitempty"`
		} `json:"match_criterias"`
	} `json:"sequences"`
}
type CentralizedFeaturePolicy struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	MeshTopology []struct {
		PolicyDefinition string `json:"policy_definition"`
	} `json:"mesh_topology,omitempty"`
	HubAndSpokeTopology []struct {
		PolicyDefinition string `json:"policy_definition"`
	} `json:"hub_and_spoke_topology,omitempty"`
	VPNMembership []struct {
		PolicyDefinition string `json:"policy_definition"`
	} `json:"vpn_membership,omitempty"`
	Cflowd []struct {
		PolicyDefinition string   `json:"policy_definition"`
		SiteLists        []string `json:"site_lists"`
	} `json:"cflowd,omitempty"`
}
type CentralizedCflowd struct {
	Name                string `json:"name"`
	Description         string `json:"description"`
	ActiveFlowTimeout   *int   `json:"active_flow_timeout,omitempty"`
	InactiveFlowTimeout *int   `json:"inactive_flow_timeout,omitempty"`
	SamplingInterval    *int   `json:"sampling_interval,omitempty"`
	FlowRefresh         *int   `json:"flow_refresh,omitempty"`
	Protocol            string `json:"protocol,omitempty"`
	TOS                 *bool  `json:"tos,omitempty"`
	RemarkedDSCP        *bool  `json:"remarked_dscp,omitempty"`
	Collectors          []struct {
		VPN             int    `json:"vpn"`
		IPAddress       string `json:"ip_address"`
		Port            int    `json:"port"`
		Transport       string `json:"transport"`
		SourceInterface string `json:"source_interface"`
		ExportSpreading string `json:"export_spreading,omitempty"`
	} `json:"collectors"`
}
type CentralizedVPNMembership struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Groups      []struct {
		SiteList string   `json:"site_list"`
		VPNLists []string `json:"vpn_lists"`
	} `json:"groups"`
}
type CentralizedMeshTopology struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	VPNList     string `json:"vpn_list"`
	MeshGroups  []struct {
		Name      string   `json:"name"`
		SiteLists []string `json:"site_lists"`
	} `json:"mesh_groups"`
}
type CentralizedHubAndSpoke struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	VPNList          string `json:"vpn_list"`
	HubAndSpokeSites []struct {
		Name            string `json:"name"`
		EqualPreference *bool  `json:"equal_preference,omitempty"`
		AdvertiseTLOC   *bool  `json:"advertise_tloc,omitempty"`
		TLOCList        string `json:"tloc_list,omitempty"`
		Spokes          []struct {
			SiteList string `json:"site_list"`
			Hubs     []struct {
				SiteList        string   `json:"site_list"`
				IPv4PrefixLists []string `json:"ipv4_prefix_lists,omitempty"`
				IPv6PrefixLists []string `json:"ipv6_prefix_lists,omitempty"`
			} `json:"hubs"`
		} `json:"spokes"`
	} `json:"hub_and_spoke_sites"`
}

func compileCentralizedPolicies(input *CentralizedPolicies) ([]Resource, error) {
	if input == nil {
		return nil, nil
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, p := range input.Definitions.ControlPolicy.CustomControlTopology {
		if p.Name == "" || p.Description == "" || seen["control/"+p.Name] {
			return nil, errors.New("invalid or duplicate custom-control definition")
		}
		seen["control/"+p.Name] = true
		defaultAction := p.DefaultActionType
		if defaultAction == "" {
			defaultAction = "reject"
		}
		if defaultAction != "accept" && defaultAction != "reject" {
			return nil, errors.New("invalid custom-control default action")
		}
		sequences, ids := []any{}, map[int]bool{}
		for _, s := range p.Sequences {
			if s.ID < 1 || s.ID > 65535 || ids[s.ID] || s.Name == "" {
				return nil, errors.New("invalid custom-control sequence")
			}
			ids[s.ID] = true
			typ := s.Type
			if typ == "" {
				typ = "route"
			}
			if typ != "route" && typ != "tloc" {
				return nil, errors.New("invalid custom-control sequence type")
			}
			ipType := s.IPType
			if ipType == "" {
				ipType = "ipv4"
			}
			if ipType != "ipv4" && ipType != "ipv6" && ipType != "all" {
				return nil, errors.New("invalid custom-control IP type")
			}
			base := s.BaseAction
			if base == "" {
				base = "accept"
			}
			if base != "accept" && base != "reject" {
				return nil, errors.New("invalid custom-control base action")
			}
			matches := []any{}
			if s.MatchCriteria.OMPTag != nil {
				if *s.MatchCriteria.OMPTag < 0 {
					return nil, errors.New("invalid custom-control OMP tag")
				}
				matches = append(matches, map[string]any{"field": "ompTag", "value": strconv.Itoa(*s.MatchCriteria.OMPTag)})
			}
			sequences = append(sequences, map[string]any{"sequenceId": s.ID, "sequenceName": s.Name, "sequenceType": typ, "sequenceIpType": ipType, "baseAction": base, "match": map[string]any{"entries": matches}, "actions": []any{}})
		}
		out = append(out, Resource{Key: "centralized_policies/definitions/control_policy/custom_control_topology/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "custom_control_topology_policy_definition", Collection: "/dataservice/template/policy/definition/control/", Body: map[string]any{"type": "control", "name": p.Name, "description": p.Description, "defaultAction": map[string]any{"type": defaultAction}, "sequences": sequences}})
	}
	for _, p := range input.Definitions.ControlPolicy.VPNMembership {
		if p.Name == "" || p.Description == "" || seen["vpnMembershipGroup/"+p.Name] {
			return nil, errors.New("invalid or duplicate VPN membership definition")
		}
		seen["vpnMembershipGroup/"+p.Name] = true
		sites := []any{}
		refs := []PolicyListReference{}
		for i, group := range p.Groups {
			if group.SiteList == "" || len(group.VPNLists) == 0 {
				return nil, errors.New("VPN membership group requires site and VPN lists")
			}
			vpns := []any{}
			for j, name := range group.VPNLists {
				if name == "" {
					return nil, errors.New("empty VPN list reference")
				}
				vpns = append(vpns, "")
				refs = append(refs, PolicyListReference{Section: "vpn_lists", Name: name, Path: []string{"definition", "sites", strconv.Itoa(i), "vpnList", strconv.Itoa(j)}})
			}
			sites = append(sites, map[string]any{"siteList": "", "vpnList": vpns})
			refs = append(refs, PolicyListReference{Section: "site_lists", Name: group.SiteList, Path: []string{"definition", "sites", strconv.Itoa(i), "siteList"}})
		}
		out = append(out, Resource{Key: "centralized_policies/definitions/control_policy/vpn_membership/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "vpn_membership_policy_definition", Collection: "/dataservice/template/policy/definition/vpnmembershipgroup/", PolicyListRefs: refs, Body: map[string]any{"type": "vpnMembershipGroup", "name": p.Name, "description": p.Description, "definition": map[string]any{"sites": sites}}})
	}
	for _, p := range input.Definitions.ControlPolicy.MeshTopology {
		if p.Name == "" || p.Description == "" || p.VPNList == "" || seen["mesh/"+p.Name] {
			return nil, errors.New("invalid or duplicate mesh definition")
		}
		seen["mesh/"+p.Name] = true
		regions := []any{}
		refs := []PolicyListReference{{Section: "vpn_lists", Name: p.VPNList, Path: []string{"definition", "vpnList"}}}
		for i, group := range p.MeshGroups {
			if group.Name == "" || len(group.SiteLists) == 0 {
				return nil, errors.New("mesh group requires name and site lists")
			}
			sites := []any{}
			for j, name := range group.SiteLists {
				if name == "" {
					return nil, errors.New("empty mesh site-list reference")
				}
				sites = append(sites, "")
				refs = append(refs, PolicyListReference{Section: "site_lists", Name: name, Path: []string{"definition", "regions", strconv.Itoa(i), "siteLists", strconv.Itoa(j)}})
			}
			regions = append(regions, map[string]any{"name": group.Name, "siteLists": sites})
		}
		out = append(out, Resource{Key: "centralized_policies/definitions/control_policy/mesh_topology/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "mesh_topology_policy_definition", Collection: "/dataservice/template/policy/definition/mesh/", PolicyListRefs: refs, Body: map[string]any{"type": "mesh", "name": p.Name, "description": p.Description, "definition": map[string]any{"vpnList": "", "regions": regions}}})
	}
	for _, p := range input.Definitions.ControlPolicy.HubAndSpokeTopology {
		if p.Name == "" || p.Description == "" || p.VPNList == "" || seen["hubAndSpoke/"+p.Name] {
			return nil, errors.New("invalid or duplicate hub-and-spoke definition")
		}
		seen["hubAndSpoke/"+p.Name] = true
		refs := []PolicyListReference{{Section: "vpn_lists", Name: p.VPNList, Path: []string{"definition", "vpnList"}}}
		topologies := []any{}
		for ti, topology := range p.HubAndSpokeSites {
			if topology.Name == "" || len(topology.Spokes) == 0 {
				return nil, errors.New("hub-and-spoke topology requires name and spokes")
			}
			row := map[string]any{"name": topology.Name, "spokes": []any{}}
			if topology.EqualPreference != nil {
				row["equalPreference"] = *topology.EqualPreference
			}
			if topology.AdvertiseTLOC != nil {
				row["advertiseTloc"] = *topology.AdvertiseTLOC
			}
			if topology.TLOCList != "" {
				row["tlocList"] = ""
				refs = append(refs, PolicyListReference{Section: "tloc_lists", Name: topology.TLOCList, Path: []string{"definition", "subDefinitions", strconv.Itoa(ti), "tlocList"}})
			}
			spokes := []any{}
			for si, spoke := range topology.Spokes {
				if spoke.SiteList == "" || len(spoke.Hubs) == 0 {
					return nil, errors.New("spoke requires site list and hubs")
				}
				hubs := []any{}
				for hi, hub := range spoke.Hubs {
					if hub.SiteList == "" {
						return nil, errors.New("hub requires site list")
					}
					entry := map[string]any{"siteList": ""}
					base := []string{"definition", "subDefinitions", strconv.Itoa(ti), "spokes", strconv.Itoa(si), "hubs", strconv.Itoa(hi)}
					refs = append(refs, PolicyListReference{Section: "site_lists", Name: hub.SiteList, Path: append(append([]string{}, base...), "siteList")})
					for _, family := range []struct {
						names          []string
						section, field string
					}{{hub.IPv4PrefixLists, "ipv4_prefix_lists", "prefixLists"}, {hub.IPv6PrefixLists, "ipv6_prefix_lists", "ipv6PrefixLists"}} {
						if len(family.names) == 0 {
							continue
						}
						values := []any{}
						for pi, name := range family.names {
							if name == "" {
								return nil, errors.New("empty hub prefix-list reference")
							}
							values = append(values, "")
							refs = append(refs, PolicyListReference{Section: family.section, Name: name, Path: append(append([]string{}, base...), family.field, strconv.Itoa(pi))})
						}
						entry[family.field] = values
					}
					hubs = append(hubs, entry)
				}
				spokes = append(spokes, map[string]any{"siteList": "", "hubs": hubs})
				refs = append(refs, PolicyListReference{Section: "site_lists", Name: spoke.SiteList, Path: []string{"definition", "subDefinitions", strconv.Itoa(ti), "spokes", strconv.Itoa(si), "siteList"}})
			}
			row["spokes"] = spokes
			topologies = append(topologies, row)
		}
		out = append(out, Resource{Key: "centralized_policies/definitions/control_policy/hub_and_spoke_topology/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "hub_and_spoke_topology_policy_definition", Collection: "/dataservice/template/policy/definition/hubandspoke/", PolicyListRefs: refs, Body: map[string]any{"type": "hubAndSpoke", "name": p.Name, "description": p.Description, "definition": map[string]any{"vpnList": "", "subDefinitions": topologies}}})
	}
	for _, p := range input.Definitions.DataPolicy.Cflowd {
		if p.Name == "" || p.Description == "" || seen["cflowd/"+p.Name] {
			return nil, errors.New("invalid or duplicate cflowd definition")
		}
		seen["cflowd/"+p.Name] = true
		definition := map[string]any{}
		for _, field := range []struct {
			key       string
			value     *int
			low, high int
		}{
			{"flowActiveTimeout", p.ActiveFlowTimeout, 30, 3600}, {"flowInactiveTimeout", p.InactiveFlowTimeout, 1, 3600},
			{"flowSamplingInterval", p.SamplingInterval, 1, 65536}, {"templateRefresh", p.FlowRefresh, 60, 86400},
		} {
			if field.value != nil {
				if *field.value < field.low || *field.value > field.high {
					return nil, fmt.Errorf("invalid cflowd %s", field.key)
				}
				definition[field.key] = *field.value
			}
		}
		protocol := p.Protocol
		if protocol == "" {
			protocol = "ipv4"
		}
		if protocol != "ipv4" && protocol != "ipv6" && protocol != "both" {
			return nil, errors.New("invalid cflowd protocol")
		}
		definition["protocol"] = protocol
		tos, dscp := false, false
		if p.TOS != nil {
			tos = *p.TOS
		}
		if p.RemarkedDSCP != nil {
			dscp = *p.RemarkedDSCP
		}
		definition["customizedIpv4RecordFields"] = map[string]any{"collectTos": tos, "collectDscpOutput": dscp}
		collectors := []any{}
		for _, c := range p.Collectors {
			if c.VPN < 1 || c.VPN > 65535 || c.Port < 1024 || c.Port > 65535 || c.SourceInterface == "" || (c.Transport != "transport_tcp" && c.Transport != "transport_udp") {
				return nil, errors.New("invalid cflowd collector")
			}
			if ip, e := netip.ParseAddr(c.IPAddress); e != nil || !ip.IsValid() {
				return nil, errors.New("invalid cflowd collector IP")
			}
			row := map[string]any{"vpn": strconv.Itoa(c.VPN), "address": c.IPAddress, "port": c.Port, "transport": c.Transport, "sourceInterface": c.SourceInterface}
			if c.ExportSpreading != "" {
				if c.ExportSpreading != "enable" && c.ExportSpreading != "disable" {
					return nil, errors.New("invalid cflowd export spreading")
				}
				row["exportSpread"] = c.ExportSpreading
			}
			collectors = append(collectors, row)
		}
		definition["collectors"] = collectors
		out = append(out, Resource{Key: "centralized_policies/definitions/data_policy/cflowd/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "cflowd_policy_definition", Collection: "/dataservice/template/policy/definition/cflowd/", Body: map[string]any{"type": "cflowd", "name": p.Name, "description": p.Description, "definition": definition}})
	}
	for _, p := range input.Definitions.DataPolicy.TrafficData {
		if p.Name == "" || p.Description == "" || seen["data/"+p.Name] {
			return nil, errors.New("invalid or duplicate traffic data definition")
		}
		seen["data/"+p.Name] = true
		defaultAction := p.DefaultActionType
		if defaultAction == "" {
			defaultAction = "drop"
		}
		if defaultAction != "accept" && defaultAction != "drop" {
			return nil, errors.New("invalid traffic data default action")
		}
		sequences := []any{}
		ids := map[int]bool{}
		for _, s := range p.Sequences {
			if s.ID < 1 || s.ID > 65535 || ids[s.ID] || s.Name == "" {
				return nil, errors.New("invalid traffic data sequence")
			}
			ids[s.ID] = true
			typ := s.Type
			if typ == "" {
				typ = "custom"
			}
			apiTypes := map[string]string{"custom": "data", "application_firewall": "applicationFirewall", "qos": "qos", "service_chaining": "serviceChaining", "traffic_engineering": "trafficEngineering"}
			mapped := apiTypes[typ]
			if mapped == "" {
				return nil, errors.New("invalid traffic data sequence type")
			}
			ipType := s.IPType
			if ipType == "" {
				ipType = "ipv4"
			}
			if ipType != "ipv4" && ipType != "ipv6" && ipType != "all" {
				return nil, errors.New("invalid traffic data IP type")
			}
			base := s.BaseAction
			if base == "" {
				base = "accept"
			}
			if base != "accept" && base != "drop" {
				return nil, errors.New("invalid traffic data base action")
			}
			matches := []any{}
			if s.MatchCriteria.SourceDataPrefix != "" {
				prefix, e := netip.ParsePrefix(s.MatchCriteria.SourceDataPrefix)
				if e != nil || (ipType == "ipv4" && !prefix.Addr().Is4()) || (ipType == "ipv6" && !prefix.Addr().Is6()) {
					return nil, errors.New("invalid traffic source prefix")
				}
				matches = append(matches, map[string]any{"field": "sourceIp", "value": s.MatchCriteria.SourceDataPrefix})
			}
			sequences = append(sequences, map[string]any{"sequenceId": s.ID, "sequenceName": s.Name, "sequenceType": mapped, "sequenceIpType": ipType, "baseAction": base, "match": map[string]any{"entries": matches}, "actions": []any{}})
		}
		out = append(out, Resource{Key: "centralized_policies/definitions/data_policy/traffic_data/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "traffic_data_policy_definition", Collection: "/dataservice/template/policy/definition/data/", Body: map[string]any{"type": "data", "name": p.Name, "description": p.Description, "defaultAction": map[string]any{"type": defaultAction}, "sequences": sequences}})
	}
	for _, p := range input.Definitions.DataPolicy.ApplicationAwareRouting {
		if p.Name == "" || p.Description == "" || seen["appRoute/"+p.Name] {
			return nil, errors.New("invalid or duplicate application-aware routing definition")
		}
		seen["appRoute/"+p.Name] = true
		sequences := []any{}
		ids := map[int]bool{}
		for _, s := range p.Sequences {
			if s.ID < 1 || s.ID > 65535 || ids[s.ID] || s.Name == "" {
				return nil, errors.New("invalid application-aware routing sequence")
			}
			ids[s.ID] = true
			ipType := s.IPType
			if ipType == "" {
				ipType = "ipv4"
			}
			if ipType != "ipv4" && ipType != "ipv6" && ipType != "all" {
				return nil, errors.New("invalid application-aware routing IP type")
			}
			matches := []any{}
			if s.MatchCriteria.SourceDataPrefix != "" {
				prefix, e := netip.ParsePrefix(s.MatchCriteria.SourceDataPrefix)
				if e != nil || (ipType == "ipv4" && !prefix.Addr().Is4()) || (ipType == "ipv6" && !prefix.Addr().Is6()) {
					return nil, errors.New("invalid application-aware routing source prefix")
				}
				matches = append(matches, map[string]any{"field": "sourceIp", "value": s.MatchCriteria.SourceDataPrefix})
			}
			actions := []any{}
			if s.Actions.Log != nil {
				row := map[string]any{"type": "log"}
				if *s.Actions.Log {
					row["parameter"] = ""
				}
				actions = append(actions, row)
			}
			sequences = append(sequences, map[string]any{"sequenceId": s.ID, "sequenceName": s.Name, "sequenceType": "appRoute", "sequenceIpType": ipType, "match": map[string]any{"entries": matches}, "actions": actions})
		}
		out = append(out, Resource{Key: "centralized_policies/definitions/data_policy/application_aware_routing/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "classic-definition", Type: "application_aware_routing_policy_definition", Collection: "/dataservice/template/policy/definition/approute/", Body: map[string]any{"type": "appRoute", "name": p.Name, "description": p.Description, "sequences": sequences}})
	}
	for _, p := range input.FeaturePolicies {
		if p.Name == "" || p.Description == "" || seen["feature/"+p.Name] {
			return nil, errors.New("invalid or duplicate centralized feature policy")
		}
		seen["feature/"+p.Name] = true
		refs := []ClassicDefinitionReference{}
		assembly := []any{}
		for _, item := range p.MeshTopology {
			if item.PolicyDefinition == "" {
				return nil, errors.New("empty mesh policy definition reference")
			}
			refs = append(refs, ClassicDefinitionReference{Type: "centralized_policies/definitions/control_policy/mesh_topology", Name: item.PolicyDefinition})
			assembly = append(assembly, map[string]any{"definitionId": "", "type": "mesh", "entries": []any{}})
		}
		for _, item := range p.HubAndSpokeTopology {
			if item.PolicyDefinition == "" {
				return nil, errors.New("empty hub-and-spoke policy definition reference")
			}
			refs = append(refs, ClassicDefinitionReference{Type: "centralized_policies/definitions/control_policy/hub_and_spoke_topology", Name: item.PolicyDefinition})
			assembly = append(assembly, map[string]any{"definitionId": "", "type": "hubAndSpoke", "entries": []any{}})
		}
		for _, item := range p.VPNMembership {
			if item.PolicyDefinition == "" {
				return nil, errors.New("empty membership policy definition reference")
			}
			refs = append(refs, ClassicDefinitionReference{Type: "centralized_policies/definitions/control_policy/vpn_membership", Name: item.PolicyDefinition})
			assembly = append(assembly, map[string]any{"definitionId": "", "type": "vpnMembershipGroup", "entries": []any{}})
		}
		if len(p.Cflowd) > 0 {
			return nil, errors.New("centralized cflowd policy binding is not yet implemented")
		}
		out = append(out, Resource{Key: "centralized_policies/feature_policies/" + p.Name, Name: p.Name, Section: "centralized_policies", Kind: "centralized-policy", Type: "centralized_policy", Collection: "/dataservice/template/policy/vsmart/", ClassicDefinitionRefs: refs, Body: map[string]any{"policyType": "feature", "policyName": p.Name, "policyDescription": p.Description, "policyDefinition": map[string]any{"assembly": assembly}}})
	}
	return out, nil
}

func setPolicyReference(value any, path []string, id string) error {
	if len(path) == 0 {
		return ErrResponse
	}
	switch node := value.(type) {
	case map[string]any:
		child, ok := node[path[0]]
		if !ok {
			return ErrResponse
		}
		if len(path) == 1 {
			node[path[0]] = id
			return nil
		}
		return setPolicyReference(child, path[1:], id)
	case []any:
		i, err := strconv.Atoi(path[0])
		if err != nil || i < 0 || i >= len(node) {
			return ErrResponse
		}
		if len(path) == 1 {
			node[i] = id
			return nil
		}
		return setPolicyReference(node[i], path[1:], id)
	default:
		return ErrResponse
	}
}

func (c *Client) resolveClassicPolicyLists(ctx context.Context, r Resource) error {
	for _, ref := range r.PolicyListRefs {
		spec, ok := policyLists[ref.Section]
		if !ok {
			return ErrResponse
		}
		probe := Resource{Name: ref.Name, Type: spec.kind, Kind: "policy-list", Collection: "/dataservice/template/policy/list/" + spec.endpoint}
		o, err := c.observePolicyList(ctx, probe)
		if err != nil {
			return err
		}
		if o == nil {
			return fmt.Errorf("policy list %q: %w", ref.Name, ErrDependencyPending)
		}
		if err := setPolicyReference(r.Body, ref.Path, o.ID); err != nil {
			return err
		}
	}
	return nil
}
