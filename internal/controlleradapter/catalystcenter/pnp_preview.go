// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
)

const pnpPreviewPath = pnpDevicesPath + "/site-config-preview"

// Preview generation is not device configuration or claim completion. Only
// structural metadata is retained: generated configuration and native error
// text may contain credentials and must never enter status or diagnostics.
type pnpPreviewObservation struct {
	Complete bool   `json:"complete"`
	Failed   bool   `json:"error"`
	SiteID   string `json:"siteId"`
	TaskID   string `json:"taskId,omitempty"`
}

type pnpPreviewWrite struct {
	submit func(context.Context) (*pnpPreviewObservation, error)
}

func pnpPreviewPayload(want pnpDeviceIntent, deviceID, siteID string) ([]byte, error) {
	members, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	if !validAPIID(deviceID) || !validAPIID(siteID) {
		return nil, errInvalidResponse
	}
	typ := want.Type
	if typ == "" {
		typ = "Default"
	}
	if len(members) > 1 {
		typ = "StackSwitch"
	}
	switch typ {
	case "Default", "StackSwitch", "Sensor", "MobilityExpress":
	default:
		// The pinned module excludes AP previews. CatalystWLC is not a
		// supported type in the documented native preview request.
		return nil, errConfigurationTransition
	}
	return json.Marshal(map[string]string{"deviceId": deviceID, "siteId": siteID, "type": typ})
}

func decodePnPPreview(raw []byte, siteID string) (*pnpPreviewObservation, error) {
	var envelope struct {
		Response *struct {
			Complete *bool  `json:"complete"`
			Failed   *bool  `json:"error"`
			SiteID   string `json:"siteId"`
			TaskID   string `json:"taskId"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil {
		return nil, errInvalidResponse
	}
	r := envelope.Response
	if r.Complete == nil || r.Failed == nil || !validAPIID(siteID) || r.SiteID != siteID || r.TaskID != "" && !validAPIID(r.TaskID) {
		return nil, errInvalidResponse
	}
	return &pnpPreviewObservation{Complete: *r.Complete, Failed: *r.Failed, SiteID: r.SiteID, TaskID: r.TaskID}, nil
}

// Private primitive. Its caller still needs durable owned post-claim sequencing;
// the one-use closure only prevents replay within this plan, not across restart.
func (c *client) planPnPPreview(ctx context.Context, want pnpDeviceIntent, deviceID, siteID string) (*pnpPreviewWrite, error) {
	// Freeze nested pointers/slices before either preflight or request assembly.
	raw, err := json.Marshal(want)
	if err != nil {
		return nil, err
	}
	var frozen pnpDeviceIntent
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, errInvalidResponse
	}
	body, err := pnpPreviewPayload(frozen, deviceID, siteID)
	if err != nil {
		return nil, err
	}
	snapshot, err := c.pnpClaimInventorySnapshot(ctx, frozen, deviceID, siteID)
	if err != nil {
		return nil, err
	}
	observedSite := snapshot.pnp.Info.SiteID
	if observedSite == "" {
		observedSite = snapshot.pnp.Info.Location.SiteID
	}
	if observedSite != siteID {
		return nil, errConfigurationStale
	}
	var used atomic.Bool
	return &pnpPreviewWrite{submit: func(ctx context.Context) (*pnpPreviewObservation, error) {
		if used.Swap(true) {
			return nil, errConfigurationTransition
		}
		if err := c.revalidatePnPClaimInventory(ctx, frozen, deviceID, siteID, snapshot); err != nil {
			return nil, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, pnpPreviewPath, nil, body, http.StatusOK)
		if err != nil {
			return nil, err
		}
		return decodePnPPreview(raw, siteID)
	}}, nil
}
