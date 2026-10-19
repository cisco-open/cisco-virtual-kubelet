// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type fabricPortAssignmentIntent struct {
	Interface      *string `json:"interface_name"`
	Range          *string `json:"interfaces_range"`
	Type           string  `json:"connected_device_type"`
	DataVLAN       *string `json:"data_vlan_name"`
	VoiceVLAN      *string `json:"voice_vlan_name"`
	SecurityGroup  *string `json:"security_group_name"`
	Authentication *string `json:"authenticate_template_name"`
	Description    *string `json:"interface_description"`
}

type fabricPortAssignmentDeploymentIntent struct {
	device      fabricDeviceIntent
	assignments []fabricPortAssignmentIntent
}

// Pinned 0.5.0 expands the final port number of slot/module/port ranges. Reject
// different end modules instead of silently applying the start module to them.
var fabricPortAssignmentRange = regexp.MustCompile(`^([A-Za-z]+)([0-9]+)/([0-9]+)/([0-9]+)-([0-9]+)/([0-9]+)/([0-9]+)$`)

func expandFabricPortAssignments(rows []fabricPortAssignmentIntent) ([]fabricPortAssignmentIntent, error) {
	out := []fabricPortAssignmentIntent{}
	seen := map[string]bool{}
	for _, row := range rows {
		if !slices.Contains([]string{"USER_DEVICE", "ACCESS_POINT", "TRUNKING_DEVICE", "AUTHENTICATOR_SWITCH", "SUPPLICANT_BASED_EXTENDED_NODE"}, row.Type) || (row.Interface == nil) == (row.Range == nil) {
			return nil, errInvalidResponse
		}
		for _, name := range []*string{row.DataVLAN, row.VoiceVLAN, row.SecurityGroup} {
			if name != nil && (!validProfileName(*name) || strings.ContainsFunc(*name, unicode.IsControl)) {
				return nil, errInvalidResponse
			}
		}
		if row.Authentication != nil && !slices.Contains([]string{"No Authentication", "Open Authentication", "Closed Authentication", "Low Impact"}, *row.Authentication) {
			return nil, errInvalidResponse
		}
		if row.Description != nil && (len(*row.Description) > 4096 || strings.ContainsFunc(*row.Description, unicode.IsControl)) {
			return nil, errInvalidResponse
		}
		ports := []string{}
		if row.Interface != nil {
			if !validLANInterfaceName(*row.Interface) {
				return nil, errInvalidResponse
			}
			ports = append(ports, *row.Interface)
		} else {
			if len(*row.Range) > 256 {
				return nil, errInvalidResponse
			}
			parts := fabricPortAssignmentRange.FindStringSubmatch(*row.Range)
			if parts == nil || parts[2] != parts[5] || parts[3] != parts[6] {
				return nil, errInvalidResponse
			}
			first, err1 := strconv.Atoi(parts[4])
			last, err2 := strconv.Atoi(parts[7])
			if err1 != nil || err2 != nil || first < 0 || last < first || last-first >= 1000 {
				return nil, errInvalidResponse
			}
			prefix := parts[1] + parts[2] + "/" + parts[3] + "/"
			// Offset iteration cannot overflow when the final index is MaxInt.
			for offset := 0; offset <= last-first; offset++ {
				ports = append(ports, prefix+strconv.Itoa(first+offset))
			}
		}
		if len(out)+len(ports) > 1000 {
			return nil, errInvalidResponse
		}
		for _, port := range ports {
			key := strings.ToLower(port)
			if seen[key] {
				return nil, errInvalidResponse
			}
			seen[key] = true
			copy := row
			copy.Interface = &port
			copy.Range = nil
			out = append(out, copy)
		}
	}
	sort.Slice(out, func(i, j int) bool { return *out[i].Interface < *out[j].Interface })
	return out, nil
}

func parseFabricPortAssignmentDeployment(raw []byte) ([]fabricDeviceIntent, []fabricPortAssignmentDeploymentIntent, error) {
	base, assignments, err := parseFabricEdgeExtensions(raw, "port_assignments", expandFabricPortAssignments)
	if err != nil {
		return nil, nil, err
	}
	out := []fabricPortAssignmentDeploymentIntent{}
	for _, device := range base {
		if rows, present := assignments[device.inventory.reference.Name]; present {
			out = append(out, fabricPortAssignmentDeploymentIntent{device: device, assignments: rows})
		}
	}
	return base, out, nil
}
