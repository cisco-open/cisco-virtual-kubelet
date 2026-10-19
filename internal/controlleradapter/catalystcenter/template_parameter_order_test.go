// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTemplateParameterOrderBaseline(t *testing.T) {
	for _, mode := range []string{"unordered", "custom", "missing", "null", "value", "opaque", "id", "large-number", "flag-change", "duplicate", "missing-name", "bad-record", "bad-collection", "bad-flag", "binding-filter", "binding-context", "binding-format", "binding-removed"} {
		t.Run(mode, func(t *testing.T) {
			body := map[string]any{"customParamsOrder": false, "templateParams": []any{map[string]any{"parameterName": "z", "id": "z-id", "defaultValue": "private original", "opaque": map[string]any{"value": "private native"}}, map[string]any{"parameterName": "a", "id": "a-id"}}, "opaqueCounter": json.Number("9007199254740993")}
			if strings.HasPrefix(mode, "binding-") {
				body["templateParams"].([]any)[0].(map[string]any)["binding"] = templateCommonSettingsBinding(t, "RADIUS")
			}
			switch mode {
			case "custom":
				body["customParamsOrder"] = true
			case "missing":
				delete(body, "customParamsOrder")
			case "null":
				body["customParamsOrder"] = nil
			}
			raw, _ := json.Marshal(body)
			first, err := decodeTemplateNativeJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			rows := body["templateParams"].([]any)
			body["templateParams"] = []any{rows[1], rows[0]}
			valid := true
			equal := mode == "unordered"
			switch mode {
			case "binding-filter":
				rows[0].(map[string]any)["binding"] = templateCommonSettingsBinding(t, "TACACS")
			case "binding-context":
				rows[0].(map[string]any)["binding"] = strings.ReplaceAll(templateCommonSettingsBinding(t, "RADIUS"), `"scope":"RUNTIME"`, `"scope":"STATIC"`)
			case "binding-format":
				rows[0].(map[string]any)["binding"] = " " + templateCommonSettingsBinding(t, "RADIUS")
			case "binding-removed":
				delete(rows[0].(map[string]any), "binding")
			case "value":
				rows[0].(map[string]any)["defaultValue"] = "private changed"
			case "opaque":
				rows[0].(map[string]any)["opaque"].(map[string]any)["value"] = "private changed"
			case "id":
				rows[0].(map[string]any)["id"] = "replacement"
			case "large-number":
				body["opaqueCounter"] = json.Number("9007199254740992")
			case "flag-change":
				body["customParamsOrder"] = true
			case "duplicate":
				rows[0].(map[string]any)["parameterName"] = "a"
				valid = false
			case "missing-name":
				delete(rows[0].(map[string]any), "parameterName")
				valid = false
			case "bad-record":
				body["templateParams"] = []any{"invalid"}
				valid = false
			case "bad-collection":
				body["templateParams"] = map[string]any{}
				valid = false
			case "bad-flag":
				body["customParamsOrder"] = "false"
				valid = false
			}
			raw, _ = json.Marshal(body)
			second, err := decodeTemplateNativeJSON(raw)
			if !valid {
				if err == nil || second != nil {
					t.Fatal("unknown parameter identity returned a usable snapshot")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			comparison := &templateInputComparison{key: bytes.Repeat([]byte("k"), 32), keyID: strings.Repeat("a", 64)}
			identity := templateCreatorResource("r", "Lab", "base") + ":i:" + templateComparisonTag + ":" + comparison.keyID
			a, err := templateNativeMAC(comparison, identity, first)
			if err != nil {
				t.Fatal(err)
			}
			b, err := templateNativeMAC(comparison, identity, second)
			if err != nil {
				t.Fatal(err)
			}
			if (a == b) != equal {
				t.Fatal("canonical keyed snapshot discarded meaningful state or retained unordered noise")
			}
			if first.(map[string]any)["opaqueCounter"] != json.Number("9007199254740993") {
				t.Fatal("native integer precision lost")
			}
		})
	}
}

func TestTemplateParameterOrderSubmission(t *testing.T) {
	for _, mode := range []string{"unordered", "custom-order", "flag-missing", "flag-null", "value-changed", "unknown-field", "parameter-id", "custom-flag", "duplicate-name", "malformed-row", "large-native-number", "binding-filter", "binding-context", "binding-format", "binding-removed"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Lab","dayn_templates":[{"name":"base","language":"JINJA","software_type":"IOS-XE","device_types":[{"product_family":"Switches and Hubs"}],"variables":[{"name":"b","data_type":"STRING"},{"name":"a","data_type":"INTEGER"}]}]}]}}}`)
			files := map[string]string{"base.j2": "! {{ b }} {{ a }}\n"}
			intent, err := parseTemplateIntent(raw, files)
			if err != nil {
				t.Fatal(err)
			}
			var reads, posts atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					posts.Add(1)
					if r.Method != "POST" || r.URL.Path != templateVersionsPath {
						t.Error("unexpected native mutation")
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				switch r.URL.Path {
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
				case templatesPath:
					fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"base"}]`)
				case templateVersionsPath + "/base":
					fmt.Fprint(w, `[]`)
				case templatesPath + "/base":
					read := reads.Add(1)
					encoded, _ := json.Marshal(intent.definitions[0].payload)
					var body map[string]any
					json.Unmarshal(encoded, &body)
					body["id"] = "base"
					body["projectId"] = "project"
					body["customParamsOrder"] = false
					params := body["templateParams"].([]any)
					if strings.HasPrefix(mode, "binding-") {
						params[0].(map[string]any)["binding"] = templateCommonSettingsBinding(t, "RADIUS")
					}
					for _, row := range params {
						p := row.(map[string]any)
						p["id"] = "id-" + p["parameterName"].(string)
						p["nativeOpaque"] = "private native"
					}
					switch mode {
					case "custom-order":
						body["customParamsOrder"] = true
					case "flag-missing":
						delete(body, "customParamsOrder")
					case "flag-null":
						body["customParamsOrder"] = nil
					}
					if read > 1 {
						body["templateParams"] = []any{params[1], params[0]}
						switch mode {
						case "binding-filter":
							params[0].(map[string]any)["binding"] = templateCommonSettingsBinding(t, "TACACS")
						case "binding-context":
							params[0].(map[string]any)["binding"] = strings.ReplaceAll(templateCommonSettingsBinding(t, "RADIUS"), `"scope":"RUNTIME"`, `"scope":"STATIC"`)
						case "binding-format":
							params[0].(map[string]any)["binding"] = " " + templateCommonSettingsBinding(t, "RADIUS")
						case "binding-removed":
							delete(params[0].(map[string]any), "binding")
						case "value-changed":
							params[0].(map[string]any)["defaultValue"] = "private changed"
						case "unknown-field":
							params[0].(map[string]any)["nativeOpaque"] = "private changed"
						case "parameter-id":
							params[0].(map[string]any)["id"] = "replacement"
						case "custom-flag":
							body["customParamsOrder"] = true
						case "duplicate-name":
							params[0].(map[string]any)["parameterName"] = "a"
						case "malformed-row":
							body["templateParams"] = []any{"invalid"}
						}
					}
					if mode == "large-native-number" {
						body["nativeCounter"] = json.Number("9007199254740992")
						if read > 1 {
							body["nativeCounter"] = json.Number("9007199254740993")
						}
					}
					json.NewEncoder(w).Encode(body)
				default:
					t.Error("unexpected native read", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ex, err := c.compileConfigurationExecutor(raw, "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			write, err := ex.next(context.Background())
			if err != nil || write == nil {
				t.Fatal("commit planning failed", err)
			}
			task, err := write.submit(context.Background())
			if mode == "unordered" {
				if err != nil || task.ID != "task" || posts.Load() != 1 {
					t.Fatal("harmless unordered response prevented dispatch", err)
				}
			} else {
				if err == nil || task != (Task{}) || posts.Load() != 0 || write.noDispatch == nil || write.noDispatch(err) == nil {
					t.Fatal("meaningful/unknown native change was not refused before dispatch")
				}
			}
			if _, err := write.submit(context.Background()); !errors.Is(err, errConfigurationTransition) {
				t.Fatal("spent write replayed")
			}
		})
	}
}
