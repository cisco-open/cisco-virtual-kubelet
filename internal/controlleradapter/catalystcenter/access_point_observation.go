// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
)

const accessPointSummaryPath = "/dna/intent/api/v1/wireless/accesspoint-configuration/summary"

type accessPointObservation struct {
	EthernetMAC        string                        `json:"ethMac"`
	Name               string                        `json:"apName"`
	ProvisioningStatus *string                       `json:"provisioningStatus"`
	AdminStatus        *string                       `json:"adminStatus"`
	Mode               *string                       `json:"apMode"`
	FailoverPriority   *string                       `json:"failoverPriority"`
	LEDStatus          *string                       `json:"ledStatus"`
	LEDBrightness      *int                          `json:"ledBrightnessLevel"`
	Location           *string                       `json:"location"`
	PrimaryName        *string                       `json:"primaryControllerName"`
	PrimaryIP          *string                       `json:"primaryIpAddress"`
	SecondaryName      *string                       `json:"secondaryControllerName"`
	SecondaryIP        *string                       `json:"secondaryIpAddress"`
	TertiaryName       *string                       `json:"tertiaryControllerName"`
	TertiaryIP         *string                       `json:"tertiaryIpAddress"`
	Reachability       *string                       `json:"reachabilityStatus"`
	Radios             []accessPointRadioObservation `json:"radioDTOs"`
}
type accessPointRadioObservation struct {
	Type         *int    `json:"ifType"`
	Slot         *int    `json:"slotId"`
	AdminStatus  *string `json:"adminStatus"`
	PowerMode    *string `json:"powerAssignmentMode"`
	PowerLevel   *int    `json:"powerlevel"`
	ChannelMode  *string `json:"channelAssignmentMode"`
	Channel      *int    `json:"channelNumber"`
	ChannelWidth *string `json:"channelWidth"`
	AntennaName  *string `json:"antennaPatternName"`
	AntennaGain  *int    `json:"antennaGain"`
	// The published schema does not describe these object members. Preserve
	// them for qualified decoding rather than guessing strings or enum values.
	Role json.RawMessage `json:"radioRoleAssignment"`
	Band json.RawMessage `json:"radioBand"`
}

// The keyed API documents a direct object. Reject envelopes, arrays and partial
// responses rather than guessing a shape that could mix several AP identities.
// This reads evidence only; absent or unknown provisioning status is not success.
func (c *client) accessPointConfiguration(ctx context.Context, ethernetMAC string) (accessPointObservation, error) {
	var out accessPointObservation
	key, err := accessPointEthernetMAC(ethernetMAC)
	if err != nil {
		return out, err
	}
	raw, err := c.get(ctx, accessPointSummaryPath, url.Values{"key": {key}}, 200)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(raw, &out) != nil {
		return accessPointObservation{}, errInvalidResponse
	}
	observed, err := accessPointEthernetMAC(out.EthernetMAC)
	if err != nil || observed != key {
		return accessPointObservation{}, errInvalidResponse
	}
	if _, err := plannedAPText(out.Name); err != nil {
		return accessPointObservation{}, errInvalidResponse
	}
	if len(out.Radios) > 8 {
		return accessPointObservation{}, errInvalidResponse
	}
	slots := map[int]bool{}
	for _, radio := range out.Radios {
		if radio.Type == nil || radio.Slot == nil || *radio.Slot < 0 || slots[*radio.Slot] {
			return accessPointObservation{}, errInvalidResponse
		}
		switch *radio.Type {
		case 1, 2, 3, 6:
		default:
			return accessPointObservation{}, errInvalidResponse
		}
		slots[*radio.Slot] = true
	}
	out.EthernetMAC = observed
	return out, nil
}
