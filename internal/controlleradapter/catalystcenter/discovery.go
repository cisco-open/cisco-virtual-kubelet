// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const discoveryPath = "/dna/intent/api/v1/discovery"

type discoveryIntent struct {
	name        string
	fields      map[string]any
	credentials []string
}

func (d discoveryIntent) resource() string { return "discovery:" + d.name }
func discoveryType(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "SINGLE", "RANGE", "MULTI RANGE", "CDP", "LLDP", "CIDR":
		return s
	}
	return ""
}

// Keep canonical ranges without enumerating a subnet into individual hosts.
func discoveryAddresses(value string, cidr bool) (string, error) {
	if value == "" || len(value) > 8192 {
		return "", errors.New("invalid discovery address list")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range strings.Split(value, ",") {
		if len(out) >= 128 || strings.TrimSpace(v) != v {
			return "", errors.New("invalid discovery address list")
		}
		normalized := ""
		if cidr {
			p, err := netip.ParsePrefix(v)
			if err != nil || !p.Addr().Is4() {
				return "", errors.New("discovery requires IPv4 prefixes")
			}
			normalized = p.Masked().String()
		} else {
			parts := strings.Split(v, "-")
			if len(parts) > 2 {
				return "", errors.New("invalid discovery address range")
			}
			first, err := netip.ParseAddr(parts[0])
			if err != nil || !first.Is4() {
				return "", errors.New("discovery requires IPv4 addresses")
			}
			last := first
			if len(parts) == 2 {
				last, err = netip.ParseAddr(parts[1])
				if err != nil || !last.Is4() || last.Compare(first) < 0 {
					return "", errors.New("invalid discovery address range")
				}
			}
			normalized = first.String() + "-" + last.String()
		}
		if seen[normalized] {
			return "", errors.New("duplicate discovery address range")
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	sort.Strings(out)
	return strings.Join(out, ","), nil
}
func parseDiscoveryModels(value any) ([]discoveryIntent, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 100 {
		return nil, errors.New("discovery requires a list of at most 100 jobs")
	}
	out := []discoveryIntent{}
	seen := map[string]bool{}
	for _, v := range list {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("invalid discovery job")
		}
		name, err := globalCredentialText(m["name"])
		if err != nil || seen[name] {
			return nil, errors.New("invalid or duplicate discovery name")
		}
		seen[name] = true
		d := discoveryIntent{name: name, fields: map[string]any{"name": name}}
		for key, value := range m {
			switch key {
			case "name":
			case "type":
				s, ok := value.(string)
				if !ok || discoveryType(s) == "" {
					return nil, errors.New("invalid discovery type")
				}
				d.fields["discoveryType"] = discoveryType(s)
			case "ip_address_list":
				s, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid discovery addresses")
				}
				d.fields["ipAddressList"] = s
			case "protocol_order":
				s, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid discovery protocol order")
				}
				s = strings.ToUpper(s)
				if s != "SSH" && s != "TELNET" && s != "SSH,TELNET" && s != "TELNET,SSH" {
					return nil, errors.New("invalid discovery protocol order")
				}
				d.fields["protocolOrder"] = s
			case "global_credential_list":
				list, ok := value.([]any)
				if !ok || len(list) == 0 || len(list) > 64 {
					return nil, errors.New("discovery credential names are required")
				}
				names := map[string]bool{}
				for _, v := range list {
					s, err := globalCredentialText(v)
					if err != nil || names[s] {
						return nil, errors.New("invalid discovery credential reference")
					}
					names[s] = true
					d.credentials = append(d.credentials, s)
				}
				sort.Strings(d.credentials)
			case "cdp_level", "lldp_level", "retry", "time_out":
				// Optional Terraform nulls are unselected, not a request for a
				// guessed default or a native zero. Unknown fields still fail.
				if value == nil {
					continue
				}
				api := map[string]string{"cdp_level": "cdpLevel", "lldp_level": "lldpLevel", "retry": "retry", "time_out": "timeout"}[key]
				min, max := 0, 16
				if key == "retry" {
					max = 10
				}
				if key == "time_out" {
					min, max = 1, 3600
				}
				n, err := policyServerInteger(value, min, max)
				if err != nil {
					return nil, err
				}
				d.fields[api] = *n
			case "netconf_port":
				if value == nil {
					continue
				}
				s, ok := value.(string)
				if !ok {
					return nil, errors.New("netconf_port must be a resolved port string")
				}
				n, err := strconv.Atoi(s)
				if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != s {
					return nil, errors.New("invalid NETCONF port")
				}
				d.fields["netconfPort"] = s
			case "preferred_mgmt_ip_method":
				if value == nil {
					continue
				}
				s, ok := value.(string)
				if !ok || (s != "None" && s != "UseLoopBack") {
					return nil, errors.New("invalid preferred management IP method")
				}
				d.fields["preferredMgmtIPMethod"] = s
			case "ip_filter_list":
				if value == nil {
					continue
				}
				list, ok := value.([]any)
				if !ok || len(list) > 128 {
					return nil, errors.New("invalid discovery filter list")
				}
				items := []string{}
				for _, v := range list {
					s, ok := v.(string)
					if !ok {
						return nil, errors.New("invalid discovery filter")
					}
					normalized, err := discoveryAddresses(s, false)
					if err != nil {
						return nil, err
					}
					items = append(items, strings.Split(normalized, ",")...)
				}
				sort.Strings(items)
				if len(slices.Compact(slices.Clone(items))) != len(items) {
					return nil, errors.New("duplicate discovery filter")
				}
				d.fields["ipFilterList"] = items
			case "create_per_site":
				b, ok := value.(bool)
				if !ok || b {
					return nil, errors.New("per-site discovery scope is not implemented")
				}
			default:
				return nil, errors.New("unsupported discovery field; use global credential references, not inline credentials")
			}
		}
		kind, _ := d.fields["discoveryType"].(string)
		addresses, _ := d.fields["ipAddressList"].(string)
		if kind == "" || len(d.credentials) == 0 || d.fields["protocolOrder"] == nil {
			return nil, errors.New("resolved discovery type, protocol order and credentials are required")
		}
		normalized, err := discoveryAddresses(addresses, kind == "CIDR")
		if err != nil {
			return nil, err
		}
		if kind == "SINGLE" || kind == "CDP" || kind == "LLDP" {
			if strings.Contains(addresses, ",") || strings.Contains(addresses, "-") {
				return nil, errors.New("discovery type requires one seed address")
			}
		}
		if kind != "CDP" && d.fields["cdpLevel"] != nil || kind != "LLDP" && d.fields["lldpLevel"] != nil {
			return nil, errors.New("neighbor depth is incompatible with discovery type")
		}
		_ = normalized // Validation does not rewrite the API's type-specific request syntax.
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// Explicitly omit every password/community field returned by discovery APIs.
type discoveryMetadata struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Type          string          `json:"discoveryType"`
	Addresses     string          `json:"ipAddressList"`
	Condition     string          `json:"discoveryCondition"`
	Status        string          `json:"discoveryStatus"`
	CredentialIDs []string        `json:"globalCredentialIdList"`
	Protocol      string          `json:"protocolOrder"`
	CDP           *int            `json:"cdpLevel"`
	LLDP          *int            `json:"lldpLevel"`
	Retry         *int            `json:"retry"`
	Timeout       *int            `json:"timeout"`
	Netconf       string          `json:"netconfPort"`
	Preferred     string          `json:"preferredMgmtIPMethod"`
	Filters       json.RawMessage `json:"ipFilterList"`
	Usernames     []string        `json:"userNameList,omitempty"`
	SNMPVersion   string          `json:"snmpVersion,omitempty"`
	SNMPMode      string          `json:"snmpMode,omitempty"`
	SNMPUser      string          `json:"snmpUserName,omitempty"`
	SNMPAuth      string          `json:"snmpAuthProtocol,omitempty"`
	SNMPPrivacy   string          `json:"snmpPrivProtocol,omitempty"`
	SNMPRODesc    string          `json:"snmpRoCommunityDesc,omitempty"`
	SNMPRWDesc    string          `json:"snmpRwCommunityDesc,omitempty"`
}

// The discovery response schema exposes v3 mode/user but omits snmpVersion.
// Derive only from that positive v3 evidence; missing/unknown modes and explicit
// contradictory versions must not be repaired into a matching desired state.
func normalizeDiscoverySNMPReadback(d discoveryMetadata) discoveryMetadata {
	mode := ""
	switch strings.ToUpper(d.SNMPMode) {
	case "NOAUTHNOPRIV":
		mode = "NoAuthNoPriv"
	case "AUTHNOPRIV":
		mode = "AuthNoPriv"
	case "AUTHPRIV":
		mode = "AuthPriv"
	}
	if mode != "" {
		d.SNMPMode = mode
		if d.SNMPVersion == "" && d.SNMPUser != "" {
			d.SNMPVersion = "v3"
		}
	}
	return d
}

func (c *client) discoveries(ctx context.Context) (map[string]discoveryMetadata, error) {
	out := map[string]discoveryMetadata{}
	ids := map[string]bool{}
	for start := 1; start <= 10000; start += 100 {
		raw, err := c.get(ctx, fmt.Sprintf("%s/%d/100", discoveryPath, start), nil, 200)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Response *[]discoveryMetadata `json:"response"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil || len(*envelope.Response) > 100 {
			return nil, errInvalidResponse
		}
		for _, d := range *envelope.Response {
			if !validAPIID(d.ID) || ids[d.ID] || d.Name == "" || discoveryType(d.Type) == "" {
				return nil, errInvalidResponse
			}
			if _, exists := out[d.Name]; exists {
				return nil, errInvalidResponse
			}
			ids[d.ID] = true
			out[d.Name] = normalizeDiscoverySNMPReadback(d)
		}
		if len(*envelope.Response) < 100 {
			return out, nil
		}
	}
	return nil, errors.New("discovery inventory exceeds complete-observation bound")
}
func resolveDiscoveryCredentials(want discoveryIntent, all map[string]map[string]globalCredentialMetadata) (map[string]any, error) {
	fields := map[string]any{}
	for k, v := range want.fields {
		fields[k] = v
	}
	ids := []string{}
	for _, name := range want.credentials {
		matches := []string{}
		for _, group := range all {
			if item, found := group[name]; found {
				matches = append(matches, item.ID)
			}
		}
		if len(matches) != 1 || !validAPIID(matches[0]) {
			return nil, errors.New("discovery credential name is missing or ambiguous across types")
		}
		ids = append(ids, matches[0])
	}
	sort.Strings(ids)
	fields["globalCredentialIdList"] = ids
	return fields, nil
}
func discoveryMatches(want map[string]any, got discoveryMetadata) (bool, error) {
	raw, _ := json.Marshal(got)
	var actual map[string]any
	_ = json.Unmarshal(raw, &actual)
	for key, value := range want {
		observed := actual[key]
		switch key {
		case "discoveryType":
			normalize := func(s string) string {
				k := discoveryType(s)
				if k == "SINGLE" || k == "MULTI RANGE" {
					return "RANGE"
				}
				return k
			}
			observed = normalize(got.Type)
			value = normalize(value.(string))
		case "ipAddressList":
			var err error
			value, err = discoveryAddresses(value.(string), want["discoveryType"] == "CIDR")
			if err != nil {
				return false, err
			}
			observed, err = discoveryAddresses(got.Addresses, discoveryType(got.Type) == "CIDR")
			if err != nil {
				return false, errInvalidResponse
			}
		case "protocolOrder":
			observed = strings.ToUpper(got.Protocol)
		case "userNameList":
			items := slices.Clone(got.Usernames)
			sort.Strings(items)
			for i, item := range items {
				if _, err := globalCredentialText(item); err != nil || i > 0 && item == items[i-1] {
					return false, errInvalidResponse
				}
			}
			observed = items
		case "globalCredentialIdList":
			ids := slices.Clone(got.CredentialIDs)
			sort.Strings(ids)
			for i, id := range ids {
				if !validAPIID(id) || (i > 0 && id == ids[i-1]) {
					return false, errInvalidResponse
				}
			}
			observed = ids
		case "ipFilterList":
			var entries []string
			var s string
			if json.Unmarshal(got.Filters, &s) == nil {
				if s != "" {
					entries = strings.Split(s, ",")
				}
			} else if json.Unmarshal(got.Filters, &entries) != nil {
				return false, errInvalidResponse
			}
			normalized := []string{}
			for _, s := range entries {
				v, err := discoveryAddresses(s, false)
				if err != nil {
					return false, errInvalidResponse
				}
				normalized = append(normalized, strings.Split(v, ",")...)
			}
			sort.Strings(normalized)
			observed = normalized
		}
		a, _ := json.Marshal(value)
		b, _ := json.Marshal(observed)
		if !bytes.Equal(a, b) {
			return false, nil
		}
	}
	return true, nil
}

type discoveryExecutor struct {
	client  *client
	desired []discoveryIntent
}

func (e *discoveryExecutor) count() int { return len(e.desired) }
func (e *discoveryExecutor) observe(ctx context.Context) (map[string]discoveryMetadata, []map[string]any, error) {
	actual, err := e.client.discoveries(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(e.desired) == 0 {
		return actual, []map[string]any{}, nil
	}
	credentials, err := e.client.globalCredentials(ctx)
	if err != nil {
		return nil, nil, err
	}
	resolved := []map[string]any{}
	for _, d := range e.desired {
		fields, err := resolveDiscoveryCredentials(d, credentials)
		if err != nil {
			return nil, nil, err
		}
		resolved = append(resolved, fields)
	}
	return actual, resolved, nil
}
func discoveryCreateSelection(desired []discoveryIntent, actual map[string]discoveryMetadata, resolved []map[string]any) (int, error) {
	if len(desired) != len(resolved) {
		return -1, errConfigurationTransition
	}
	selected := -1
	for i, d := range desired {
		if d.fields["name"] != d.name || resolved[i]["name"] != d.name {
			return -1, errConfigurationTransition
		}
		if got, exists := actual[d.name]; exists {
			matches, err := discoveryMatches(resolved[i], got)
			if err != nil {
				return -1, err
			}
			if !matches {
				return -1, errors.New("discovery changes require explicit replacement; automatic rerun is not implemented")
			}
			if got.Condition != "Complete" || got.Status != "Inactive" {
				return -1, errors.New("discovery has not completed")
			}
		} else if selected < 0 {
			selected = i
		}
	}
	return selected, nil
}
func (e *discoveryExecutor) createPlan(selected int, resolved []map[string]any, selectCurrent func(map[string]discoveryMetadata, []map[string]any) (int, error), guard ...func(context.Context) error) (*configurationWrite, error) {
	if selected < 0 || selected >= len(e.desired) || len(guard) > 1 {
		return nil, errConfigurationTransition
	}
	captured, err := json.Marshal(resolved)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(resolved[selected])
	if err != nil {
		return nil, err
	}
	resource := e.desired[selected].resource()
	var used atomic.Bool
	return &configurationWrite{resource: resource, action: "Create", submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		actual, fresh, err := e.observe(ctx)
		if err != nil {
			return Task{}, err
		}
		raw, err := json.Marshal(fresh)
		if err != nil {
			return Task{}, err
		}
		if !bytes.Equal(captured, raw) {
			return Task{}, errConfigurationStale
		}
		selector := selectCurrent
		if selector == nil {
			selector = func(actual map[string]discoveryMetadata, resolved []map[string]any) (int, error) {
				return discoveryCreateSelection(e.desired, actual, resolved)
			}
		}
		current, err := selector(actual, fresh)
		if err != nil {
			return Task{}, err
		}
		if current != selected {
			return Task{}, errConfigurationStale
		}
		if len(guard) == 1 {
			if err := guard[0](ctx); err != nil {
				return Task{}, err
			}
		}

		return e.client.createDiscovery(ctx, body)
	}}, nil
}
func (e *discoveryExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, resolved, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	selected, err := discoveryCreateSelection(e.desired, actual, resolved)
	if err != nil {
		return nil, err
	}
	if selected < 0 {
		return nil, nil
	}
	return e.createPlan(selected, resolved, nil)
}
func (e *discoveryExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Create" || op.TargetID != "" || !validAPIID(op.TaskID) {
		return false, errConfigurationTransition
	}
	id, err := e.client.discoveryTaskResource(ctx, op.TaskID)
	if err != nil {
		return false, err
	}
	actual, resolved, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for i, d := range e.desired {
		if d.resource() != op.Resource {
			continue
		}
		got, exists := actual[d.name]
		if !exists {
			return false, nil
		}
		if got.ID != id {
			return false, errors.New("discovery task resource does not match observed job")
		}
		matches, err := discoveryMatches(resolved[i], got)
		return matches && got.Condition == "Complete" && got.Status == "Inactive", err
	}
	return false, errConfigurationTransition
}
func (e *discoveryExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	return e.reportWithOutcomes(ctx, nil)
}
func (e *discoveryExecutor) reportWithOutcomes(ctx context.Context, summary *discoveryOutcomeSummary) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, resolved, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	if summary != nil {
		*summary = discoveryOutcomeSummary{}
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for i, d := range e.desired {
		got, exists := actual[d.name]
		change := configapi.NetworkControllerDriftChangeAdd
		if summary != nil {
			if !exists {
				summary.MissingJobs++
			} else {
				rows, err := e.client.discoveryDevices(ctx, got.ID)
				if err != nil {
					return nil, err
				}
				summary.add(rows)
			}
		}
		if exists {
			matches, err := discoveryMatches(resolved[i], got)
			if err != nil {
				return nil, err
			}
			if matches && got.Condition == "Complete" && got.Status == "Inactive" {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "inventory", Resource: d.resource(), Path: "/discovery", Change: change})
	}
	return out, nil
}

// Correlate a discovery UUID with the exact retained create task, not a name.
// Callers must independently verify successful task timing/completion.
func (c *client) discoveryTaskResource(ctx context.Context, taskID string) (string, error) {
	if !validAPIID(taskID) {
		return "", errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+taskID, nil, 200)
	if err != nil {
		return "", err
	}
	return decodeDiscoveryTaskResource(raw, taskID)
}

func decodeDiscoveryTaskResource(raw []byte, taskID string) (string, error) {
	if !validAPIID(taskID) {
		return "", errConfigurationTransition
	}
	var task struct {
		Response struct {
			ID   string `json:"id"`
			Data string `json:"data"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &task) != nil || task.Response.ID != taskID {
		return "", errInvalidResponse
	}
	id := task.Response.Data
	var data struct {
		ID json.RawMessage `json:"discoveryId"`
	}
	if json.Unmarshal([]byte(id), &data) == nil && data.ID != nil {
		if json.Unmarshal(data.ID, &id) != nil {
			id = string(data.ID)
		}
	}
	if !validAPIID(id) {
		return "", errInvalidResponse
	}
	return id, nil
}

// Shared native create boundary for both legacy and receipt-backed discovery.
func (c *client) createDiscovery(ctx context.Context, body []byte) (Task, error) {
	raw, err := c.mutate(ctx, http.MethodPost, discoveryPath, body)
	if err != nil {
		return Task{}, err
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		return Task{}, errInvalidResponse
	}
	return Task{ID: receipt.Response.ID}, nil
}

func (c *client) deleteDiscovery(ctx context.Context, id string) (Task, error) {
	if !validAPIID(id) {
		return Task{}, errConfigurationTransition
	}
	raw, err := c.mutate(ctx, http.MethodDelete, discoveryPath+"/"+url.PathEscape(id), nil)
	if err != nil {
		return Task{}, err
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		return Task{}, errInvalidResponse
	}
	return Task{ID: receipt.Response.ID}, nil
}
