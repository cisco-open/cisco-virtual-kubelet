// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

type plannedAPPosition struct {
	X *float64 `json:"x"`
	Y *float64 `json:"y"`
	Z *float64 `json:"z"`
}
type plannedAPAntenna struct {
	Name      string `json:"name"`
	Azimuth   *int   `json:"azimuth"`
	Elevation *int   `json:"elevation"`
}
type plannedAPRadio struct {
	ID      string            `json:"id"`
	Bands   []float64         `json:"bands"`
	Channel *int              `json:"channel"`
	TxPower *int              `json:"txPower"`
	Antenna *plannedAPAntenna `json:"antenna"`
}
type plannedAPObservation struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	MAC      *string            `json:"macAddress"`
	Position *plannedAPPosition `json:"position"`
	Radios   []plannedAPRadio   `json:"radios"`
}

func plannedAPPositionsPath(floorID string) string {
	return "/dna/intent/api/v2/floors/" + url.PathEscape(floorID) + "/plannedAccessPointPositions"
}

func validatePlannedAPObservation(ap *plannedAPObservation) error {
	if ap == nil || !validAPIID(ap.ID) || ap.MAC == nil || ap.Position == nil || ap.Position.X == nil || ap.Position.Y == nil || ap.Position.Z == nil || len(ap.Radios) < 1 || len(ap.Radios) > 4 {
		return errInvalidResponse
	}
	for _, value := range []string{ap.Name, ap.Type} {
		if _, err := plannedAPText(value); err != nil {
			return errInvalidResponse
		}
	}
	for _, value := range []*float64{ap.Position.X, ap.Position.Y, ap.Position.Z} {
		if _, err := plannedAPNumber(*value, false); err != nil {
			return errInvalidResponse
		}
	}
	if *ap.MAC != "" {
		mac, err := net.ParseMAC(*ap.MAC)
		if err != nil || len(mac) != 6 {
			return errInvalidResponse
		}
		canonical := mac.String()
		ap.MAC = &canonical
	}
	ids := map[string]bool{}
	for _, radio := range ap.Radios {
		if !validAPIID(radio.ID) || ids[radio.ID] || len(radio.Bands) < 1 || len(radio.Bands) > 3 || radio.Channel == nil || radio.TxPower == nil || radio.Antenna == nil {
			return errInvalidResponse
		}
		ids[radio.ID] = true
		bands := map[float64]bool{}
		for _, band := range radio.Bands {
			if (band != 2.4 && band != 5 && band != 6) || bands[band] {
				return errInvalidResponse
			}
			bands[band] = true
		}
		antenna := radio.Antenna
		if _, err := plannedAPText(antenna.Name); err != nil {
			return errInvalidResponse
		}
		if antenna.Azimuth == nil || antenna.Elevation == nil || *antenna.Azimuth < 0 || *antenna.Azimuth > 360 || *antenna.Elevation < -90 || *antenna.Elevation > 90 {
			return errInvalidResponse
		}
	}
	return nil
}

// Obtain a complete floor-local inventory through the common authenticated
// transport. Null/missing pages, partial HTTP responses, repeated identities and
// pagination exhaustion are errors, never evidence that a planned AP is absent.
type plannedAPNativePosition struct {
	actual plannedAPObservation
	native map[string]any
}

func (c *client) plannedAPNativePositions(ctx context.Context, floorID string) (map[string]plannedAPNativePosition, error) {
	if !validAPIID(floorID) {
		return nil, errInvalidResponse
	}
	const pageSize = 500
	out := map[string]plannedAPNativePosition{}
	ids := map[string]bool{}
	for offset := 1; offset <= 10000; offset += pageSize {
		raw, err := c.get(ctx, plannedAPPositionsPath(floorID), url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(pageSize)}}, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return nil, errInvalidResponse
		}
		response := bytes.TrimSpace(envelope.Response)
		if len(response) == 0 || response[0] != '[' {
			return nil, errInvalidResponse
		}
		var page []json.RawMessage
		if json.Unmarshal(response, &page) != nil || len(page) > pageSize {
			return nil, errInvalidResponse
		}
		for _, item := range page {
			var ap plannedAPObservation
			if json.Unmarshal(item, &ap) != nil {
				return nil, errInvalidResponse
			}
			value, err := decodeExactNativeJSON(item)
			if err != nil {
				return nil, err
			}
			native, ok := value.(map[string]any)
			if !ok || native == nil {
				return nil, errInvalidResponse
			}
			if err := validatePlannedAPObservation(&ap); err != nil {
				return nil, err
			}
			if _, duplicate := out[ap.Name]; duplicate || ids[ap.ID] {
				return nil, errInvalidResponse
			}
			ids[ap.ID] = true
			out[ap.Name] = plannedAPNativePosition{actual: ap, native: native}
		}
		if len(page) < pageSize {
			return out, nil
		}
	}
	return nil, errInvalidResponse
}

// Preserve the existing typed API for Report and independent readback callers.
// The executor consumes both projections from the same validated paged response.
func (c *client) plannedAPPositions(ctx context.Context, floorID string) (map[string]plannedAPObservation, error) {
	rows, err := c.plannedAPNativePositions(ctx, floorID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]plannedAPObservation, len(rows))
	for name, row := range rows {
		out[name] = row.actual
	}
	return out, nil
}
