// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"reflect"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const globalPoolsPath = "/dna/intent/api/v1/ipam/globalIpAddressPools"
const sitePoolsPath = "/dna/intent/api/v1/ipam/siteIpAddressPools"

type poolAddressSpace struct {
	Subnet       string    `json:"subnet"`
	PrefixLength *int      `json:"prefixLength"`
	Gateway      *string   `json:"gatewayIpAddress"`
	DHCP         *[]string `json:"dhcpServers"`
	DNS          *[]string `json:"dnsServers"`
	GlobalPoolID string    `json:"globalPoolId"`
	SLAAC        *bool     `json:"slaacSupport"`
}
type ipPool struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Type  string            `json:"poolType"`
	Space *poolAddressSpace `json:"addressSpace"`
}
type ipReservation struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Type   string            `json:"poolType"`
	SiteID string            `json:"siteId"`
	V4     *poolAddressSpace `json:"ipV4AddressSpace"`
	V6     *poolAddressSpace `json:"ipV6AddressSpace"`
}
type desiredReservation struct {
	ipReservation
	site     string
	siteKind string
}
type networkReportIntent struct {
	credentials  []globalCredentialIntent
	assignments  []siteCredentialIntent
	bindings     []networkBinding
	pools        []ipPool
	reservations []desiredReservation
}

func validPoolName(name string) bool {
	if name == "" || len(name) > 256 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '/') {
			return false
		}
	}
	return true
}
func poolPrefix(space *poolAddressSpace) (netip.Prefix, error) {
	if space == nil || space.PrefixLength == nil {
		return netip.Prefix{}, errInvalidResponse
	}
	addr, err := netip.ParseAddr(space.Subnet)
	if err != nil || addr.Zone() != "" || addr.Is4In6() {
		return netip.Prefix{}, errInvalidResponse
	}
	prefix, err := addr.Prefix(*space.PrefixLength)
	if err != nil || prefix.Masked().Addr() != addr {
		return netip.Prefix{}, errInvalidResponse
	}
	return prefix, nil
}
func normalizePoolSpace(space *poolAddressSpace) error {
	prefix, err := poolPrefix(space)
	if err != nil {
		return err
	}
	space.Subnet = prefix.Addr().String()
	if space.Gateway != nil && *space.Gateway != "" {
		addr, err := netip.ParseAddr(*space.Gateway)
		if err != nil || addr.Zone() != "" || !prefix.Contains(addr) {
			return errInvalidResponse
		}
		s := addr.String()
		space.Gateway = &s
	}
	for _, list := range []**[]string{&space.DHCP, &space.DNS} {
		if *list != nil {
			values := make([]any, 0, len(**list))
			for _, s := range **list {
				values = append(values, s)
			}
			kind := "dhcp"
			if list == &space.DNS {
				kind = "dns"
			} // DNS order is retained.
			normalized, err := parseServerList(values, kind)
			if err != nil {
				return errInvalidResponse
			}
			*list = &normalized
		}
	}
	return nil
}
func parsePoolSpace(object map[string]any, subnet string, bits int) (*poolAddressSpace, error) {
	space := &poolAddressSpace{Subnet: subnet, PrefixLength: &bits}
	if raw, present := object["gateway"]; present {
		v, ok := raw.(string)
		if !ok {
			return nil, errors.New("invalid pool gateway")
		}
		space.Gateway = &v
	}
	for _, field := range []string{"dhcp_servers", "dns_servers"} {
		if raw, present := object[field]; present {
			kind := "dhcp"
			if field == "dns_servers" {
				kind = "dns"
			}
			v, err := parseServerList(raw, kind)
			if err != nil {
				return nil, err
			}
			if field == "dns_servers" {
				space.DNS = &v
			} else {
				space.DHCP = &v
			}
		}
	}
	if err := normalizePoolSpace(space); err != nil {
		return nil, errors.New("invalid pool address space")
	}
	return space, nil
}
func parseIPPoolModels(raw any) ([]ipPool, map[string]*desiredReservation, error) {
	entries, ok := raw.([]any)
	if !ok || len(entries) > 1000 {
		return nil, nil, errors.New("ip_pools must contain at most 1000 pools")
	}
	pools := []ipPool{}
	seen := map[string]bool{}
	reservations := map[string]*desiredReservation{}
	for _, entry := range entries {
		object, ok := entry.(map[string]any)
		if !ok {
			return nil, nil, errors.New("invalid pool object")
		}
		for key := range object {
			switch key {
			case "name", "type", "ip_address_space", "ip_pool_cidr", "gateway", "dhcp_servers", "dns_servers", "ip_pools_reservations":
			default:
				return nil, nil, errors.New("unsupported pool field")
			}
		}
		name, _ := object["name"].(string)
		kind, _ := object["type"].(string)
		family, _ := object["ip_address_space"].(string)
		cidr, _ := object["ip_pool_cidr"].(string)
		prefix, err := netip.ParsePrefix(cidr)
		if !validPoolName(name) || seen[name] || (kind != "Generic" && kind != "Tunnel") || err != nil || prefix != prefix.Masked() || (family != "IPv4" && family != "IPv6") || (prefix.Addr().Is4() != (family == "IPv4")) {
			return nil, nil, errors.New("invalid or duplicate pool identity, type or CIDR")
		}
		seen[name] = true
		space, err := parsePoolSpace(object, prefix.Addr().String(), prefix.Bits())
		if err != nil {
			return nil, nil, err
		}
		pools = append(pools, ipPool{Name: name, Type: kind, Space: space})
		if raw, present := object["ip_pools_reservations"]; present {
			list, ok := raw.([]any)
			if !ok {
				return nil, nil, errors.New("invalid reservation list")
			}
			for _, value := range list {
				r, ok := value.(map[string]any)
				if !ok {
					return nil, nil, errors.New("invalid reservation")
				}
				for key := range r {
					switch key {
					case "name", "type", "subnet", "prefix_length", "gateway", "dhcp_servers", "dns_servers":
					default:
						return nil, nil, errors.New("unsupported reservation field")
					}
				}
				n, _ := r["name"].(string)
				typ, _ := r["type"].(string)
				subnet, _ := r["subnet"].(string)
				bits, ok := r["prefix_length"].(int)
				if !validPoolName(n) || !ok || !validReservationType(typ) || kind == "Tunnel" {
					return nil, nil, errors.New("invalid reservation identity or type")
				}
				child, err := parsePoolSpace(r, subnet, bits)
				if err != nil {
					return nil, nil, err
				}
				childPrefix, _ := poolPrefix(child)
				if childPrefix.Addr().Is4() != prefix.Addr().Is4() || childPrefix.Bits() < prefix.Bits() || !prefix.Contains(childPrefix.Addr()) {
					return nil, nil, errors.New("reservation lies outside its parent pool")
				}
				// Until inventory resolution this reference is the upstream pool name.
				child.GlobalPoolID = name
				reservation := reservations[n]
				if reservation == nil {
					reservation = &desiredReservation{ipReservation: ipReservation{Name: n, Type: typ}}
					reservations[n] = reservation
				}
				if reservation.Type != typ {
					return nil, nil, errors.New("dual-stack reservation types differ")
				}
				if family == "IPv4" {
					if reservation.V4 != nil {
						return nil, nil, errors.New("duplicate IPv4 reservation")
					}
					reservation.V4 = child
				} else {
					if reservation.V6 != nil {
						return nil, nil, errors.New("duplicate IPv6 reservation")
					}
					if bits == 64 {
						v := true
						child.SLAAC = &v
					}
					reservation.V6 = child
				}
			}
		}
	}
	for _, r := range reservations {
		if r.V4 == nil {
			return nil, nil, errors.New("upstream reservations require IPv4; IPv6 augments the matching IPv4 reservation")
		}
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	return pools, reservations, nil
}
func validReservationType(s string) bool {
	switch s {
	case "Generic", "LAN", "Management", "Service", "WAN":
		return true
	}
	return false
}

// Extract pool intent while preserving the existing strict named-profile parser.
func parseNetworkReportIntent(raw []byte) (networkReportIntent, error) {
	return parseNetworkIntentWithSettings(raw, parseNetworkSettings)
}

func parseNetworkIntentWithSettings(raw []byte, parseSettings func([]byte) ([]networkBinding, error)) (networkReportIntent, error) {
	out := networkReportIntent{}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return out, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return out, errors.New("invalid network intent")
	}
	for key := range root {
		if key != "sites" && key != "network_settings" {
			return out, errors.New("unsupported network report section")
		}
	}
	network, ok := root["network_settings"].(map[string]any)
	if !ok {
		return out, errors.New("network settings are required")
	}
	if value, present := network["device_credentials"]; present {
		projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": value}}})
		out.credentials, err = parseGlobalCredentials(projection, false)
		if err != nil {
			return out, err
		}
		delete(network, "device_credentials")
	}
	reservations := map[string]*desiredReservation{}
	// The pinned module uses try(network_settings.ip_pools, []). An empty
	// settings object therefore selects zero allocations, just like ip_pools: [].
	// Keep this distinct from null or a malformed/unknown setting.
	emptyPoolSelection := len(network) == 0
	if value, present := network["ip_pools"]; present {
		if entries, ok := value.([]any); ok && len(entries) == 0 {
			emptyPoolSelection = true
		}
		out.pools, reservations, err = parseIPPoolModels(value)
		if err != nil {
			return out, err
		}
		delete(network, "ip_pools")
	}
	profileSites := map[string]any{}
	if value, present := root["sites"]; present {
		sites, ok := value.(map[string]any)
		if !ok {
			return out, errors.New("sites must be an object")
		}
		seen := map[string]bool{}
		for collection, value := range sites {
			kind := ""
			switch collection {
			case "areas":
				kind = "area"
			case "buildings":
				kind = "building"
			case "floors":
				kind = "floor"
			case "global":
				profileSites[collection] = value
				continue
			default:
				return out, errors.New("unknown sites collection")
			}
			entries, ok := value.([]any)
			if !ok {
				return out, errors.New("sites collection must be a list")
			}
			filtered := []any{}
			for _, entry := range entries {
				site, ok := entry.(map[string]any)
				if !ok {
					return out, errors.New("invalid site reference")
				}
				for key := range site {
					if key != "name" && key != "parent_name" && key != "ip_pools_reservations" && key != "network_settings" && credentialSlot(key) == "" {
						return out, errors.New("unsupported site reference field")
					}
				}
				name, _ := site["name"].(string)
				parent, _ := site["parent_name"].(string)
				path := parent + "/" + name
				siteKind := kind
				_, hasParent := site["parent_name"]
				if kind == "area" && name == "Global" && !hasParent {
					// The pinned module's sites_to_creds_map uses area.name when
					// parent_name is absent. Global is an existing root, not an area
					// to create. Other parentless paths remain unresolved.
					path, siteKind = "Global", "global"
					if _, present := site["ip_pools_reservations"]; present {
						return out, errors.New("Global cannot receive site pool reservations")
					}
				} else if name == "" || strings.Contains(name, "/") || !validSitePath(parent) {
					return out, errors.New("invalid site reference")
				}
				if !validSitePath(path) || seen[path] {
					return out, errors.New("invalid or duplicate site reference")
				}
				seen[path] = true
				for _, credentialKind := range globalCredentialKinds {
					value, present := site[credentialKind.model]
					if !present {
						continue
					}
					assignment := siteCredentialIntent{site: path, siteKind: siteKind, kind: credentialKind.model}
					if value != nil {
						assignment.name, err = globalCredentialText(value)
						if err != nil {
							return out, err
						}
					}
					if len(assignment.resource()) > 512 {
						return out, errors.New("credential assignment identity exceeds journal bound")
					}
					out.assignments = append(out.assignments, assignment)
					delete(site, credentialKind.model)
				}
				if value, present := site["ip_pools_reservations"]; present {
					list, ok := value.([]any)
					if !ok {
						return out, errors.New("invalid site reservation references")
					}
					for _, value := range list {
						n, ok := value.(string)
						r := reservations[n]
						if !ok || r == nil || r.site != "" {
							return out, errors.New("undefined or multiply assigned reservation")
						}
						r.site = path
						r.siteKind = kind
					}
					delete(site, "ip_pools_reservations")
				}
				if value, present := site["network_settings"]; present {
					if siteKind == "global" {
						if _, duplicate := sites["global"]; duplicate {
							return out, errors.New("duplicate Global profile binding")
						}
						profileSites["global"] = map[string]any{"network_settings": value}
					} else {
						filtered = append(filtered, site)
					}
				}
			}
			if len(filtered) > 0 {
				profileSites[collection] = filtered
			}
		}
	}
	for _, r := range reservations {
		if r.site == "" || len(r.site+"/"+r.Name) > 512 {
			return out, errors.New("reservation has no site reference")
		}
		out.reservations = append(out.reservations, *r)
	}
	sort.Slice(out.reservations, func(i, j int) bool { return out.reservations[i].Name < out.reservations[j].Name })
	if len(network) > 0 {
		normalized, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": network, "sites": profileSites}})
		out.bindings, err = parseSettings(normalized)
		if err != nil {
			return out, err
		}
	} else if len(profileSites) > 0 {
		return out, errors.New("site profiles have no definitions")
	}
	if len(out.pools) == 0 && len(out.bindings) == 0 && len(out.credentials) == 0 && len(out.assignments) == 0 && !emptyPoolSelection {
		return out, errors.New("network report is empty")
	}
	if len(out.assignments) > 600 {
		return out, errors.New("too many credential assignments")
	}
	sort.Slice(out.assignments, func(i, j int) bool { return out.assignments[i].resource() < out.assignments[j].resource() })
	return out, nil
}

func (c *client) listIPPools(ctx context.Context) ([]ipPool, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	pools, err := listInventoryQueryWithGet(ctx, get, globalPoolsPath, url.Values{}, func(p ipPool) string { return p.ID })
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range pools {
		p := &pools[i]
		if !validPoolName(p.Name) || seen[p.Name] || (p.Type != "Generic" && p.Type != "Tunnel") || normalizePoolSpace(p.Space) != nil {
			return nil, errInvalidResponse
		}
		seen[p.Name] = true
	}
	return pools, nil
}
func (c *client) listIPReservations(ctx context.Context) ([]ipReservation, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	reservations, err := listInventoryQueryWithGet(ctx, get, sitePoolsPath, url.Values{}, func(p ipReservation) string { return p.ID })
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range reservations {
		r := &reservations[i]
		key := r.SiteID + "/" + r.Name
		if !validPoolName(r.Name) || r.SiteID == "" || seen[key] || !validReservationType(r.Type) || r.V4 == nil {
			return nil, errInvalidResponse
		}
		seen[key] = true
		for j, s := range []*poolAddressSpace{r.V4, r.V6} {
			if s == nil {
				continue
			}
			if normalizePoolSpace(s) != nil || s.GlobalPoolID == "" {
				return nil, errInvalidResponse
			}
			p, _ := poolPrefix(s)
			if p.Addr().Is4() != (j == 0) {
				return nil, errInvalidResponse
			}
		}
	}
	return reservations, nil
}
func poolSpaceDiff(want, got *poolAddressSpace) ([]string, error) {
	if got == nil {
		return []string{"address_space"}, nil
	}
	fields := []string{}
	if want.Subnet != got.Subnet || *want.PrefixLength != *got.PrefixLength {
		fields = append(fields, "cidr")
	}
	if want.Gateway != nil {
		if got.Gateway == nil {
			return nil, errInvalidResponse
		}
		if *want.Gateway != *got.Gateway {
			fields = append(fields, "gateway")
		}
	}
	for _, v := range []struct {
		name      string
		want, got *[]string
	}{{"dhcp_servers", want.DHCP, got.DHCP}, {"dns_servers", want.DNS, got.DNS}} {
		if v.want != nil {
			if v.got == nil {
				return nil, errInvalidResponse
			}
			if !reflect.DeepEqual(*v.want, *v.got) {
				fields = append(fields, v.name)
			}
		}
	}
	if want.SLAAC != nil {
		if got.SLAAC == nil {
			return nil, errInvalidResponse
		}
		if *want.SLAAC != *got.SLAAC {
			fields = append(fields, "slaac_support")
		}
	}
	return fields, nil
}
func (c *client) reportIPAM(ctx context.Context, intent networkReportIntent) ([]configapi.NetworkControllerDriftEntry, error) {
	observed, err := c.listIPPools(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]ipPool{}
	byID := map[string]ipPool{}
	for _, p := range observed {
		byName[p.Name] = p
		byID[p.ID] = p
	}
	out := []configapi.NetworkControllerDriftEntry{}
	add := func(resource, path string, change configapi.NetworkControllerDriftChange) {
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "network_settings", Resource: resource, Path: path, Change: change})
	}
	for _, want := range intent.pools {
		got, ok := byName[want.Name]
		if !ok {
			add(want.Name, "/ip_pools", configapi.NetworkControllerDriftChangeAdd)
			continue
		}
		if want.Type != got.Type {
			add(want.Name, "/ip_pools/type", configapi.NetworkControllerDriftChangeUpdate)
		}
		fields, err := poolSpaceDiff(want.Space, got.Space)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			add(want.Name, "/ip_pools/"+field, configapi.NetworkControllerDriftChangeUpdate)
		}
	}
	if len(intent.reservations) == 0 {
		return out, nil
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	siteIDs := map[string]Site{}
	for _, s := range sites {
		siteIDs[s.NameHierarchy] = s
	}
	reservations, err := c.listIPReservations(ctx)
	if err != nil {
		return nil, err
	}
	bySite := map[string]ipReservation{}
	for _, r := range reservations {
		for _, s := range []*poolAddressSpace{r.V4, r.V6} {
			if s == nil {
				continue
			}
			parent, ok := byID[s.GlobalPoolID]
			if !ok || parent.Type == "Tunnel" {
				return nil, errInvalidResponse
			}
			p, _ := poolPrefix(parent.Space)
			child, _ := poolPrefix(s)
			if child.Bits() < p.Bits() || !p.Contains(child.Addr()) {
				return nil, errInvalidResponse
			}
		}
		bySite[r.SiteID+"/"+r.Name] = r
	}
	for _, want := range intent.reservations {
		site, ok := siteIDs[want.site]
		if !ok || site.Type != want.siteKind {
			return nil, errors.New("reservation site is missing or incompatible")
		}
		resource := want.site + "/" + want.Name
		if len(resource) > 512 {
			return nil, errors.New("reservation identity exceeds status bound")
		}
		got, ok := bySite[site.ID+"/"+want.Name]
		if !ok {
			add(resource, "/ip_pools_reservations", configapi.NetworkControllerDriftChangeAdd)
			continue
		}
		if got.Type != want.Type {
			add(resource, "/ip_pools_reservations/type", configapi.NetworkControllerDriftChangeUpdate)
		}
		for i, w := range []*poolAddressSpace{want.V4, want.V6} {
			if w == nil {
				continue
			}
			g := got.V4
			family := "ipv4"
			if i == 1 {
				g = got.V6
				family = "ipv6"
			}
			if g != nil {
				parent, ok := byName[w.GlobalPoolID]
				if !ok || parent.ID != g.GlobalPoolID {
					add(resource, "/ip_pools_reservations/"+family+"/parent", configapi.NetworkControllerDriftChangeUpdate)
				}
			}
			fields, err := poolSpaceDiff(w, g)
			if err != nil {
				return nil, err
			}
			for _, field := range fields {
				add(resource, "/ip_pools_reservations/"+family+"/"+field, configapi.NetworkControllerDriftChangeUpdate)
			}
		}
	}
	return out, nil
}
