// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPnPRemovalContentProjection(t *testing.T) {
	for _, body := range []string{
		`{"dayZeroConfig":{"config":"private-value"}}`,
		`{"deviceInfo":{"projectId":"project"}}`,
		`{"deviceInfo":{"workflowName":"workflow"}}`,
		`{"deviceInfo":{"imageFile":"image.bin"}}`,
		`{"deviceInfo":{"pnpProfileList":[{}]}}`,
		`{"deviceInfo":{"tags":{"owned":"elsewhere"}}}`,
		`{"workflowParameters":{"licenseLevel":"NETWORK_ADVANTAGE"}}`,
		`{"workflowParameters":{"unknown":null}}`,
		`{"workflowParameters":{"configList":[{}]}}`,
		`{"dayZeroConfig":{"unknown":""}}`,
		`{"dayZeroConfig":[]}`,
		`{"dayZeroConfigPreview":{"config":"private-value"}}`,
		`{"deviceInfo":{"aaaCredentials":{"password":"private-value"}}}`,
		`{"deviceInfo":{"aaaCredentials":{"unknown":null}}}`,
		`{"deviceInfo":{"httpHeaders":[{"value":"private-value"}]}}`,
		`{"_cvkRemovalContentBlocked":false,"dayZeroConfig":{"config":"private-value"}}`,
	} {
		var projected pnpRemovalDevice
		if err := json.Unmarshal([]byte(body), &projected); err != nil {
			t.Fatal(err)
		}
		if !projected.ContentBlocked {
			t.Fatal("unsafe/unknown content treated as empty")
		}
		safe, err := json.Marshal(projected)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(safe), "private-value") {
			t.Fatal("private contents retained")
		}
		var restored pnpRemovalDevice
		if err := json.Unmarshal(safe, &restored); err != nil || !restored.ContentBlocked {
			t.Fatal("refusal bit lost across safe paging", err)
		}
	}
	for _, body := range []string{`{}`, `{"dayZeroConfig":null}`, `{"dayZeroConfig":{}}`, `{"dayZeroConfig":{"config":""},"dayZeroConfigPreview":{},"deviceInfo":{"aaaCredentials":{"username":null,"password":""},"httpHeaders":[]}}`} {
		var projected pnpRemovalDevice
		if err := json.Unmarshal([]byte(body), &projected); err != nil || projected.ContentBlocked {
			t.Fatal("empty content refused", err)
		}
	}
}
