// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
)

const lanActiveSessionsPath = "/dna/intent/api/v1/lan-automation/sessions"

type lanActiveSessionSnapshot struct {
	Maximum int
	Active  []string
}

// The dedicated native active count/list is authoritative for session activity;
// free-form history status and a STOP action cannot substitute for this record.
func (c *client) lanActiveSessions(ctx context.Context) (*lanActiveSessionSnapshot, error) {
	raw, err := c.get(ctx, lanActiveSessionsPath, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var body struct {
		Response *struct {
			Maximum *string  `json:"maxSupportedCount"`
			Count   *string  `json:"activeSessions"`
			IDs     []string `json:"activeSessionIds"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response == nil || body.Response.Maximum == nil || body.Response.Count == nil || body.Response.IDs == nil {
		return nil, errInvalidResponse
	}
	parse := func(raw string) (int, error) {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 1000 || strconv.Itoa(n) != raw {
			return 0, errInvalidResponse
		}
		return n, nil
	}
	maximum, err := parse(*body.Response.Maximum)
	if err != nil {
		return nil, err
	}
	count, err := parse(*body.Response.Count)
	if err != nil || count > maximum || count != len(body.Response.IDs) {
		return nil, errInvalidResponse
	}
	seen := map[string]bool{}
	ids := append([]string{}, body.Response.IDs...)
	for _, id := range ids {
		if !validAPIID(id) || seen[id] {
			return nil, errInvalidResponse
		}
		seen[id] = true
	}
	sort.Strings(ids)
	return &lanActiveSessionSnapshot{Maximum: maximum, Active: ids}, nil
}
