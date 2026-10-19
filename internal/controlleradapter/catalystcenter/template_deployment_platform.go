// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Keep template applicability metadata local to deployment; adding it to the
// shared inventory model would alter unrelated operation snapshot digests.
type templateDevicePlatform struct {
	ID              string `json:"id"`
	Serial          string `json:"serialNumber"`
	Family          string `json:"family"`
	Series          string `json:"series"`
	Type            string `json:"type"`
	SoftwareType    string `json:"softwareType"`
	SoftwareVersion string `json:"softwareVersion"`
	Reachability    string `json:"reachabilityStatus"`
}

func (c *client) templateDevicePlatform(ctx context.Context, device Device) (templateDevicePlatform, error) {
	var zero templateDevicePlatform
	if !validAPIID(device.ID) {
		return zero, errInvalidResponse
	}
	raw, err := c.get(ctx, devicesPath+"/"+device.ID, nil, 200)
	if err != nil {
		return zero, err
	}
	var body struct {
		Response *templateDevicePlatform `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response == nil {
		return zero, errInvalidResponse
	}
	got := *body.Response
	serial, err := topologyidentity.CanonicalPhysicalIdentity(got.Serial)
	expected, expectedErr := topologyidentity.CanonicalPhysicalIdentity(device.Serial)
	if err != nil || expectedErr != nil || serial != expected || got.ID != device.ID || got.Family != device.Family || got.Reachability != "Reachable" || got.SoftwareType == "" || got.SoftwareVersion == "" || (device.SoftwareVersion != "" && device.SoftwareVersion != got.SoftwareVersion) {
		return zero, errConfigurationStale
	}
	got.Serial = serial
	return got, nil
}

// Exact family/series/type/software constraints are supported. Version patterns
// and software variants need explicit appliance-qualified matching semantics.
func validateTemplatePlatform(body map[string]any, device templateDevicePlatform) error {
	software, ok := body["softwareType"].(string)
	if !ok || software == "" || software != device.SoftwareType {
		return errConfigurationTransition
	}
	for _, key := range []string{"softwareVersion", "softwareVariant"} {
		if value, present := body[key]; present && value != nil {
			value, ok := value.(string)
			if !ok {
				return errInvalidResponse
			}
			if value != "" {
				if key == "softwareVariant" || strings.ContainsAny(value, "*[]()|^$\\") || value != device.SoftwareVersion {
					return errConfigurationTransition
				}
			}
		}
	}
	types, ok := body["deviceTypes"].([]any)
	if !ok || len(types) == 0 || len(types) > 1000 {
		return errInvalidResponse
	}
	matched := false
	for _, entry := range types {
		row, ok := entry.(map[string]any)
		if !ok {
			return errInvalidResponse
		}
		family, ok := row["productFamily"].(string)
		if !ok || family == "" {
			return errInvalidResponse
		}
		matches := family == device.Family
		for key, actual := range map[string]string{"productSeries": device.Series, "productType": device.Type} {
			if value, present := row[key]; present && value != nil {
				expected, ok := value.(string)
				if !ok {
					return errInvalidResponse
				}
				if expected != "" && expected != actual {
					matches = false
				}
			}
		}
		matched = matched || matches
	}
	if !matched {
		return errConfigurationTransition
	}
	return nil
}
