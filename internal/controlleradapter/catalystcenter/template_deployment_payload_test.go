// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRegularTemplateDeploymentPayload(t *testing.T) {
	for _, mode := range []string{"valid", "false-options", "no-params", "draft", "base-version", "invalid-device", "invalid-project", "invalid-name", "nil-values", "bad-key", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			want := regularTemplateDeployment{
				base:    templateSummary{ID: "base", ProjectID: "project", Name: "Baseline"},
				version: templateVersion{ID: "committed", Number: 2}, deviceID: "device",
				copyingConfig: true, forcePush: true,
				parameters: map[string][]string{"single": {"secret-value"}, "multiple": {"a", "b"}, "empty": {}, "blank": {""}},
			}
			switch mode {
			case "false-options":
				want.copyingConfig = false
				want.forcePush = false
			case "no-params":
				want.parameters = nil
			case "draft":
				want.version.Number = 0
			case "base-version":
				want.version.ID = want.base.ID
			case "invalid-device":
				want.deviceID = "../device"
			case "invalid-project":
				want.base.ProjectID = ""
			case "invalid-name":
				want.base.Name = ""
			case "nil-values":
				want.parameters["nil"] = nil
			case "bad-key":
				want.parameters["bad\nkey"] = []string{"secret-value"}
			case "oversize":
				want.parameters["large"] = []string{strings.Repeat("s", 1<<20)}
			}
			raw, err := regularTemplateDeploymentPayload(want)
			valid := mode == "valid" || mode == "false-options" || mode == "no-params"
			if (err == nil) != valid {
				t.Fatal(mode, "unexpected validation result")
			}
			if !valid {
				if len(raw) != 0 || strings.Contains(err.Error(), "secret-value") {
					t.Fatal("request material leaked on rejection")
				}
				return
			}
			var got map[string]any
			if json.Unmarshal(raw, &got) != nil {
				t.Fatal("invalid JSON")
			}
			params := map[string]any{"single": "secret-value", "multiple": []any{"a", "b"}, "empty": []any{}, "blank": ""}
			if mode == "no-params" {
				params = map[string]any{}
			}
			expected := map[string]any{
				"templateId": "base", "copyingConfig": want.copyingConfig, "forcePushTemplate": want.forcePush, "isComposite": false,
				"targetInfo": []any{map[string]any{"id": "device", "type": "MANAGED_DEVICE_UUID", "versionedTemplateId": "committed", "params": params,
					"resourceParams": []any{map[string]any{"type": "MANAGED_DEVICE_UUID", "scope": "RUNTIME", "value": "device"}}}},
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatal("native payload differs from expected contract")
			}
		})
	}
}
