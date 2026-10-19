// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const authProfilesPath = "/dna/intent/api/v1/sda/authenticationProfiles"

type authContract struct {
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Port     string `json:"port"`
}
type authACL struct {
	Description    *string         `json:"description,omitempty"`
	Enabled        *bool           `json:"enabled"`
	ImplicitAction *string         `json:"implicitAction"`
	Contracts      *[]authContract `json:"accessContracts"`
}
type authProfile struct {
	ID       string   `json:"id"`
	FabricID string   `json:"fabricId"`
	Name     string   `json:"authenticationProfileName"`
	Order    *string  `json:"authenticationOrder"`
	Timeout  *int     `json:"dot1xToMabFallbackTimeout"`
	Wake     *bool    `json:"wakeOnLan"`
	Hosts    *string  `json:"numberOfHosts"`
	ACL      *authACL `json:"preAuthAcl"`
	BPDU     *bool    `json:"isBpduGuardEnabled,omitempty"`
	Voice    *bool    `json:"isVoiceVlanEnabled,omitempty"`
}

func validAuthProfile(p authProfile) bool {
	if p.Name != "Closed Authentication" && p.Name != "Open Authentication" && p.Name != "Low Impact" {
		return false
	}
	if p.Order == nil || (*p.Order != "dot1x" && *p.Order != "mac") || p.Timeout == nil || *p.Timeout < 0 || p.Wake == nil || p.Hosts == nil || (*p.Hosts != "Single" && *p.Hosts != "Unlimited") {
		return false
	}
	if p.Name == "Low Impact" {
		a := p.ACL
		if a == nil || a.Enabled == nil || a.ImplicitAction == nil || (*a.ImplicitAction != "PERMIT" && *a.ImplicitAction != "DENY") || a.Contracts == nil {
			return false
		}
		seen := map[string]bool{}
		for _, c := range *a.Contracts {
			if (c.Action != "PERMIT" && c.Action != "DENY") || (c.Protocol != "UDP" && c.Protocol != "TCP" && c.Protocol != "TCP_UDP") || (c.Port != "domain" && c.Port != "bootpc" && c.Port != "bootps") || c.Port != "domain" && c.Protocol != "UDP" || seen[c.Port] {
				return false
			}
			seen[c.Port] = true
		}
	}
	return true
}
func parseAuthTemplates(raw []byte) ([]authProfile, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("authentication template report requires only authentication_templates")
	}
	list, ok := root["authentication_templates"].([]any)
	if !ok || len(list) == 0 || len(list) > 3 {
		return nil, errors.New("authentication templates must select 1 to 3 global profiles")
	}
	out := []authProfile{}
	seen := map[string]bool{}
	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("invalid authentication template")
		}
		for key := range object {
			switch key {
			case "name", "authentication_order", "dot1x_to_mab_fallback_timeout", "wake_on_lan", "number_of_hosts", "pre_auth_acl":
			default:
				return nil, errors.New("unsupported global authentication field")
			}
		}
		name, _ := object["name"].(string)
		order, ok1 := object["authentication_order"].(string)
		timeout, ok2 := object["dot1x_to_mab_fallback_timeout"].(int)
		wake, ok3 := object["wake_on_lan"].(bool)
		hosts, ok4 := object["number_of_hosts"].(string)
		if !ok1 || !ok2 || !ok3 || !ok4 || seen[name] {
			return nil, errors.New("authentication profile values must be explicit and unique")
		}
		seen[name] = true
		p := authProfile{Name: name, Order: &order, Timeout: &timeout, Wake: &wake, Hosts: &hosts}
		if raw, present := object["pre_auth_acl"]; present {
			if name != "Low Impact" {
				return nil, errors.New("pre-auth ACL requires Low Impact profile")
			}
			acl, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("invalid pre-auth ACL")
			}
			for key := range acl {
				switch key {
				case "enabled", "implicit_action", "access_contracts", "description":
				default:
					return nil, errors.New("unsupported pre-auth ACL field")
				}
			}
			// Upstream deliberately ignores description changes. Validate its type only.
			if v, present := acl["description"]; present {
				if _, ok := v.(string); !ok {
					return nil, errors.New("invalid pre-auth description")
				}
			}
			enabled, ok1 := acl["enabled"].(bool)
			action, ok2 := acl["implicit_action"].(string)
			contracts, ok3 := acl["access_contracts"].([]any)
			if !ok1 || !ok2 || !ok3 {
				return nil, errors.New("pre-auth ACL values must be explicit")
			}
			parsed := []authContract{}
			for _, v := range contracts {
				m, ok := v.(map[string]any)
				if !ok || len(m) != 3 {
					return nil, errors.New("invalid access contract")
				}
				a, aok := m["action"].(string)
				p, pok := m["protocol"].(string)
				port, portok := m["port"].(string)
				if !aok || !pok || !portok {
					return nil, errors.New("invalid access contract")
				}
				parsed = append(parsed, authContract{Action: a, Protocol: p, Port: port})
			}
			p.ACL = &authACL{Enabled: &enabled, ImplicitAction: &action, Contracts: &parsed}
		}
		if !validAuthProfile(p) {
			return nil, errors.New("invalid or unresolved authentication profile")
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (c *client) listAuthTemplates(ctx context.Context) ([]authProfile, error) {
	return c.listAuthProfiles(ctx, "")
}

func (c *client) listAuthProfiles(ctx context.Context, fabricID string) ([]authProfile, error) {
	if fabricID != "" && !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	query := url.Values{"isGlobalAuthenticationProfile": {"true"}}
	if fabricID != "" {
		query = url.Values{"fabricId": {fabricID}, "isGlobalAuthenticationProfile": {"false"}}
	}
	profiles, err := listInventoryQueryWithGet(ctx, get, authProfilesPath, query, func(p authProfile) string { return p.ID })
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		if p.FabricID != fabricID || seen[p.Name] || !validAPIID(p.ID) || !validAuthProfile(p) || p.BPDU != nil && p.Name != "Closed Authentication" {
			return nil, errInvalidResponse
		}
		seen[p.Name] = true
	}
	return profiles, nil
}
func (c *client) reportAuthTemplates(ctx context.Context, want []authProfile) ([]configapi.NetworkControllerDriftEntry, error) {
	got, err := c.listAuthTemplates(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]authProfile{}
	for _, p := range got {
		byName[p.Name] = p
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, w := range want {
		g, ok := byName[w.Name]
		if !ok {
			return nil, errors.New("built-in global authentication profile is missing")
		}
		for _, field := range []struct {
			name string
			a, b any
		}{{"authentication_order", w.Order, g.Order}, {"dot1x_to_mab_fallback_timeout", w.Timeout, g.Timeout}, {"wake_on_lan", w.Wake, g.Wake}, {"number_of_hosts", w.Hosts, g.Hosts}, {"pre_auth_acl", w.ACL, g.ACL}} {
			if field.name == "pre_auth_acl" && w.Name != "Low Impact" {
				continue
			}
			equal := reflect.DeepEqual(field.a, field.b)
			if field.name == "pre_auth_acl" {
				equal = authACLManagedEqual(w.ACL, g.ACL)
			}
			if !equal {
				out = append(out, configapi.NetworkControllerDriftEntry{Section: "authentication_templates", Resource: w.Name, Path: "/" + field.name, Change: configapi.NetworkControllerDriftChangeUpdate})
			}
		}
	}
	return out, nil
}
