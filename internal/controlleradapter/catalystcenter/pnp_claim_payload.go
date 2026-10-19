// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "encoding/json"

type pnpClaimReferences struct{ deviceID, siteID, imageID, templateID string }

// Requires independently resolved native references. This maps the pinned
// module's claim fields; it does not resolve names, grant ownership or submit.
func pnpClaimPayload(want pnpDeviceIntent, refs pnpClaimReferences) ([]byte, error) {
	members, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	if !validAPIID(refs.deviceID) || !validAPIID(refs.siteID) || refs.imageID != "" && !validAPIID(refs.imageID) || refs.templateID != "" && !validAPIID(refs.templateID) {
		return nil, errInvalidResponse
	}
	if want.ImageID != "" && refs.imageID != want.ImageID || want.ImageName != "" && want.ImageID == "" && refs.imageID == "" || want.ImageID == "" && want.ImageName == "" && refs.imageID != "" || want.Template != nil && refs.templateID == "" || want.Template == nil && refs.templateID != "" {
		return nil, errConfigurationStale
	}
	typ := want.Type
	if typ == "" {
		typ = "Default"
	}
	if len(members) > 1 {
		typ = "StackSwitch"
	}
	skip := true
	if want.ImageSkip != nil {
		skip = *want.ImageSkip
	}
	image := map[string]any{"skip": skip}
	if refs.imageID != "" || len(members) > 1 {
		image["imageId"] = refs.imageID
	}
	parameters := []map[string]string{}
	if want.Template != nil {
		for _, v := range want.Template.Variables {
			parameters = append(parameters, map[string]string{"key": v.Name, "value": v.Value})
		}
	}
	config := map[string]any{"configParameters": parameters}
	if refs.templateID != "" || len(members) > 1 {
		config["configId"] = refs.templateID
	}
	body := map[string]any{"deviceId": refs.deviceID, "siteId": refs.siteID, "type": typ, "hostname": want.Name, "imageInfo": image, "configInfo": config}
	if want.RFProfile != "" {
		body["rfProfile"] = want.RFProfile
	}
	if want.Stack != nil {
		if want.Stack.TopSerial != "" {
			body["topOfStackSerialNumber"] = want.Stack.TopSerial
		}
		if want.Stack.Cabling != "" {
			body["cablingScheme"] = want.Stack.Cabling
		}
	}
	return json.Marshal(body)
}
