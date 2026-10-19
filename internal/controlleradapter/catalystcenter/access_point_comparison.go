// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"sort"
	"time"
)

type accessPointSettingState string

const (
	apSettingMatched     accessPointSettingState = "Matched"
	apSettingPending     accessPointSettingState = "Pending"
	apSettingUnavailable accessPointSettingState = "Unavailable"
)

// Paths and classification only: native free-form values are not diagnostics.
type accessPointSettingComparison struct {
	path  string
	state accessPointSettingState
}

type accessPointConfigurationComplianceObservation struct {
	execution *accessPointConfigurationExecutionObservation
	settings  [][]accessPointSettingComparison
}

// This is selected-setting comparison, not provisioning/actor completion or a
// release grant. Unknown representations and an empty selection cannot pass.
func accessPointSettingsMatch(rows []accessPointSettingComparison) bool {
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if row.state != apSettingMatched {
			return false
		}
	}
	return true
}

// Only fields with independently comparable readback types are mapped here.
// Native bool/enum response strings, radio role/band objects, cable properties
// and assigned-site location semantics still need qualified representations.
var apComparableSettings = map[string]string{
	"apNameNew": "apName", "ledBrightnessLevel": "ledBrightnessLevel", "location": "location",
	"primaryControllerName": "primaryControllerName", "primaryIpAddress": "primaryIpAddress",
	"secondaryControllerName": "secondaryControllerName", "secondaryIpAddress": "secondaryIpAddress",
	"tertiaryControllerName": "tertiaryControllerName", "tertiaryIpAddress": "tertiaryIpAddress",
}
var apComparableRadioSettings = map[string]string{
	"antennaPatternName": "antennaPatternName", "antennaGain": "antennaGain",
	"channelNumber": "channelNumber", "powerLevel": "powerlevel",
}

func compareAccessPointProperties(want, got map[string]any, fields map[string]apSettingField, readable map[string]string, prefix string) []accessPointSettingComparison {
	gates := map[string]bool{}
	for _, field := range fields {
		if field.gate != "" {
			gates[field.gate] = true
		}
	}
	rows := []accessPointSettingComparison{}
	for key, expected := range want {
		if gates[key] {
			continue
		}
		state := apSettingUnavailable
		if observedKey, comparable := readable[key]; comparable {
			actual := got[observedKey]
			state = apSettingPending
			if wrapped, ok := expected.(map[string]any); ok {
				// Desired IPs use {address: ...}; keyed readback uses a string.
				address, selected := wrapped["address"].(string)
				observed, present := actual.(string)
				left, lerr := netip.ParseAddr(address)
				right, rerr := netip.ParseAddr(observed)
				if selected && present && lerr == nil && rerr == nil && left.Zone() == "" && right.Zone() == "" && left == right {
					state = apSettingMatched
				}
			} else if actual != nil && reflect.DeepEqual(expected, actual) {
				state = apSettingMatched
			}
		}
		rows = append(rows, accessPointSettingComparison{path: prefix + key, state: state})
	}
	return rows
}

// The native observation reader already binds this row to the exact cohort
// member. Comparison covers every selected payload value, including values
// whose response representation is currently unavailable; none are dropped.
func compareAccessPointSettings(want accessPointDeviceIntent, row accessPointObservation) ([]accessPointSettingComparison, error) {
	if _, err := accessPointEthernetMAC(row.EthernetMAC); err != nil || row.Name == "" {
		return nil, errAccessPointIdentity
	}
	project := func(value any) (map[string]any, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if json.Unmarshal(raw, &out) != nil || out == nil {
			return nil, errInvalidResponse
		}
		return out, nil
	}
	payload := map[string]any{}
	if want.profile != nil {
		var err error
		payload, err = project(want.profile.payload)
		if err != nil {
			return nil, err
		}
	}
	if want.manageHostname {
		payload["apNameNew"] = want.inventory.reference.Name
	}
	actual, err := project(row)
	if err != nil {
		return nil, err
	}
	radioList, hasRadios := payload["radioConfigurations"]
	delete(payload, "radioConfigurations")
	if payload["isAssignedSiteAsLocation"] == false {
		delete(payload, "isAssignedSiteAsLocation")
	}
	rows := compareAccessPointProperties(payload, actual, apProfileFields, apComparableSettings, "/")
	if hasRadios {
		radios, ok := radioList.([]any)
		if !ok {
			return nil, errAccessPointModel
		}
		for _, entry := range radios {
			desired, ok := entry.(map[string]any)
			if !ok {
				return nil, errAccessPointModel
			}
			kind, ok := desired["radioType"].(float64)
			if !ok || kind != 1 && kind != 2 && kind != 3 && kind != 6 {
				return nil, errAccessPointModel
			}
			delete(desired, "radioType")
			found := 0
			observed := map[string]any{}
			for _, radio := range row.Radios {
				if radio.Type != nil && float64(*radio.Type) == kind {
					found++
					observed, err = project(radio)
					if err != nil {
						return nil, err
					}
				}
			}
			comparisons := compareAccessPointProperties(desired, observed, apRadioFields, apComparableRadioSettings, fmt.Sprintf("/radioConfigurations/%g/", kind))
			if found > 1 {
				// Dual-band/slot semantics must be qualified before choosing one
				// same-type radio or treating the first matching value as proof.
				for i := range comparisons {
					comparisons[i].state = apSettingUnavailable
				}
			}
			rows = append(rows, comparisons...)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].path < rows[j].path })
	return rows, nil
}

func (c *client) observeAccessPointConfigurationCompliance(ctx context.Context, wants []accessPointDeviceIntent, digest, taskID string, submitted, now time.Time, guard func(context.Context, string, []accessPointConfigurationMember) error) (*accessPointConfigurationComplianceObservation, error) {
	frozen, err := freezeAccessPointConfiguration(wants)
	if err != nil {
		return nil, err
	}
	execution, err := c.observeAccessPointConfigurationExecution(ctx, frozen, digest, taskID, submitted, now, guard)
	if err != nil || execution == nil {
		return nil, err
	}
	if len(execution.members) != len(frozen) {
		return nil, errConfigurationStale
	}
	settings := make([][]accessPointSettingComparison, len(frozen))
	for i := range frozen {
		settings[i], err = compareAccessPointSettings(frozen[i], execution.members[i])
		if err != nil {
			return nil, err
		}
	}
	return &accessPointConfigurationComplianceObservation{execution: execution, settings: settings}, nil
}
