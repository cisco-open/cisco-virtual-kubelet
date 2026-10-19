// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Deliberately omit free-form errors and credential-bearing API fields.
type discoveryDeviceOutcome struct {
	ID           string `json:"id"`
	IP           string `json:"managementIpAddress"`
	CLI          string `json:"cliStatus"`
	SNMP         string `json:"snmpStatus"`
	NETCONF      string `json:"netconfStatus"`
	Collection   string `json:"inventoryCollectionStatus"`
	Reachability string `json:"inventoryReachabilityStatus"`
}

func (c *client) discoveryDevices(ctx context.Context, id string) ([]discoveryDeviceOutcome, error) {
	if !validAPIID(id) {
		return nil, errInvalidResponse
	}
	rows := []discoveryDeviceOutcome{}
	seen := map[string]bool{}
	addresses := map[netip.Addr]bool{}
	for start := 1; start <= 10000; start += 100 {
		raw, err := c.get(ctx, fmt.Sprintf("%s/%s/network-device/%d/100", discoveryPath, id, start), nil, 200)
		if err != nil {
			return nil, err
		}
		var page struct {
			Response *[]discoveryDeviceOutcome `json:"response"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Response == nil || len(*page.Response) > 100 {
			return nil, errInvalidResponse
		}
		for _, row := range *page.Response {
			ip, err := netip.ParseAddr(row.IP)
			if err != nil || !ip.Is4() || !validAPIID(row.ID) || seen[row.ID] || addresses[ip] {
				return nil, errInvalidResponse
			}
			seen[row.ID], addresses[ip] = true, true
			rows = append(rows, row)
		}
		if len(*page.Response) < 100 {
			return rows, nil
		}
	}
	return nil, errInvalidResponse
}

type discoveryProtocolCounts struct{ Success, Failure, Unverified int }

func (c *discoveryProtocolCounts) add(status string) {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS":
		c.Success++
	case "FAILURE":
		c.Failure++
	default:
		c.Unverified++
	}
}
func (c discoveryProtocolCounts) String() string {
	return fmt.Sprintf("%d successful/%d failed/%d unverified", c.Success, c.Failure, c.Unverified)
}

// Counts are job/device observations, not globally deduplicated inventory.
// Configuration convergence and operational outcomes remain distinct: range
// discovery can intentionally include addresses at which no device exists.
type discoveryOutcomeSummary struct {
	Jobs, MissingJobs, EmptyJobs, Devices, ManagedReachable, OtherCollection int
	CLI, SNMP, NETCONF                                                       discoveryProtocolCounts
}

func (s *discoveryOutcomeSummary) add(rows []discoveryDeviceOutcome) {
	s.Jobs++
	if len(rows) == 0 {
		s.EmptyJobs++
	}
	for _, row := range rows {
		s.Devices++
		s.CLI.add(row.CLI)
		s.SNMP.add(row.SNMP)
		s.NETCONF.add(row.NETCONF)
		if strings.EqualFold(row.Collection, "Managed") && strings.EqualFold(row.Reachability, "Reachable") {
			s.ManagedReachable++
		} else {
			s.OtherCollection++
		}
	}
}
func (s *discoveryOutcomeSummary) message() string {
	return fmt.Sprintf("Discovery outcomes: %d observed jobs, %d missing, %d without device outcomes (unverified); %d job/device observations; CLI %s; SNMP %s; NETCONF %s; collection %d Managed/Reachable, %d other/unverified. Configuration convergence does not imply successful onboarding", s.Jobs, s.MissingJobs, s.EmptyJobs, s.Devices, s.CLI, s.SNMP, s.NETCONF, s.ManagedReachable, s.OtherCollection)
}
