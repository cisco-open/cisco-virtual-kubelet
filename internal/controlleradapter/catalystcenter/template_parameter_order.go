// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "sort"

// The appliance permutes parameter records when customParamsOrder is false.
// Canonicalize only that explicit unordered case, preserving every field/value
// and all record identities. Missing/true flags retain the original array order.
// Apply the same view to submission observations and keyed native baselines.
func canonicalizeTemplateParameterOrder(body map[string]any) error {
	raw, present := body["customParamsOrder"]
	if !present || raw == nil {
		return nil
	}
	custom, ok := raw.(bool)
	if !ok {
		return errInvalidResponse
	}
	if custom {
		return nil
	}
	raw = body["templateParams"]
	if raw == nil {
		return nil
	}
	params, ok := raw.([]any)
	if !ok || len(params) > 1000 {
		return errInvalidResponse
	}
	names := map[string]bool{}
	for _, entry := range params {
		parameter, ok := entry.(map[string]any)
		if !ok {
			return errInvalidResponse
		}
		name, ok := parameter["parameterName"].(string)
		if !ok || name == "" || names[name] {
			return errInvalidResponse
		}
		names[name] = true
	}
	// Copy the collection before ordering; do not mutate another view's slice.
	ordered := append([]any{}, params...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].(map[string]any)["parameterName"].(string) < ordered[j].(map[string]any)["parameterName"].(string)
	})
	body["templateParams"] = ordered
	return nil
}
