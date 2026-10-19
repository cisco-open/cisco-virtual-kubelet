// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "strings"

type fabricPortAssignmentSelection struct {
	missing  []fabricPortAssignmentIntent
	existing []fabricPortAssignment
}

// Matching observations establish compliance only, not adoption. Conflicting
// settings must not be converted into a create or overwritten implicitly.
func selectFabricPortAssignments(wants []fabricPortAssignmentIntent, before []fabricPortAssignment) (*fabricPortAssignmentSelection, error) {
	rows, err := expandFabricPortAssignments(wants)
	if err != nil || len(rows) == 0 {
		return nil, errInvalidResponse
	}
	byPort := map[string]fabricPortAssignment{}
	ids := map[string]bool{}
	for _, row := range before {
		key := strings.ToLower(row.Interface)
		if !validAPIID(row.ID) || !validLANInterfaceName(row.Interface) || ids[row.ID] {
			return nil, errInvalidResponse
		}
		if _, duplicate := byPort[key]; duplicate {
			return nil, errInvalidResponse
		}
		ids[row.ID] = true
		byPort[key] = row
	}
	out := &fabricPortAssignmentSelection{}
	for _, want := range rows {
		row, present := byPort[strings.ToLower(*want.Interface)]
		if !present {
			out.missing = append(out.missing, want)
			continue
		}
		if !fabricPortAssignmentMatches(want, row) {
			return nil, errConfigurationStale
		}
		out.existing = append(out.existing, row)
	}
	return out, nil
}
