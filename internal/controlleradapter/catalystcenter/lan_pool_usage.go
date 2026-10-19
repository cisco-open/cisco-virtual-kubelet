// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
)

type lanPoolUsage struct {
	ReservationID, SiteID, ParentID, CIDR          string
	Total, Unassignable, Assigned, DefaultAssigned uint64
}

// This is a conservative remaining-count bound, not proof of a contiguous,
// allocatable range or allocator admission. Exclusions are never treated as free.
func (u *lanPoolUsage) remaining() uint64 {
	return u.Total - u.Unassignable - u.Assigned - u.DefaultAssigned
}

// Keep changing usage counters out of the common IPAM desired-state and
// dependency structs. Read the existing modern detail API and bind its exact
// reservation/site/global-parent/CIDR to the previously observed LAN pool.
func (c *client) lanPoolUsage(ctx context.Context, bound ipReservation) (*lanPoolUsage, error) {
	if !validAPIID(bound.ID) || !validAPIID(bound.SiteID) || bound.Type != "LAN" || bound.V4 == nil || bound.V4.PrefixLength == nil || !validAPIID(bound.V4.GlobalPoolID) {
		return nil, errInvalidResponse
	}
	var body struct {
		Response *struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Type   string `json:"poolType"`
			SiteID string `json:"siteId"`
			V4     *struct {
				Subnet       string  `json:"subnet"`
				Prefix       *int    `json:"prefixLength"`
				Parent       string  `json:"globalPoolId"`
				Total        *string `json:"totalAddresses"`
				Unassignable *string `json:"unassignableAddresses"`
				Assigned     *string `json:"assignedAddresses"`
				Default      *string `json:"defaultAssignedAddresses"`
			} `json:"ipV4AddressSpace"`
		} `json:"response"`
	}
	raw, err := c.get(ctx, sitePoolsPath+"/"+url.PathEscape(bound.ID), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &body) != nil || body.Response == nil || body.Response.V4 == nil || body.Response.V4.Prefix == nil {
		return nil, errInvalidResponse
	}
	got := body.Response
	space := got.V4
	if got.ID != bound.ID || got.Name != bound.Name || got.Type != bound.Type || got.SiteID != bound.SiteID || space.Subnet != bound.V4.Subnet || *space.Prefix != *bound.V4.PrefixLength || space.Parent != bound.V4.GlobalPoolID {
		return nil, errConfigurationStale
	}
	address, err := netip.ParseAddr(space.Subnet)
	if err != nil || !address.Is4() || *space.Prefix < 0 || *space.Prefix > 32 {
		return nil, errInvalidResponse
	}
	prefix := netip.PrefixFrom(address, *space.Prefix)
	if prefix.Masked() != prefix {
		return nil, errInvalidResponse
	}
	parse := func(raw *string) (uint64, error) {
		if raw == nil {
			return 0, errConfigurationBusy
		}
		n, err := strconv.ParseUint(*raw, 10, 64)
		if err != nil || strconv.FormatUint(n, 10) != *raw || n > uint64(1)<<32 {
			return 0, errInvalidResponse
		}
		return n, nil
	}
	total, err := parse(space.Total)
	if err != nil {
		return nil, err
	}
	unassignable, err := parse(space.Unassignable)
	if err != nil {
		return nil, err
	}
	assigned, err := parse(space.Assigned)
	if err != nil {
		return nil, err
	}
	defaults, err := parse(space.Default)
	if err != nil {
		return nil, err
	}
	if total > uint64(1)<<uint(32-*space.Prefix) || unassignable > total || assigned > total-unassignable || defaults > total-unassignable-assigned {
		return nil, errInvalidResponse
	}
	if total == 0 {
		return nil, errConfigurationBusy
	}
	return &lanPoolUsage{ReservationID: got.ID, SiteID: got.SiteID, ParentID: space.Parent, CIDR: prefix.String(), Total: total, Unassignable: unassignable, Assigned: assigned, DefaultAssigned: defaults}, nil
}
