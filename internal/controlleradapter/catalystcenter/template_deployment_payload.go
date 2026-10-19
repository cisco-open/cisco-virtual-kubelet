// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
)

// Parameters are transient request material: they may contain secrets and must
// never appear as plaintext or unkeyed value digests in operation IDs, resource
// keys, status or diagnostics. Explicit private comparison emits keyed MACs only.
type regularTemplateDeployment struct {
	base                     templateSummary
	version                  templateVersion
	deviceID                 string
	copyingConfig, forcePush bool
	parameters               map[string][]string
}

// The caller resolves defaults, validates committed content and admits the
// physical target before submission. Redeploy is reconciliation policy, not a
// native request field. This builder neither submits nor chooses to replay.
func regularTemplateDeploymentPayload(want regularTemplateDeployment) ([]byte, error) {
	if !validAPIID(want.base.ID) || !validAPIID(want.base.ProjectID) || !validProfileName(want.base.Name) || !validAPIID(want.deviceID) || !validAPIID(want.version.ID) || want.version.ID == want.base.ID || want.version.Number < 1 || len(want.parameters) > 1000 {
		return nil, errInvalidResponse
	}
	params, err := templateDeploymentParameters(want.parameters)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"templateId":        want.base.ID,
		"copyingConfig":     want.copyingConfig,
		"forcePushTemplate": want.forcePush,
		"isComposite":       false,
		"targetInfo": []any{map[string]any{
			"id":                  want.deviceID,
			"type":                "MANAGED_DEVICE_UUID",
			"versionedTemplateId": want.version.ID,
			"params":              params,
			"resourceParams": []any{map[string]string{
				"type": "MANAGED_DEVICE_UUID", "scope": "RUNTIME", "value": want.deviceID,
			}},
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 1<<20 {
		return nil, errInvalidResponse
	}
	return raw, nil
}

// Shared transient parameter encoding for regular and composite members.
func templateDeploymentParameters(parameters map[string][]string) (map[string]any, error) {
	if len(parameters) > 1000 {
		return nil, errInvalidResponse
	}
	params := make(map[string]any, len(parameters))
	for name, values := range parameters {
		if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") || values == nil || len(values) > 1000 {
			return nil, errInvalidResponse
		}
		// The pinned provider flattens singleton lists into strings; multiple
		// values and an explicit empty list remain arrays. Do not stringify
		// arrays or confuse an empty list with an absent parameter.
		if len(values) == 1 {
			params[name] = values[0]
		} else {
			params[name] = append([]string{}, values...)
		}
	}
	return params, nil
}
