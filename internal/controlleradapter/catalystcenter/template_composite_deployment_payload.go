// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "encoding/json"

// Composite deployment remains a single physical target. Ordered member
// bindings must be resolved from committed native content before calling this
// builder. This is request construction only, not admission or execution.
type compositeTemplateDeployment struct {
	base      templateSummary
	version   templateVersion
	deviceID  string
	forcePush bool
	members   []regularTemplateDeployment
}

// Matches nac-catalystcenter 0.5.0 composite_template_deploy and
// unmanaged_composite_template_deploy via provider 0.6.1: templateId is the
// committed UUID and mainTemplateId is the base UUID, including each member.
// Redeploy stays journal policy and is not sent as a native request field.
func compositeTemplateDeploymentPayload(want compositeTemplateDeployment) ([]byte, error) {
	if !validAPIID(want.base.ID) || !validAPIID(want.base.ProjectID) || !validProfileName(want.base.Name) || !validAPIID(want.deviceID) || !validAPIID(want.version.ID) || want.version.ID == want.base.ID || want.version.Number < 1 || len(want.members) == 0 || len(want.members) > 1000 {
		return nil, errInvalidResponse
	}
	target := func(params map[string]any) []any {
		return []any{map[string]any{
			"id": want.deviceID, "type": "MANAGED_DEVICE_UUID", "params": params,
			"resourceParams": []any{map[string]string{"type": "MANAGED_DEVICE_UUID", "scope": "RUNTIME", "value": want.deviceID}},
		}}
	}
	seen := map[string]bool{want.base.ID: true, want.version.ID: true}
	members := make([]any, 0, len(want.members))
	for _, member := range want.members {
		if !validAPIID(member.base.ID) || !validAPIID(member.base.ProjectID) || !validProfileName(member.base.Name) || !validAPIID(member.version.ID) || member.base.ID == member.version.ID || member.version.Number < 1 || member.deviceID != want.deviceID || seen[member.base.ID] || seen[member.version.ID] {
			return nil, errInvalidResponse
		}
		seen[member.base.ID], seen[member.version.ID] = true, true
		params, err := templateDeploymentParameters(member.parameters)
		if err != nil {
			return nil, err
		}
		members = append(members, map[string]any{
			"templateId": member.version.ID, "mainTemplateId": member.base.ID,
			"isComposite": false, "copyingConfig": member.copyingConfig,
			"forcePushTemplate": member.forcePush, "targetInfo": target(params),
		})
	}
	raw, err := json.Marshal(map[string]any{
		"templateId": want.version.ID, "mainTemplateId": want.base.ID,
		"isComposite": true, "forcePushTemplate": want.forcePush,
		"memberTemplateDeploymentInfo": members, "targetInfo": target(map[string]any{}),
	})
	if err != nil || len(raw) > 1<<20 {
		return nil, errInvalidResponse
	}
	return raw, nil
}
