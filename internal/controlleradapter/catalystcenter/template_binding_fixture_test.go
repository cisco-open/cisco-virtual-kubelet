// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

// Same opaque binding contract for public, dispatch and native qualification.
// Runtime values remain controller-owned; the fixture does not resolve them.
func templateCommonSettingsBinding(t *testing.T, filter string) string {
	t.Helper()
	binding := map[string]any{
		"provider": "CommonSettings", "source": "CommonSettings", "entity": "aaa.endpoint.server", "variableName": "__aaaendpointserver",
		"attribute": "ipAddress", "attributeToDisplay": "ipAddress", "attributesAvailable": []string{"ipAddress", "protocol", "encryptionScheme"},
		"params": []any{map[string]any{"type": "SITE_UUID", "scope": "RUNTIME", "value": nil}, map[string]any{"type": "MANAGED_DEVICE_UUID", "scope": "RUNTIME", "value": nil}},
		"value":  nil, "errorMessage": nil, "sortAttribute": nil, "sortValues": nil,
		"filterType": "contains", "filterValue": filter, "filterByAttribute": "protocol", "filterSupported": true,
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatal("binding fixture encoding failed")
	}
	return string(encoded)
}
