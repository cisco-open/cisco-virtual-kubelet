// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/url"
	"reflect"
	"slices"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const fabricExtranetPath = "/dna/intent/api/v1/sda/extranetPolicies"

type fabricExtranetPolicy struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"extranetPolicyName"`
	Provider    string   `json:"providerVirtualNetworkName"`
	Subscribers []string `json:"subscriberVirtualNetworkNames"`
	Fabrics     []string `json:"fabricIds,omitempty"`
}
type fabricExtranetIntent struct {
	policy fabricExtranetPolicy
	paths  []string
}

func extranetStringSet(value any, required bool, valid func(string) bool) ([]string, error) {
	if value == nil && !required {
		return nil, nil
	}
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 || (required && len(rows) == 0) {
		return nil, errInvalidResponse
	}
	out := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		s, ok := row.(string)
		if !ok || !valid(s) || seen[s] {
			return nil, errInvalidResponse
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}
func parseFabricExtranets(value any) ([]fabricExtranetIntent, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	out := []fabricExtranetIntent{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		for k := range m {
			if k != "name" && k != "provider_virtual_network" && k != "subscriber_virtual_networks" && k != "fabric_sites" {
				return nil, errInvalidResponse
			}
		}
		name, nok := m["name"].(string)
		provider, pok := m["provider_virtual_network"].(string)
		if !nok || !pok || !validProfileName(name) || !validProfileName(provider) || seen[name] {
			return nil, errInvalidResponse
		}
		subscribers, err := extranetStringSet(m["subscriber_virtual_networks"], true, validProfileName)
		if err != nil || slices.Contains(subscribers, provider) {
			return nil, errInvalidResponse
		}
		paths, err := extranetStringSet(m["fabric_sites"], false, func(s string) bool { return validSitePath(s) && s != "Global" })
		if err != nil {
			return nil, err
		}
		seen[name] = true
		out = append(out, fabricExtranetIntent{policy: fabricExtranetPolicy{Name: name, Provider: provider, Subscribers: subscribers}, paths: paths})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].policy.Name < out[j].policy.Name })
	return out, nil
}

func canonicalExtranet(p fabricExtranetPolicy) (fabricExtranetPolicy, error) {
	if !validAPIID(p.ID) || !validProfileName(p.Name) || !validProfileName(p.Provider) || len(p.Subscribers) == 0 {
		return p, errInvalidResponse
	}
	normalize := func(in []string, valid func(string) bool) ([]string, error) {
		out := append([]string{}, in...)
		sort.Strings(out)
		for i, s := range out {
			if !valid(s) || (i > 0 && out[i-1] == s) {
				return nil, errInvalidResponse
			}
		}
		return out, nil
	}
	var err error
	p.Subscribers, err = normalize(p.Subscribers, validProfileName)
	if err != nil || slices.Contains(p.Subscribers, p.Provider) {
		return p, errInvalidResponse
	}
	p.Fabrics, err = normalize(p.Fabrics, validAPIID)
	return p, err
}
func (c *client) fabricExtranets(ctx context.Context) (map[string]fabricExtranetPolicy, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, fabricExtranetPath, nil, func(p fabricExtranetPolicy) string { return p.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]fabricExtranetPolicy{}
	for _, row := range rows {
		p, err := canonicalExtranet(row)
		if err != nil {
			return nil, err
		}
		if _, exists := out[p.Name]; exists {
			return nil, errInvalidResponse
		}
		out[p.Name] = p
	}
	return out, nil
}

type fabricExtranetExecutor struct {
	client  *client
	desired []fabricExtranetIntent
}

func (e *fabricExtranetExecutor) count() int { return len(e.desired) }

// Extranet writes change forwarding across multiple VNs/fabrics. The same gate
// must wrap prerequisite compositions until group admission is enabled.
func (*fabricExtranetExecutor) next(context.Context) (*configurationWrite, error) {
	return nil, errConfigurationDeviceAdmission
}
func (*fabricExtranetExecutor) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	return false, errConfigurationDeviceAdmission
}

type fabricExtranetAdmissionGate struct{ configurationExecutor }

func (*fabricExtranetAdmissionGate) next(context.Context) (*configurationWrite, error) {
	return nil, errConfigurationDeviceAdmission
}
func (*fabricExtranetAdmissionGate) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	return false, errConfigurationDeviceAdmission
}

type fabricExtranetBindings struct {
	policies []fabricExtranetPolicy
	networks map[string]fabricL3Network
}

func (e *fabricExtranetExecutor) bindings(ctx context.Context) (*fabricExtranetBindings, error) {
	networks, err := e.client.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	needsScope := false
	for _, want := range e.desired {
		needsScope = needsScope || len(want.paths) > 0
	}
	if needsScope {
		sites, err := e.client.ListSites(ctx)
		if err != nil {
			return nil, err
		}
		fabrics, err := e.client.fabricSites(ctx)
		if err != nil {
			return nil, err
		}
		zones, err := e.client.fabricZones(ctx)
		if err != nil {
			return nil, err
		}
		for _, site := range sites {
			id := ""
			if f, ok := fabrics[site.ID]; ok {
				id = f.ID
			}
			if z, ok := zones[site.ID]; ok {
				if id != "" {
					return nil, errInvalidResponse
				}
				id = z.ID
			}
			if id != "" {
				if _, duplicate := paths[site.NameHierarchy]; duplicate {
					return nil, errInvalidResponse
				}
				paths[site.NameHierarchy] = id
			}
		}
	}
	out := &fabricExtranetBindings{networks: map[string]fabricL3Network{}}
	for _, want := range e.desired {
		p := want.policy
		p.Fabrics = []string{}
		for _, path := range want.paths {
			id := paths[path]
			if id == "" {
				return nil, errConfigurationBusy
			}
			if slices.Contains(p.Fabrics, id) {
				return nil, errInvalidResponse
			}
			p.Fabrics = append(p.Fabrics, id)
		}
		sort.Strings(p.Fabrics)
		for _, name := range append([]string{p.Provider}, p.Subscribers...) {
			n, ok := networks[name]
			if !ok {
				return nil, errConfigurationBusy
			}
			n.Fabrics = append([]string{}, n.Fabrics...)
			sort.Strings(n.Fabrics)
			out.networks[name] = n
		}
		out.policies = append(out.policies, p)
	}
	return out, nil
}
func (e *fabricExtranetExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	before, err := e.bindings(ctx)
	if err != nil {
		return nil, err
	}
	actual, err := e.client.fabricExtranets(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range before.policies {
		got, present := actual[want.Name]
		change := configapi.NetworkControllerDriftChangeAdd
		if present {
			// The pinned module emits null for omitted/empty fabric_sites. Report
			// compares only an explicitly selected nonempty scope, never inferring a
			// request to remove live fabric associations from that null value.
			if want.Provider == got.Provider && slices.Equal(want.Subscribers, got.Subscribers) && (len(want.Fabrics) == 0 || slices.Equal(want.Fabrics, got.Fabrics)) {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: "extranet-policy:" + want.Name, Path: "/extranet_policies", Change: change})
	}
	after, err := e.bindings(ctx)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, errConfigurationStale
	}
	return out, nil
}
