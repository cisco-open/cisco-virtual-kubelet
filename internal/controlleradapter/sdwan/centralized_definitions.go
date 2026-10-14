// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

type PolicyListReference struct {
	Section, Name string
	Path          []string
}

type CentralizedPolicies struct {
	Definitions struct {
		ControlPolicy struct {
			VPNMembership []CentralizedVPNMembership `json:"vpn_membership,omitempty"`
			MeshTopology  []CentralizedMeshTopology  `json:"mesh_topology,omitempty"`
		} `json:"control_policy"`
	} `json:"definitions"`
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

func compileCentralizedPolicies(input *CentralizedPolicies) ([]Resource, error) {
	if input == nil {
		return nil, nil
	}
	out := []Resource{}
	seen := map[string]bool{}
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
