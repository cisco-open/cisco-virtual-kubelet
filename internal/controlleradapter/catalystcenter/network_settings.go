// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type networkSetting struct {
	aaa            map[string]map[string]string
	aaaCredentials []configurationCredentialIntent
	kind           string
	// For DNS only, nil means omitted/null (unmanaged); an empty non-nil
	// slice explicitly requests an empty resolver list.
	servers                      []string
	domain                       string
	timezone, banner, bannerType string
	telemetry                    map[string]map[string]any
}
type networkBinding struct {
	path, kind string
	settings   []networkSetting
}
type networkObservation struct {
	aaa                          map[string]map[string]string
	inherited, unset             bool
	servers                      []string
	domain                       string
	timezone, banner, bannerType string
	telemetry                    map[string]map[string]any
}

func parseNetworkSettings(raw []byte) ([]networkBinding, error) {
	return parseNetworkSettingsWithAAA(raw, func(p map[string]any, _ int) ([]networkSetting, error) { return parseAAAProfile(p) })
}

func parseNetworkSettingsWithAAA(raw []byte, parseAAA func(map[string]any, int) ([]networkSetting, error)) ([]networkBinding, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 2 {
		return nil, errors.New("network report requires only sites and network_settings")
	}
	network, ok := root["network_settings"].(map[string]any)
	if !ok {
		return nil, errors.New("network settings profiles are required")
	}
	profiles := map[string][]networkSetting{}
	for category, value := range network {
		if category != "network" && category != "telemetry" && category != "aaa_servers" {
			return nil, errors.New("unsupported network settings profile category")
		}
		list, ok := value.([]any)
		if !ok || len(list) == 0 {
			return nil, errors.New("profiles must be a non-empty list")
		}
		for index, value := range list {
			p, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("profile must be an object")
			}
			name, ok := p["name"].(string)
			if !ok || strings.TrimSpace(name) == "" {
				return nil, errors.New("profile name is required")
			}
			key := category + "/" + name
			if _, exists := profiles[key]; exists {
				return nil, errors.New("duplicate profile name")
			}
			var settings []networkSetting
			if category == "network" {
				settings, err = parseNetworkProfile(p)
			} else if category == "aaa_servers" {
				settings, err = parseAAA(p, index)
			} else {
				settings, err = parseTelemetryProfile(p)
			}
			if err != nil {
				return nil, err
			}
			profiles[key] = settings
		}
	}
	sites, ok := root["sites"].(map[string]any)
	if !ok {
		return nil, errors.New("sites references are required")
	}
	bindings := []networkBinding{}
	seen := map[string]bool{}
	used := map[string]bool{}
	appendBinding := func(site map[string]any, kind string) error {
		path := "Global"
		for key := range site {
			if key != "name" && key != "parent_name" && key != "network_settings" {
				return errors.New("site references may contain only name, parent_name and network_settings")
			}
		}
		if kind != "global" {
			name, nok := site["name"].(string)
			parent, pok := site["parent_name"].(string)
			if !nok || !pok || name == "" || strings.Contains(name, "/") || !validSitePath(parent) {
				return errors.New("site reference requires explicit canonical name and parent_name")
			}
			path = parent + "/" + name
		} else if len(site) != 1 {
			return errors.New("global site reference may contain only network_settings")
		}
		if !validSitePath(path) || seen[path] {
			return errors.New("invalid or duplicate site reference")
		}
		seen[path] = true
		settings, ok := site["network_settings"].(map[string]any)
		if !ok || len(settings) == 0 {
			return errors.New("site profile references are required")
		}
		for category := range settings {
			if category != "network" && category != "telemetry" && category != "aaa_servers" {
				return errors.New("unsupported site profile reference")
			}
		}
		combined := []networkSetting{}
		for _, category := range []string{"network", "telemetry", "aaa_servers"} {
			value, present := settings[category]
			if !present {
				continue
			}
			name, ok := value.(string)
			if !ok {
				return errors.New("site profile reference must be a name")
			}
			key := category + "/" + name
			profile, ok := profiles[key]
			if !ok {
				return errors.New("site references an undefined profile")
			}
			used[key] = true
			combined = append(combined, profile...)
		}
		bindings = append(bindings, networkBinding{path: path, kind: kind, settings: combined})
		return nil
	}
	for key, value := range sites {
		if key == "global" {
			site, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("global site must be an object")
			}
			if err = appendBinding(site, "global"); err != nil {
				return nil, err
			}
			continue
		}
		kind := ""
		switch key {
		case "areas":
			kind = "area"
		case "buildings":
			kind = "building"
		case "floors":
			kind = "floor"
		default:
			return nil, errors.New("unsupported sites reference collection")
		}
		entries, ok := value.([]any)
		if !ok {
			return nil, errors.New("site references must be a list")
		}
		for _, entry := range entries {
			site, ok := entry.(map[string]any)
			if !ok {
				return nil, errors.New("site reference must be an object")
			}
			if err = appendBinding(site, kind); err != nil {
				return nil, err
			}
		}
	}
	if len(bindings) > 100 || len(bindings) == 0 && len(profiles) != 0 {
		return nil, errors.New("network reporting requires at most 100 site references and no unreferenced profiles")
	}
	if len(used) != len(profiles) {
		return nil, errors.New("unreferenced network profile cannot be evaluated")
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].path < bindings[j].path })
	return bindings, nil
}
func validNetworkName(s string) bool {
	if len(s) > 253 || s == "" || strings.TrimSpace(s) != s {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func parseServerList(v any, kind string) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, errors.New("server list must be explicit and non-null")
	}
	limit := 100 // Local reporting bound; not a claim about appliance write limits.
	if len(items) > limit {
		return nil, errors.New("server list exceeds supported API bound")
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		server, ok := item.(string)
		if !ok {
			return nil, errors.New("server address must be a string")
		}
		if ip, err := netip.ParseAddr(server); err == nil && ip.Zone() == "" {
			server = ip.String()
		} else if kind != "ntp" || !validNetworkName(server) {
			return nil, errors.New("invalid server address")
		} else {
			server = strings.ToLower(strings.TrimSuffix(server, "."))
		}
		if seen[server] {
			return nil, errors.New("duplicate server address")
		}
		seen[server] = true
		out = append(out, server)
	}
	// Preserve DNS resolver order; do not hide a primary/secondary change.
	if kind != "dns" {
		sort.Strings(out)
	}
	return out, nil
}
func (c *client) networkSettingResponse(ctx context.Context, siteID, kind string) (map[string]json.RawMessage, error) {
	// This value comes from inventory, never use it as an arbitrary URL path.
	if siteID == "" || strings.ContainsAny(siteID, "/%?#\\") || siteID == "." || siteID == ".." {
		return nil, errInvalidResponse
	}
	switch kind {
	case "ntp", "dns", "dhcp", "timezone", "banner", "telemetry", "aaa":
	default:
		return nil, errors.New("unsupported network setting")
	}
	apiKind := kind
	if kind == "timezone" {
		apiKind = "timeZone"
	}
	raw, err := c.get(ctx, sitesPath+"/"+url.PathEscape(siteID)+"/"+apiKind+"Settings", url.Values{"_inherited": {"false"}}, 200)
	if err != nil {
		return nil, err
	}
	var env struct {
		Response map[string]json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Response == nil {
		return nil, errInvalidResponse
	}
	return env.Response, nil
}

func (c *client) readNetworkSetting(ctx context.Context, siteID, kind string) (networkObservation, error) {
	out := networkObservation{}
	response, err := c.networkSettingResponse(ctx, siteID, kind)
	if err != nil {
		return out, err
	}
	apiKind := kind
	if kind == "timezone" {
		apiKind = "timeZone"
	}
	if kind == "aaa" {
		out.aaa, err = parseAAAObservation(response)
		return out, err
	}
	if kind == "telemetry" {
		out.telemetry, err = parseTelemetryObservation(response)
		return out, err
	}
	value, present := response[apiKind]
	if !present {
		return out, errInvalidResponse
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		out.inherited = true
		return out, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil || fields == nil {
		return out, errInvalidResponse
	}
	if len(fields) == 0 {
		out.unset = true
		return out, nil
	}
	// _inherited=false must not return effective inherited values as explicit.
	for _, key := range []string{"inheritedSiteId", "inheritedSiteName"} {
		if v, ok := fields[key]; ok {
			var identity string
			if json.Unmarshal(v, &identity) != nil || identity != "" {
				return out, errInvalidResponse
			}
		}
	}
	if kind == "timezone" {
		if !readSettingString(fields, "identifier", &out.timezone) || !validTimezoneIdentifier(out.timezone) {
			return out, errInvalidResponse
		}
		return out, nil
	}
	if kind == "banner" {
		if !readSettingString(fields, "type", &out.bannerType) {
			return out, errInvalidResponse
		}
		switch out.bannerType {
		case "Custom":
			if !readSettingString(fields, "message", &out.banner) || !validBanner(out.banner) {
				return out, errInvalidResponse
			}
		case "Builtin": // A built-in banner need not carry a custom message.
		default:
			return out, errInvalidResponse
		}
		return out, nil
	}
	serverKey := "servers"
	if kind == "dns" {
		serverKey = "dnsServers"
	}
	var servers any
	if kind == "dns" && (len(fields[serverKey]) == 0 || bytes.Equal(bytes.TrimSpace(fields[serverKey]), []byte("null"))) {
		servers = []any{}
	} else if json.Unmarshal(fields[serverKey], &servers) != nil {
		return out, errInvalidResponse
	}
	out.servers, err = parseServerList(servers, kind)
	if err != nil {
		return out, errInvalidResponse
	}
	if kind == "dns" {
		if json.Unmarshal(fields["domainName"], &out.domain) != nil || !validNetworkName(out.domain) {
			return out, errInvalidResponse
		}
	}
	return out, nil
}
func networkSettingDrift(path string, want networkSetting, got networkObservation) []configapi.NetworkControllerDriftEntry {
	add := func(field string) configapi.NetworkControllerDriftEntry {
		return configapi.NetworkControllerDriftEntry{Section: "network_settings", Resource: path, Path: "/" + field, Change: configapi.NetworkControllerDriftChangeUpdate}
	}
	if want.kind == "aaa" {
		return aaaDrift(path, want.aaa, got.aaa)
	}
	if want.kind == "telemetry" {
		return telemetryDrift(path, want.telemetry, got.telemetry)
	}
	// A native empty group is the actual readback of NTP/DHCP reset. It is
	// convergence only for an explicit empty desired list, never for nonempty
	// values or an arbitrary scalar/telemetry group. Parent lookup still obeys
	// the common resolver: unset is not guessed to mean inherited.
	if (want.kind == "ntp" || want.kind == "dhcp") && len(want.servers) == 0 && got.unset {
		return nil
	}
	if got.inherited || got.unset {
		return []configapi.NetworkControllerDriftEntry{add(want.kind)}
	}
	if want.kind == "timezone" {
		if got.unset || want.timezone != got.timezone {
			return []configapi.NetworkControllerDriftEntry{add("timezone")}
		}
		return nil
	}
	if want.kind == "banner" {
		if got.unset || want.bannerType != got.bannerType || want.banner != got.banner {
			return []configapi.NetworkControllerDriftEntry{add("banner")}
		}
		return nil
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	if (want.kind != "dns" || want.servers != nil) && !reflect.DeepEqual(want.servers, got.servers) && !(len(want.servers) == 0 && len(got.servers) == 0) {
		drift = append(drift, add(want.kind+"_servers"))
	}
	if want.kind == "dns" && want.domain != got.domain {
		drift = append(drift, add("domain_name"))
	}
	return drift
}
func (c *client) reportNetworkSettings(ctx context.Context, bindings []networkBinding) ([]configapi.NetworkControllerDriftEntry, error) {
	// The pinned module selects resources, not profile/site declarations. An
	// all-null profile has no native dependency and does not adopt or clear it.
	selected := make([]networkBinding, 0, len(bindings))
	for _, binding := range bindings {
		if len(binding.settings) > 0 {
			selected = append(selected, binding)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	bindings = selected
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, s := range sites {
		byPath[s.NameHierarchy] = s
	}
	// Validate every binding before any settings read. A missing site is not a
	// network-setting create operation and must not be reported as an empty object.
	for _, binding := range bindings {
		site, ok := byPath[binding.path]
		if !ok || site.Type != binding.kind {
			return nil, errors.New("network settings site reference is missing or incompatible")
		}
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, binding := range bindings {
		for _, want := range binding.settings {
			got, err := c.effectiveNetworkSetting(ctx, byPath, binding.path, want.kind)
			if err != nil {
				return nil, err
			}
			out = append(out, networkSettingDrift(binding.path, want, got)...)
		}
	}
	return out, nil
}

// Catalyst Center may collapse a PUT matching an ancestor into inheritance.
// The NetAsCode profile declares values, not a force-explicit-override flag.
// Resolve only fields which the explicit API marks inherited, walking the
// already validated parent-ID chain. Unset groups never become guessed values.
func (c *client) effectiveNetworkSetting(ctx context.Context, sites map[string]Site, path, kind string) (networkObservation, error) {
	return effectiveNetworkSettingWithReader(ctx, sites, path, kind, c.readNetworkSetting)
}

// Reuse the same inheritance resolver for an observation-local cached reader.
func effectiveNetworkSettingWithReader(ctx context.Context, sites map[string]Site, path, kind string, read func(context.Context, string, string) (networkObservation, error)) (networkObservation, error) {
	site, ok := sites[path]
	if !ok {
		return networkObservation{}, errInvalidResponse
	}
	got, err := read(ctx, site.ID, kind)
	if err != nil {
		return got, err
	}
	if kind == "aaa" {
		return got, nil
	} // AAA metadata retains its separate reporting contract.
	unresolved := func() bool {
		if kind != "telemetry" {
			return got.inherited
		}
		for _, group := range telemetryGroups {
			if got.telemetry[group] == nil {
				return true
			}
		}
		return false
	}
	for depth := 0; unresolved(); depth++ {
		if depth >= len(sites) {
			return networkObservation{}, errInvalidResponse
		}
		if site.Type == "global" {
			if kind == "telemetry" {
				for _, group := range telemetryGroups {
					if got.telemetry[group] == nil {
						got.telemetry[group] = map[string]any{}
					}
				}
			} else {
				got.inherited = false
				got.unset = true
			}
			break
		}
		parent, ok := sites[parentSitePath(site.NameHierarchy)]
		if !ok || parent.ID != site.ParentID {
			return networkObservation{}, errInvalidResponse
		}
		ancestor, err := read(ctx, parent.ID, kind)
		if err != nil {
			return networkObservation{}, err
		}
		if kind == "telemetry" {
			for _, group := range telemetryGroups {
				if got.telemetry[group] == nil {
					got.telemetry[group] = ancestor.telemetry[group]
				}
			}
		} else {
			got = ancestor
		}
		site = parent
	}
	return got, nil
}

// Validate representation, not membership in an appliance-version-specific timezone catalog.
func validTimezoneIdentifier(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" {
			return false
		}
		for _, c := range part {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '+') {
				return false
			}
		}
	}
	return true
}
func validBanner(value string) bool {
	return utf8.ValidString(value) && len(value) <= 65536 && !strings.ContainsRune(value, 0)
}
func readSettingString(fields map[string]json.RawMessage, key string, target *string) bool {
	value, ok := fields[key]
	return ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) && json.Unmarshal(value, target) == nil
}

func parseNetworkProfile(p map[string]any) ([]networkSetting, error) {
	for key := range p {
		switch key {
		case "name", "ntp_servers", "dhcp_servers", "dns_servers", "domain_name", "timezone", "banner":
		default:
			return nil, errors.New("unsupported network profile field")
		}
	}
	settings := []networkSetting{}
	for _, kind := range []string{"ntp", "dhcp"} {
		if v, exists := p[kind+"_servers"]; exists && v != nil {
			servers, e := parseServerList(v, kind)
			if e != nil {
				return nil, e
			}
			settings = append(settings, networkSetting{kind: kind, servers: servers})
		}
	}
	domain, domainSet := p["domain_name"]
	servers, dnsSet := p["dns_servers"]
	if domainSet && domain != nil {
		d, ok := domain.(string)
		if !ok || !validNetworkName(d) {
			return nil, errors.New("invalid DNS domain_name")
		}
		var v []string
		if dnsSet && servers != nil {
			var e error
			v, e = parseServerList(servers, "dns")
			if e != nil {
				return nil, e
			}
		}
		settings = append(settings, networkSetting{kind: "dns", domain: d, servers: v})
	}
	for _, kind := range []string{"timezone", "banner"} {
		if value, present := p[kind]; present && value != nil {
			text, ok := value.(string)
			if !ok || (kind == "timezone" && !validTimezoneIdentifier(text)) || (kind == "banner" && !validBanner(text)) {
				return nil, errors.New("invalid timezone or banner value")
			}
			setting := networkSetting{kind: kind}
			if kind == "timezone" {
				setting.timezone = text
			} else {
				setting.banner = text
				setting.bannerType = "Custom"
			}
			settings = append(settings, setting)
		}
	}
	return settings, nil
}
