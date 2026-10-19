// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"
)

// Native evidence inputs reconstructed by a future owned journal caller. This
// type is not a second durable store and does not itself authorize observation
// as an owned operation or release any physical reservation.
type fabricPendingEventReceipt struct {
	resource, siteID, taskID string
	event                    fabricPendingEvent
	submitted                time.Time
}

type fabricPendingEventObservation struct {
	taskComplete bool
	eventPending bool
}

func (c *client) observePendingFabricEvent(ctx context.Context, want fabricSiteIntent, receipt fabricPendingEventReceipt, now time.Time) (*fabricPendingEventObservation, error) {
	if !validAPIID(receipt.siteID) || !validAPIID(receipt.taskID) || !validAPIID(receipt.event.ID) || !validAPIID(receipt.event.FabricID) || receipt.submitted.IsZero() || now.Before(receipt.submitted) || receipt.resource != pendingFabricEventResource(want.path, receipt.siteID, receipt.event) {
		return nil, errConfigurationStale
	}
	snapshot, err := c.pendingFabricEventsSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	if snapshot.site.siteID != receipt.siteID || snapshot.site.actual.ID != receipt.event.FabricID {
		return nil, errConfigurationStale
	}
	done, err := c.configurationTaskComplete(ctx, receipt.taskID, receipt.submitted, now)
	if err != nil {
		return nil, err
	}
	if done {
		done, err = c.configurationTaskTreeComplete(ctx, receipt.taskID, receipt.submitted, now)
		if err != nil {
			return nil, err
		}
	}
	pending := false
	for _, event := range snapshot.events {
		if event.ID == receipt.event.ID {
			pending = true
		}
	}
	if err := c.revalidatePendingFabricEvents(ctx, want, snapshot); err != nil {
		return nil, err
	}
	// Keep these facts separate. Event disappearance alone is not task success;
	// even both facts together do not attest every device's convergence.
	return &fabricPendingEventObservation{taskComplete: done, eventPending: pending}, nil
}
