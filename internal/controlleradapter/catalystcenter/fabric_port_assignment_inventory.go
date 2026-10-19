// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode"
)

const fabricPortAssignmentsPath = "/dna/intent/api/v1/sda/portAssignments"

type fabricPortAssignment struct {
	ID             string  `json:"id"`
	FabricID       string  `json:"fabricId"`
	DeviceID       string  `json:"networkDeviceId"`
	Interface      string  `json:"interfaceName"`
	Type           string  `json:"connectedDeviceType"`
	DataVLAN       *string `json:"dataVlanName"`
	VoiceVLAN      *string `json:"voiceVlanName"`
	SecurityGroup  *string `json:"securityGroupName"`
	Authentication *string `json:"authenticateTemplateName"`
	Description    *string `json:"interfaceDescription"`
}

func (c *client) fabricPortAssignments(ctx context.Context, fabricID, deviceID string) ([]fabricPortAssignment, error) {
	if !validAPIID(fabricID) || !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	// 2.3.7.6 documents a maximum of 50 records, unlike port channels (500).
	rows, err := listInventoryPagesWithGet(ctx, get, fabricPortAssignmentsPath, url.Values{"fabricId": {fabricID}, "networkDeviceId": {deviceID}}, func(row fabricPortAssignment) string { return row.ID }, 50)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		key := strings.ToLower(row.Interface)
		if !validAPIID(row.ID) || row.FabricID != fabricID || row.DeviceID != deviceID || !validLANInterfaceName(row.Interface) || seen[key] || !slices.Contains([]string{"USER_DEVICE", "ACCESS_POINT", "TRUNKING_DEVICE", "AUTHENTICATOR_SWITCH", "SUPPLICANT_BASED_EXTENDED_NODE"}, row.Type) {
			return nil, errInvalidResponse
		}
		seen[key] = true
		for _, value := range []*string{row.DataVLAN, row.VoiceVLAN, row.SecurityGroup} {
			// Native empty strings can represent unconfigured optional settings.
			if value != nil && (len(*value) > 256 || strings.TrimSpace(*value) != *value || strings.ContainsFunc(*value, unicode.IsControl)) {
				return nil, errInvalidResponse
			}
		}
		if row.Authentication != nil && !slices.Contains([]string{"", "No Authentication", "Open Authentication", "Closed Authentication", "Low Impact"}, *row.Authentication) {
			return nil, errInvalidResponse
		}
		if row.Description != nil && (len(*row.Description) > 4096 || strings.ContainsFunc(*row.Description, unicode.IsControl)) {
			return nil, errInvalidResponse
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func fabricPortAssignmentMatches(want fabricPortAssignmentIntent, row fabricPortAssignment) bool {
	if want.Interface == nil || want.Range != nil || *want.Interface != row.Interface || want.Type != row.Type {
		return false
	}
	for _, pair := range [][2]*string{{want.DataVLAN, row.DataVLAN}, {want.VoiceVLAN, row.VoiceVLAN}, {want.SecurityGroup, row.SecurityGroup}, {want.Authentication, row.Authentication}, {want.Description, row.Description}} {
		if pair[0] != nil && (pair[1] == nil || *pair[0] != *pair[1]) {
			return false
		}
	}
	return true
}
