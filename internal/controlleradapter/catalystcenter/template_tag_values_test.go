// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestTagConflictingValueSelectorsPreflight(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid later rule reached the native API")
		w.WriteHeader(400)
	})
	for _, section := range []string{"templates", "inventory"} {
		model := []byte(fmt.Sprintf(`{"catalyst_center":{%q:{"tags":[{"name":"earlier"},{"name":"invalid","dynamic_rules":[{"member_type":"networkdevice","operation":"IN","name":"hostname","value":"single","values":["multiple"]}]}]}}}`, section))
		if _, err := c.compileConfigurationExecutor(model, section); err == nil {
			t.Fatal("mutually exclusive selectors accepted", section)
		}
	}
}

func TestTagMultipleValuesMappingAndFrozenRequest(t *testing.T) {
	values := []any{"first", "second"}
	desired, err := parseTemplateTags([]any{map[string]any{"name": "tag", "system_tag": false, "dynamic_rules": []any{map[string]any{"member_type": "networkdevice", "operation": "IN", "name": "hostname", "values": values}}}})
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "GET" {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		var body map[string]any
		if r.Method != "POST" || r.URL.Path != tagsPath || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("incorrect tag request")
		}
		rules := body["dynamicRules"].([]any)[0].(map[string]any)["rules"].(map[string]any)
		if !reflect.DeepEqual(rules["values"], []any{"first", "second"}) || rules["value"] != nil || rules["operation"] != "IN" || rules["name"] != "hostname" {
			t.Error("value-list mapping or frozen input changed", rules)
		}
		writes++
		fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
	})
	write, err := (&tagExecutor{client: c, desired: desired}).next(context.Background())
	if err != nil || write == nil {
		t.Fatal(err)
	}
	values[0] = "caller-mutated"
	if _, err := write.submit(context.Background()); err != nil || writes != 1 {
		t.Fatal("value-list submission failed", err)
	}
}
