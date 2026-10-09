// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const networkDeviceImagesPath = "/dna/intent/api/v1/networkDeviceImages/"

type DeviceImageDetails struct {
	GoldenImages []struct {
		ID        string `json:"id"`
		Version   string `json:"version"`
		ImageType string `json:"imageType"`
	} `json:"goldenImages"`
	ID                string `json:"id"`
	ManagementAddress string `json:"managementAddress"`
	NetworkDevice     struct {
		ID string `json:"id"`
	} `json:"networkDevice"`
}

func (c *client) GetDeviceImageDetails(ctx context.Context, deviceID string) (DeviceImageDetails, error) {
	if !validAPIID(deviceID) {
		return DeviceImageDetails{}, errors.New("invalid SWIM device ID")
	}
	raw, err := c.Get(ctx, networkDeviceImagesPath+deviceID, nil)
	if err != nil {
		return DeviceImageDetails{}, err
	}
	var env struct {
		Response *DeviceImageDetails `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Response == nil || env.Response.ID != deviceID || !validAPIID(env.Response.NetworkDevice.ID) {
		return DeviceImageDetails{}, errInvalidResponse
	}
	return *env.Response, nil
}

// StartReadinessCheck runs system validations, not distribution or activation.
// The caller must persist the submission intent and returned task ID. POST is
// never retried; missing receipts are ambiguous, including for preflight jobs.
// This API requires Catalyst Center 2.3.7.10 or later; unsupported appliances
// return an error rather than silently skipping validation.
func (c *client) StartReadinessCheck(ctx context.Context, deviceID string) (Task, error) {
	if !validAPIID(deviceID) {
		return Task{}, errors.New("invalid SWIM device ID")
	}
	return c.submitTask(ctx, networkDeviceImagesPath+deviceID+"/readinessChecks", struct{}{})
}

// ReadinessResult contains identity and outcome evidence only. Remote free-form
// resultDetails may contain device output; do not copy them into status/logs.
type ReadinessResult struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	DeviceID  string `json:"networkDeviceId"`
	Operation string `json:"operationType"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
	// Derived only from the exact qualified positive fallback detail pair.
	// Never accepted from a top-level API field or logged as arbitrary text.
	TransferFallbackAddress string `json:"-"`
	XFSUVersionPathTarget   string `json:"-"`
}

// 3.2.3 may omit top-level status and return it as a STATUS entry in a
// resultDetails array; the published schema also permits a single object.
// Retain only the normalized outcome, never arbitrary CLI/configuration text.
func (r *ReadinessResult) UnmarshalJSON(data []byte) error {
	type plain ReadinessResult
	var wire struct {
		plain
		Details json.RawMessage `json:"resultDetails"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return errInvalidResponse
	}
	status := strings.ToUpper(strings.TrimSpace(wire.Status))
	fallbackAddress := ""
	xfsuTarget := ""
	if len(wire.Details) > 0 && string(wire.Details) != "null" {
		type detail struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		var details []detail
		if json.Unmarshal(wire.Details, &details) != nil {
			var item detail
			if json.Unmarshal(wire.Details, &item) != nil {
				return errInvalidResponse
			}
			details = []detail{item}
		}
		count := 0
		knownDetails := map[string]string{}
		for _, d := range details {
			if d.Key == "DESCRIPTION" || d.Key == "EXPECTED" {
				if _, exists := knownDetails[d.Key]; exists {
					return errInvalidResponse
				}
				knownDetails[d.Key] = d.Value
			}
			if d.Key != "STATUS" {
				continue
			}
			count++
			nested := strings.ToUpper(strings.TrimSpace(d.Value))
			if count > 1 || nested == "" || (status != "" && status != nested) {
				return errInvalidResponse
			}
			status = nested
		}
		if knownDetails["EXPECTED"] == "If upgrade image version is greater than 26.1.x, the running image version also must be 26.1.x or higher." {
			match := regexp.MustCompile(`^Upgrades using xfsu to (26\.[0-9]+\.[0-9]+) is not supported from running image version (17\.[0-9]+\.[0-9]+)$`).FindStringSubmatch(knownDetails["DESCRIPTION"])
			if len(match) == 3 {
				xfsuTarget = match[1]
			}
		}
		const prefix = "HTTPS/SCP is reachable: "
		const suffix = "/ Netconf transfer failed"
		desc := knownDetails["DESCRIPTION"]
		if strings.HasPrefix(desc, prefix) && strings.HasSuffix(desc, suffix) {
			address := strings.TrimSuffix(strings.TrimPrefix(desc, prefix), suffix)
			if knownDetails["EXPECTED"] == prefix+address+". The Netconf transfer failed, likely because the device is unable to ping "+address+" through the default VRF." {
				fallbackAddress = address
			}
		}
	}
	*r = ReadinessResult(wire.plain)
	r.Status = status
	r.TransferFallbackAddress = fallbackAddress
	r.XFSUVersionPathTarget = xfsuTarget
	return nil
}

// ListReadinessResults builds a fixed-origin query. Never follow a task's
// resultLocation or URL with the controller's authentication header.
func (c *client) ListReadinessResults(ctx context.Context, deviceID string) ([]ReadinessResult, error) {
	if !validAPIID(deviceID) {
		return nil, errors.New("invalid SWIM device ID")
	}
	results, err := listInventoryQuery(ctx, c, networkDeviceImagesPath+"validationResults",
		url.Values{"networkDeviceId": {deviceID}, "operationType": {"READINESS_CHECK"}},
		func(item ReadinessResult) string { return item.ID })
	if err != nil {
		return nil, err
	}
	for _, item := range results {
		if !validAPIID(item.ID) || item.DeviceID != deviceID || item.Operation != "READINESS_CHECK" {
			return nil, errInvalidResponse
		}
	}
	return results, nil
}

// validateReadinessResults requires a completed, fresh run bound to the pinned
// task and device. Other runs are never treated as evidence for this operation.
// Task completion alone is insufficient: WARNING/SKIPPED/partial outcomes do
// not authorize an upgrade. This is one preflight gate, not mutation admission.
func validateReadinessResults(items []ReadinessResult, deviceID, taskID string, submitted, now time.Time, maxAge time.Duration) error {
	if !validAPIID(deviceID) || !validAPIID(taskID) || submitted.IsZero() || submitted.After(now) || maxAge <= 0 || now.Sub(submitted) > maxAge {
		return errors.New("SWIM readiness binding or freshness is invalid")
	}
	matched := 0
	seen := make(map[string]bool)
	for _, item := range items {
		if item.ParentID != taskID {
			continue
		}
		matched++
		if !validAPIID(item.ID) || seen[item.ID] || item.DeviceID != deviceID || item.Operation != "READINESS_CHECK" || item.Type != "PRE_VALIDATION" || strings.TrimSpace(item.Name) == "" || item.Status != "SUCCESS" || item.StartTime < submitted.UnixMilli() || item.EndTime < item.StartTime || item.EndTime > now.UnixMilli() {
			return errors.New("SWIM readiness run is incomplete, unsuccessful or mismatched")
		}
		seen[item.ID] = true
	}
	if matched == 0 {
		return errors.New("SWIM readiness run has no matching validation evidence")
	}
	return nil
}

// validateImageProduct is a necessary product check, not a substitute for
// image integrity, ROMMON, disk-space or controller readiness validation.
// Bind the appliance's exact product identifier (including supervisor when
// applicable). Its productId list is not exhaustive on all releases: the lab's
// C9300 mapping includes the device product ordinal but omits C9300-24P.
func validateImageProduct(image Image, device Device, details DeviceImageDetails) error {
	if !validAPIID(image.ID) || !validAPIID(device.ID) || details.ID != device.ID || !sameManagementIP(details.ManagementAddress, device.ManagementIP) || !validAPIID(details.NetworkDevice.ID) {
		return errors.New("SWIM image or device product binding is invalid")
	}
	for _, product := range image.ApplicableDevices {
		if product.ID == details.NetworkDevice.ID {
			return nil
		}
	}
	return errors.New("SWIM image does not explicitly list the device product identifier")
}
