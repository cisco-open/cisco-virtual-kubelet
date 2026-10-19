// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

type lanSessionPoolIntent struct {
	Name string `json:"name"`
	Role string `json:"role"`
}
type lanDiscoveryIntent struct {
	Serial   string `json:"device_serial_number"`
	Hostname string `json:"device_host_name,omitempty"`
	Site     string `json:"device_site_name_hierarchy,omitempty"`
	IP       string `json:"device_management_ip_address,omitempty"`
}
type lanSessionIntent struct {
	Name            string                 `json:"name"`
	Status          string                 `json:"status"`
	Site            string                 `json:"discovered_device_site_name_hierarchy"`
	PrimaryIP       string                 `json:"primary_device_management_ip_address"`
	PeerIP          string                 `json:"secondary_device_management_ip_address,omitempty"`
	Ports           []string               `json:"primary_device_interface_names"`
	Pools           []lanSessionPoolIntent `json:"ip_pools"`
	Multicast       *bool                  `json:"multicast_enabled,omitempty"`
	Redistribute    *bool                  `json:"advertise_lan_automation_routes_into_bgp,omitempty"`
	Prefix          string                 `json:"host_name_prefix,omitempty"`
	Password        string                 `json:"isis_domain_password,omitempty"`
	PasswordVersion *int64                 `json:"isis_domain_password_version,omitempty"`
	Level           *int                   `json:"discovery_level,omitempty"`
	Timeout         *int                   `json:"discovery_timeout,omitempty"`
	Devices         []lanDiscoveryIntent   `json:"discovery_devices,omitempty"`
}

// Projected credential values are ephemeral and must never be formatted into
// logs/status or serialized as ownership receipts.
func (lanSessionIntent) String() string   { return "<LAN automation intent>" }
func (lanSessionIntent) GoString() string { return "<LAN automation intent>" }

func parseLANSessionIntents(raw []byte, projected bool) ([]lanSessionIntent, []configurationSecretDestination, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, nil, errInvalidResponse
	}
	section, ok := root["lan_automation"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, nil, errInvalidResponse
	}
	rows, ok := section["devices"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 100 {
		return nil, nil, errInvalidResponse
	}
	out := make([]lanSessionIntent, 0, len(rows))
	destinations := []configurationSecretDestination{}
	names, seeds, serials := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok || !projected && fields["isis_domain_password"] != nil {
			return nil, nil, errConfigurationSecret
		}
		body, err := json.Marshal(row)
		if err != nil {
			return nil, nil, errInvalidResponse
		}
		var want lanSessionIntent
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&want) != nil || validateLANSessionIntent(want, projected) != nil || names[want.Name] {
			return nil, nil, errInvalidResponse
		}
		names[want.Name] = true
		for _, ip := range []string{want.PrimaryIP, want.PeerIP} {
			if ip == "" {
				continue
			}
			if seeds[ip] {
				return nil, nil, errInvalidResponse
			}
			seeds[ip] = true
		}
		for _, d := range want.Devices {
			serial, _ := topologyidentity.CanonicalPhysicalIdentity(d.Serial)
			if serials[serial] {
				return nil, nil, errInvalidResponse
			}
			serials[serial] = true
		}
		if want.PasswordVersion != nil {
			destinations = append(destinations, configurationSecretDestination{"lan_automation", fmt.Sprintf("/devices/%d/isis_domain_password", i)})
		}
		// Preserve source order for Secret JSON pointers. Only unordered sets
		// within an entry are canonicalized here.
		sort.Strings(want.Ports)
		sort.Slice(want.Devices, func(i, j int) bool { return want.Devices[i].Serial < want.Devices[j].Serial })
		out = append(out, want)
	}
	return out, destinations, nil
}

func validateLANSessionIntent(w lanSessionIntent, projected bool) error {
	if !validProfileName(w.Name) || w.Status != "START" || !validSitePath(w.Site) || !validLANObservationIP(w.PrimaryIP) || (w.PeerIP != "" && (!validLANObservationIP(w.PeerIP) || w.PeerIP == w.PrimaryIP)) || len(w.Ports) == 0 || len(w.Ports) > 1000 || len(w.Pools) == 0 || len(w.Pools) > 2 || len(w.Devices) > 50 {
		return errInvalidResponse
	}
	if w.Level != nil && (*w.Level < 1 || *w.Level > 5) || w.Timeout != nil && (*w.Timeout < 20 || *w.Timeout > 10080) || w.Prefix != "" && !validProfileName(w.Prefix) {
		return errInvalidResponse
	}
	if w.PasswordVersion != nil && *w.PasswordVersion < 1 || !projected && w.Password != "" || projected && w.PasswordVersion != nil && w.Password == "" || len(w.Password) > 4096 || strings.ContainsAny(w.Password, "\x00\r\n") {
		return errConfigurationSecret
	}
	ports := map[string]bool{}
	for _, port := range w.Ports {
		key := strings.ToLower(port)
		if !validLANInterfaceName(port) || ports[key] {
			return errInvalidResponse
		}
		ports[key] = true
	}
	roles, pools := map[string]bool{}, map[string]bool{}
	for _, pool := range w.Pools {
		if !validPoolName(pool.Name) || (pool.Role != "PRINCIPAL_IP_ADDRESS_POOL" && pool.Role != "LINK_OVERLAPPING_IP_POOL") || pools[pool.Name] || roles[pool.Role] {
			return errInvalidResponse
		}
		pools[pool.Name], roles[pool.Role] = true, true
	}
	if !roles["PRINCIPAL_IP_ADDRESS_POOL"] {
		return errInvalidResponse
	}
	serials, addresses := map[string]bool{}, map[string]bool{w.PrimaryIP: true}
	if w.PeerIP != "" {
		addresses[w.PeerIP] = true
	}
	for _, device := range w.Devices {
		serial, err := topologyidentity.CanonicalPhysicalIdentity(device.Serial)
		if err != nil || serials[serial] || device.Hostname != "" && !validProfileName(device.Hostname) || device.Site != "" && (!validSitePath(device.Site) || device.Site != w.Site && !strings.HasPrefix(device.Site, w.Site+"/")) || device.IP != "" && (!validLANObservationIP(device.IP) || addresses[device.IP]) {
			return errInvalidResponse
		}
		serials[serial] = true
		if device.IP != "" {
			addresses[device.IP] = true
		}
	}
	return nil
}

// Return an ephemeral one-session POST body, matching the pinned provider's
// array shape and native field spelling. No action/version metadata is sent.
func lanSessionStartPayload(w lanSessionIntent) ([]byte, error) {
	if err := validateLANSessionIntent(w, true); err != nil {
		return nil, err
	}
	level := 2
	if w.Level != nil {
		level = *w.Level
	}
	body := map[string]any{"discoveredDeviceSiteNameHierarchy": w.Site, "primaryDeviceManagmentIPAddress": w.PrimaryIP, "primaryDeviceInterfaceNames": w.Ports, "discoveryLevel": level}
	if w.PeerIP != "" {
		body["peerDeviceManagmentIPAddress"] = w.PeerIP
	}
	pools := []lanSessionPool{}
	for _, pool := range w.Pools {
		role := "MAIN_POOL"
		if pool.Role == "LINK_OVERLAPPING_IP_POOL" {
			role = "PHYSICAL_LINK_POOL"
		}
		pools = append(pools, lanSessionPool{pool.Name, role})
	}
	body["ipPools"] = pools
	if w.Multicast != nil {
		body["multicastEnabled"] = *w.Multicast
	}
	if w.Redistribute != nil {
		body["redistributeIsisToBgp"] = *w.Redistribute
	}
	if w.Timeout != nil {
		body["discoveryTimeout"] = *w.Timeout
	}
	if w.Prefix != "" {
		body["hostNamePrefix"] = w.Prefix
	}
	if w.Password != "" {
		body["isisDomainPwd"] = w.Password
	}
	if w.Devices != nil {
		devices := []map[string]string{}
		for _, d := range w.Devices {
			device := map[string]string{"deviceSerialNumber": d.Serial}
			if d.Hostname != "" {
				device["deviceHostName"] = d.Hostname
			}
			if d.Site != "" {
				device["deviceSiteNameHierarchy"] = d.Site
			}
			if d.IP != "" {
				device["deviceManagementIPAddress"] = d.IP
			}
			devices = append(devices, device)
		}
		body["discoveryDevices"] = devices
	}
	return json.Marshal([]any{body})
}
