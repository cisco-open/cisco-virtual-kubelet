// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
)

type fabricPendingEventWrite struct {
	resource string
	event    fabricPendingEvent
	siteID   string
	submit   func(context.Context, func(context.Context) error) (Task, error)
}

func pendingFabricEventResource(path, siteID string, event fabricPendingEvent) string {
	return fmt.Sprintf("fabric-pending-event:%x", sha256.Sum256([]byte(path+"\x00"+siteID+"\x00"+event.FabricID+"\x00"+event.ID)))
}

// Private native primitive, deliberately separate from configurationWrite.
// A public caller needs the complete physical footprint, group admission and a
// durable journal. A task acknowledgement is not evidence of device convergence.
func (c *client) planPendingFabricEvent(ctx context.Context, want fabricSiteIntent, eventID string) (*fabricPendingEventWrite, error) {
	if !validAPIID(eventID) {
		return nil, errInvalidResponse
	}
	snapshot, err := c.pendingFabricEventsSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	var selected *fabricPendingEvent
	for _, event := range snapshot.events {
		if event.ID == eventID {
			copy := event
			selected = &copy
			break
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
	}
	body, err := fabricPendingEventPayload(*selected)
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &fabricPendingEventWrite{resource: pendingFabricEventResource(want.path, snapshot.site.siteID, *selected), event: *selected, siteID: snapshot.site.siteID, submit: func(ctx context.Context, authorize func(context.Context) error) (Task, error) {
		if used.Swap(true) {
			return Task{}, errConfigurationTransition
		}
		if err := c.revalidatePendingFabricEvents(ctx, want, snapshot); err != nil {
			return Task{}, err
		}
		if authorize == nil {
			return Task{}, errConfigurationTransition
		}
		if err := authorize(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, fabricPendingEventsPath+"/apply", nil, body, http.StatusOK)
		if err != nil {
			return Task{}, err
		}
		var envelope struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &envelope) != nil || !validAPIID(envelope.Response.ID) {
			return Task{}, errInvalidResponse
		}
		// Reuse the configured task endpoint later, never follow a returned URL.
		return Task{ID: envelope.Response.ID}, nil
	}}, nil
}
