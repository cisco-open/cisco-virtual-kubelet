// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
)

const fabricPendingEventsPath = "/dna/intent/api/v1/sda/pendingFabricEvents"

// Event details are free-form native text. Retain only the identifiers needed
// for a scoped apply request; do not expose details in drift or journal entries.
type fabricPendingEvent struct {
	ID       string `json:"id"`
	FabricID string `json:"fabricId"`
}

func (c *client) fabricPendingEvents(ctx context.Context, fabricID string) ([]fabricPendingEvent, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, query url.Values) ([]byte, error) {
		return c.get(ctx, path, query, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricPendingEventsPath, url.Values{"fabricId": {fabricID}}, func(raw json.RawMessage) string {
		var event fabricPendingEvent
		if json.Unmarshal(raw, &event) != nil || !validAPIID(event.ID) || event.FabricID != fabricID {
			return ""
		}
		return event.ID
	})
	if err != nil {
		return nil, err
	}
	result := make([]fabricPendingEvent, 0, len(rows))
	for _, row := range rows {
		var event fabricPendingEvent
		if json.Unmarshal(row, &event) != nil || !validAPIID(event.ID) || event.FabricID != fabricID {
			return nil, errInvalidResponse
		}
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// Explicit event identity is mandatory. Never interpret an empty selection as
// "apply all", or select the first native event from an ambiguous response.
func fabricPendingEventPayload(event fabricPendingEvent) ([]byte, error) {
	if !validAPIID(event.ID) || !validAPIID(event.FabricID) {
		return nil, errInvalidResponse
	}
	return json.Marshal([]fabricPendingEvent{event})
}
