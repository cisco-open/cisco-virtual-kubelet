// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const lanSessionStatusPath = "/dna/intent/api/v1/lan-automation/status"

type lanSessionPool struct {
	Name string `json:"ipPoolName"`
	Role string `json:"ipPoolRole"`
}

type lanDiscoveredDevice struct {
	Name      string   `json:"name"`
	Serial    string   `json:"serialNumber"`
	State     string   `json:"state"`
	Addresses []string `json:"ipAddressInUseList"`
}

type lanSessionDiscoveryDevice struct {
	Serial     string `json:"deviceSerialNumber,omitempty"`
	Name       string `json:"deviceHostName,omitempty"`
	IP         string `json:"deviceManagementIPAddress"`
	SiteID     string `json:"deviceSiteId,omitempty"`
	Site       string `json:"deviceSiteNameHierarchy,omitempty"`
	Discovered *bool  `json:"isDeviceDiscovered,omitempty"`
	Allocated  *bool  `json:"isIPAllocated,omitempty"`
	Assigned   *bool  `json:"isIPAssigned,omitempty"`
	PnPID      string `json:"pnpDeviceId,omitempty"`
	unmapped   bool
}

func (d *lanSessionDiscoveryDevice) UnmarshalJSON(raw []byte) error {
	type plain lanSessionDiscoveryDevice
	var decoded plain
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &decoded) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errInvalidResponse
	}
	*d = lanSessionDiscoveryDevice(decoded)
	known := map[string]bool{"deviceSerialNumber": true, "deviceHostName": true, "deviceManagementIPAddress": true, "deviceSiteId": true, "deviceSiteNameHierarchy": true, "isDeviceDiscovered": true, "isIPAllocated": true, "isIPAssigned": true, "pnpDeviceId": true}
	for key := range fields {
		if !known[key] {
			d.unmapped = true
		}
	}
	return nil
}

// Native observation, not an owned execution receipt. Neither STOP nor text in
// Status establishes successful completion or permission to mutate a link.
type lanSession struct {
	ID               string                       `json:"id"`
	Site             string                       `json:"discoveredDeviceSiteNameHierarchy"`
	PrimaryIP        string                       `json:"primaryDeviceManagmentIPAddress"`
	PeerIP           string                       `json:"peerDeviceManagmentIPAddress"`
	Ports            []string                     `json:"primaryDeviceInterfaceNames"`
	Pools            []lanSessionPool             `json:"ipPools"`
	Action           string                       `json:"action"`
	Status           string                       `json:"status"`
	Created          string                       `json:"creationTime"`
	Discovered       []lanDiscoveredDevice        `json:"discoveredDeviceList"`
	Multicast        *bool                        `json:"multicastEnabled"`
	Redistribute     *bool                        `json:"redistributeIsisToBgp"`
	Level            *int                         `json:"discoveryLevel"`
	Timeout          *int                         `json:"discoveryTimeout"`
	DiscoveryDevices *[]lanSessionDiscoveryDevice `json:"discoveryDevices,omitempty"`
	unmapped         bool
}

func (s *lanSession) UnmarshalJSON(raw []byte) error {
	type plain lanSession
	var decoded plain
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &decoded) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errInvalidResponse
	}
	*s = lanSession(decoded)
	known := map[string]bool{"id": true, "discoveredDeviceSiteNameHierarchy": true, "primaryDeviceManagmentIPAddress": true, "peerDeviceManagmentIPAddress": true, "primaryDeviceInterfaceNames": true, "ipPools": true, "action": true, "status": true, "creationTime": true, "discoveredDeviceList": true, "multicastEnabled": true, "redistributeIsisToBgp": true, "discoveryLevel": true, "discoveryTimeout": true, "discoveryDevices": true, "hostNamePrefix": true, "hostNameFileId": true}
	for key := range fields {
		if !known[key] {
			s.unmapped = true
		}
	}
	return nil
}

func (c *client) lanSessions(ctx context.Context) ([]lanSession, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryPagesWithGet(ctx, get, lanSessionStatusPath, nil, func(s lanSession) string { return s.ID }, 10)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if err := normalizeLANSession(&rows[i]); err != nil {
			return nil, err
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func validLANObservationText(s string) bool {
	return s != "" && len(s) <= 4096 && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

func validLANObservationIP(s string) bool {
	ip, err := netip.ParseAddr(s)
	return err == nil && ip.Zone() == "" && !ip.Is4In6() && ip.IsGlobalUnicast() && ip.String() == s
}

func normalizeLANSession(s *lanSession) error {
	if s == nil || !validAPIID(s.ID) || !validSitePath(s.Site) || !validLANObservationIP(s.PrimaryIP) || (s.PeerIP != "" && (!validLANObservationIP(s.PeerIP) || s.PeerIP == s.PrimaryIP)) || !validLANObservationText(s.Action) || !validLANObservationText(s.Status) || !validLANObservationText(s.Created) || s.Ports == nil || len(s.Ports) > 1000 || s.Pools == nil || len(s.Pools) > 1000 || len(s.Discovered) > 1000 {
		return errInvalidResponse
	}
	ports := map[string]bool{}
	for _, port := range s.Ports {
		key := strings.ToLower(port)
		if !validLANInterfaceName(port) || ports[key] {
			return errInvalidResponse
		}
		ports[key] = true
	}
	poolNames, poolRoles := map[string]bool{}, map[string]bool{}
	for _, pool := range s.Pools {
		if !validPoolName(pool.Name) || !validLANObservationText(pool.Role) || poolNames[pool.Name] || poolRoles[pool.Role] {
			return errInvalidResponse
		}
		poolNames[pool.Name], poolRoles[pool.Role] = true, true
	}
	serials := map[string]bool{}
	for i := range s.Discovered {
		d := &s.Discovered[i]
		serial, err := topologyidentity.CanonicalPhysicalIdentity(d.Serial)
		if err != nil || serials[serial] || !validLANObservationText(d.Name) || !validLANObservationText(d.State) || len(d.Addresses) > 1000 {
			return errInvalidResponse
		}
		serials[serial] = true
		d.Serial = serial
		addresses := map[string]bool{}
		for _, ip := range d.Addresses {
			if !validLANObservationIP(ip) || addresses[ip] {
				return errInvalidResponse
			}
			addresses[ip] = true
		}
		sort.Strings(d.Addresses)
	}
	if s.DiscoveryDevices != nil {
		if len(*s.DiscoveryDevices) > 50 {
			return errInvalidResponse
		}
		addresses, serials, pnp := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for i := range *s.DiscoveryDevices {
			d := &(*s.DiscoveryDevices)[i]
			if d.IP != "" && (!validLANObservationIP(d.IP) || addresses[d.IP]) || d.Site != "" && !validSitePath(d.Site) || d.SiteID != "" && !validAPIID(d.SiteID) || d.Name != "" && !validLANObservationText(d.Name) || d.PnPID != "" && (!validAPIID(d.PnPID) || pnp[d.PnPID]) {
				return errInvalidResponse
			}
			if d.IP != "" {
				addresses[d.IP] = true
			}
			if d.PnPID != "" {
				pnp[d.PnPID] = true
			}
			if d.Serial != "" {
				serial, err := topologyidentity.CanonicalPhysicalIdentity(d.Serial)
				if err != nil || serials[serial] {
					return errInvalidResponse
				}
				d.Serial = serial
				serials[serial] = true
			}
		}
		sort.Slice(*s.DiscoveryDevices, func(i, j int) bool {
			a, b := (*s.DiscoveryDevices)[i], (*s.DiscoveryDevices)[j]
			if a.IP != b.IP {
				return a.IP < b.IP
			}
			return a.Serial < b.Serial
		})
	}
	sort.Strings(s.Ports)
	sort.Slice(s.Pools, func(i, j int) bool { return s.Pools[i].Name < s.Pools[j].Name })
	sort.Slice(s.Discovered, func(i, j int) bool { return s.Discovered[i].Serial < s.Discovered[j].Serial })
	return nil
}
