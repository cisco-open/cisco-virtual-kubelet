// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const fabricVirtualNetworkGroupsPath = "/dna/intent/api/v1/virtual-network"

// This API identifies the VN context and associated group names. It does not
// expose ISE group UUIDs and must not be used as security-group ownership proof.
type fabricScalableGroupMembership struct {
	Network   string   `json:"virtualNetworkName"`
	ContextID string   `json:"virtualNetworkContextId"`
	Guest     bool     `json:"isGuestVirtualNetwork"`
	Groups    []string `json:"scalableGroupNames"`
	VPN       *string  `json:"vManageVpnId,omitempty"`
}

func (c *client) fabricScalableGroups(ctx context.Context, network string) (*fabricScalableGroupMembership, error) {
	if !validProfileName(network) {
		return nil, errInvalidResponse
	}
	raw, err := c.get(ctx, fabricVirtualNetworkGroupsPath, url.Values{"virtualNetworkName": {network}}, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var response struct {
		Network   string    `json:"virtualNetworkName"`
		ContextID string    `json:"virtualNetworkContextId"`
		Guest     *bool     `json:"isGuestVirtualNetwork"`
		Groups    *[]string `json:"scalableGroupNames"`
		VPN       *string   `json:"vManageVpnId"`
		Status    string    `json:"status"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Status != "success" || response.Network != network || !validAPIID(response.ContextID) || response.Guest == nil || response.Groups == nil || len(*response.Groups) > 10000 {
		return nil, errInvalidResponse
	}
	seen := map[string]bool{}
	for _, name := range *response.Groups {
		key := strings.ToLower(name)
		if !validProfileName(name) || seen[key] {
			return nil, errInvalidResponse
		}
		seen[key] = true
	}
	if response.VPN != nil && (*response.VPN != "" && !validProfileName(*response.VPN)) {
		return nil, errInvalidResponse
	}
	sort.Strings(*response.Groups)
	return &fabricScalableGroupMembership{Network: response.Network, ContextID: response.ContextID, Guest: *response.Guest, Groups: *response.Groups, VPN: response.VPN}, nil
}
