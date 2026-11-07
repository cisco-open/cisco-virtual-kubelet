// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
	"net/netip"
)

type SecurityPolicies struct {
	FeaturePolicies []ClassicSecurityFeaturePolicy `json:"feature_policies,omitempty"`
	Definitions     struct {
		ZoneBasedFirewall   []ClassicZoneBasedFirewall   `json:"zone_based_firewall,omitempty"`
		IntrusionPrevention []ClassicIntrusionPrevention `json:"intrusion_prevention,omitempty"`
	} `json:"definitions"`
}
type ClassicSecurityFeaturePolicy struct {
	Name                      string   `json:"name"`
	Description               string   `json:"description"`
	Mode                      string   `json:"mode,omitempty"`
	UseCase                   string   `json:"use_case"`
	IntrusionPreventionPolicy string   `json:"intrusion_prevention_policy,omitempty"`
	FirewallPolicies          []string `json:"firewall_policies,omitempty"`
}
type ClassicZoneBasedFirewall struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	Mode              string `json:"mode,omitempty"`
	DefaultActionType string `json:"default_action_type"`
	ZonePairs         []struct {
		SourceZone      string `json:"source_zone"`
		DestinationZone string `json:"destination_zone"`
	} `json:"zone_pairs"`
	Rules []struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		BaseAction    string `json:"base_action"`
		IPType        string `json:"ip_type,omitempty"`
		MatchCriteria struct {
			SourceIPPrefix string `json:"source_ip_prefix,omitempty"`
		} `json:"match_criterias"`
		Actions struct {
			Log bool `json:"log,omitempty"`
		} `json:"actions"`
	} `json:"rules"`
}
type ClassicIntrusionPrevention struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	InspectionMode string   `json:"inspection_mode,omitempty"`
	LogLevel       string   `json:"log_level,omitempty"`
	SignatureSet   string   `json:"signature_set,omitempty"`
	TargetVPNs     []string `json:"target_vpns,omitempty"`
}

func compileSecurityPolicies(input *SecurityPolicies) ([]Resource, error) {
	if input == nil {
		return nil, nil
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, p := range input.Definitions.ZoneBasedFirewall {
		r, err := compileZoneBasedFirewall(p)
		if err != nil {
			return nil, err
		}
		if seen["firewall/"+p.Name] {
			return nil, errors.New("duplicate zone firewall definition")
		}
		seen["firewall/"+p.Name] = true
		out = append(out, r)
	}
	for _, p := range input.Definitions.IntrusionPrevention {
		if p.Name == "" || p.Description == "" || seen[p.Name] {
			return nil, errors.New("invalid or duplicate intrusion prevention definition")
		}
		seen[p.Name] = true
		if p.InspectionMode != "" && p.InspectionMode != "protection" && p.InspectionMode != "detection" {
			return nil, errors.New("invalid IPS inspection mode")
		}
		if p.SignatureSet != "" && p.SignatureSet != "balanced" && p.SignatureSet != "connectivity" && p.SignatureSet != "security" {
			return nil, errors.New("invalid IPS signature set")
		}
		validLogs := map[string]bool{"emergency": true, "alert": true, "critical": true, "error": true, "warning": true, "notice": true, "info": true, "debug": true}
		if p.LogLevel != "" && !validLogs[p.LogLevel] {
			return nil, errors.New("invalid IPS log level")
		}
		definition := map[string]any{"logging": []any{}}
		if p.InspectionMode != "" {
			definition["inspectionMode"] = p.InspectionMode
		}
		if p.LogLevel != "" {
			definition["logLevel"] = p.LogLevel
		}
		if p.SignatureSet != "" {
			definition["signatureSet"] = p.SignatureSet
		}
		if p.TargetVPNs != nil {
			if len(p.TargetVPNs) == 0 {
				return nil, errors.New("empty IPS target VPNs")
			}
			definition["targetVpns"] = p.TargetVPNs
		}
		out = append(out, Resource{Key: "security_policies/definitions/intrusion_prevention/" + p.Name, Name: p.Name, Section: "security_policies", Kind: "classic-definition", Type: "intrusion_prevention_policy_definition", Collection: "/dataservice/template/policy/definition/intrusionprevention/", Body: map[string]any{"type": "intrusionPrevention", "name": p.Name, "description": p.Description, "mode": "security", "definition": definition}})
	}
	for _, p := range input.FeaturePolicies {
		if p.Name == "" || p.Description == "" || (p.IntrusionPreventionPolicy == "" && len(p.FirewallPolicies) == 0) {
			return nil, errors.New("invalid security feature policy")
		}
		mode := p.Mode
		if mode == "" {
			mode = "security"
		}
		if mode != "security" {
			return nil, errors.New("unsupported security policy mode")
		}
		useCase := p.UseCase
		if useCase == "" {
			useCase = "custom"
		}
		validUseCase := map[string]bool{"custom": true, "compliance": true, "guestAccess": true, "directCloudAccess": true, "directInternetAccess": true}
		if !validUseCase[useCase] {
			return nil, errors.New("invalid security policy use_case")
		}
		refs, assembly := []ClassicDefinitionReference{}, []any{}
		for _, name := range p.FirewallPolicies {
			if name == "" {
				return nil, errors.New("empty firewall policy reference")
			}
			refs = append(refs, ClassicDefinitionReference{Type: "security_policies/definitions/zone_based_firewall", Name: name})
			assembly = append(assembly, map[string]any{"definitionId": "", "type": "zoneBasedFW", "entries": []any{}})
		}
		if p.IntrusionPreventionPolicy != "" {
			refs = append(refs, ClassicDefinitionReference{Type: "security_policies/definitions/intrusion_prevention", Name: p.IntrusionPreventionPolicy})
			assembly = append(assembly, map[string]any{"definitionId": "", "type": "intrusionPrevention"})
		}
		out = append(out, Resource{Key: "security_policies/feature_policies/" + p.Name, Name: p.Name, Section: "security_policies", Kind: "security-policy", Type: "security_policy", Collection: "/dataservice/template/policy/security/", ClassicDefinitionRefs: refs, Body: map[string]any{"policyType": "feature", "policyName": p.Name, "policyDescription": p.Description, "policyMode": mode, "policyUseCase": useCase, "policyDefinition": map[string]any{"assembly": assembly, "settings": map[string]any{"logging": []any{}}}}})
	}
	return out, nil
}

func compileZoneBasedFirewall(p ClassicZoneBasedFirewall) (Resource, error) {
	if p.Name == "" || p.Description == "" {
		return Resource{}, errors.New("invalid zone firewall definition")
	}
	mode := p.Mode
	if mode == "" {
		mode = "security"
	}
	if mode != "security" {
		return Resource{}, errors.New("unsupported zone firewall mode")
	}
	if p.DefaultActionType != "pass" && p.DefaultActionType != "drop" {
		return Resource{}, errors.New("invalid zone firewall default action")
	}
	entries, refs := []any{}, []PolicyListReference{}
	for i, z := range p.ZonePairs {
		if z.SourceZone == "" || z.DestinationZone == "" {
			return Resource{}, errors.New("zone pair requires source and destination")
		}
		row := map[string]any{"sourceZone": "", "destinationZone": ""}
		for _, side := range []struct{ value, field string }{{z.SourceZone, "sourceZone"}, {z.DestinationZone, "destinationZone"}} {
			if side.value == "self_zone" {
				row[side.field] = "self"
			} else {
				refs = append(refs, PolicyListReference{Section: "zones", Name: side.value, Path: []string{"definition", "entries", fmt.Sprint(i), side.field}})
			}
		}
		entries = append(entries, row)
	}
	sequences, ids := []any{}, map[int]bool{}
	for _, rule := range p.Rules {
		if rule.ID < 1 || rule.ID > 65535 || ids[rule.ID] || rule.Name == "" || (rule.BaseAction != "pass" && rule.BaseAction != "drop" && rule.BaseAction != "inspect") {
			return Resource{}, errors.New("invalid zone firewall rule")
		}
		ids[rule.ID] = true
		ipType := rule.IPType
		if ipType == "" {
			ipType = "ipv4"
		}
		if ipType != "ipv4" && ipType != "ipv6" {
			return Resource{}, errors.New("invalid zone firewall ip_type")
		}
		matches := []any{}
		if rule.MatchCriteria.SourceIPPrefix != "" {
			prefix, e := netip.ParsePrefix(rule.MatchCriteria.SourceIPPrefix)
			if e != nil || (ipType == "ipv4" && !prefix.Addr().Is4()) || (ipType == "ipv6" && !prefix.Addr().Is6()) {
				return Resource{}, errors.New("invalid zone firewall source prefix")
			}
			matches = append(matches, map[string]any{"field": "sourceIp", "value": rule.MatchCriteria.SourceIPPrefix})
		}
		actions := []any{}
		if rule.Actions.Log {
			actions = append(actions, map[string]any{"type": "log"})
		}
		sequences = append(sequences, map[string]any{"sequenceId": rule.ID, "sequenceName": rule.Name, "baseAction": rule.BaseAction, "sequenceType": "zoneBasedFW", "sequenceIpType": ipType, "match": map[string]any{"entries": matches}, "actions": actions})
	}
	return Resource{Key: "security_policies/definitions/zone_based_firewall/" + p.Name, Name: p.Name, Section: "security_policies", Kind: "classic-definition", Type: "zone_based_firewall_policy_definition", Collection: "/dataservice/template/policy/definition/zonebasedfw/", PolicyListRefs: refs, Body: map[string]any{"type": "zoneBasedFW", "name": p.Name, "description": p.Description, "mode": mode, "definition": map[string]any{"entries": entries, "defaultAction": map[string]any{"type": p.DefaultActionType}, "sequences": sequences}}}, nil
}
