// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"strings"
	"testing"
)

func TestUpstreamBannerModel(t *testing.T) {
	fixture := []byte("sdwan:\n  edge_feature_templates:\n    banner_templates:\n      - name: test\n        description: fixture\n        login: \"hello\\n\"\n        motd_variable: banner_motd\n")
	resources, err := Compile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	r := resources[0]
	if len(r.Body["deviceType"].([]string)) != 11 {
		t.Fatal("upstream device defaults missing")
	}
	def := r.Body["templateDefinition"].(map[string]any)
	if def["login"].(map[string]any)["vipValue"] != "hello\n" {
		t.Fatal("newline changed")
	}
	if def["motd"].(map[string]any)["vipVariableName"] != "banner_motd" {
		t.Fatal("variable mapping")
	}
	for _, bad := range []string{strings.Replace(string(fixture), "login:", "logni:", 1), string(fixture) + "  feature_profiles: {unsupported_profiles: []}\n", string(fixture) + "  extra: true\n"} {
		if _, err := Compile([]byte(bad)); err == nil {
			t.Fatal("silently accepted unsupported field")
		}
	}
}
func TestBannerNullEmptyAndVariablePrecedence(t *testing.T) {
	empty := ""
	variable := "var"
	if bannerValue(nil, nil)["vipType"] != "ignore" {
		t.Fatal("null")
	}
	if bannerValue(&empty, nil)["vipType"] != "constant" {
		t.Fatal("empty must remain explicit")
	}
	if bannerValue(&empty, &variable)["vipType"] != "variableName" {
		t.Fatal("provider variable precedence")
	}
}
func TestBannerDefaultsOverride(t *testing.T) {
	r, e := Compile([]byte(`defaults:
  sdwan:
    edge_feature_templates:
      banner_templates:
        device_types: [C8000V]
sdwan:
  edge_feature_templates:
    banner_templates:
      - name: fixture
        description: example
`))
	if e != nil {
		t.Fatal(e)
	}
	if len(r[0].Body["deviceType"].([]string)) != 1 {
		t.Fatal("override ignored")
	}
}

func TestResolvePreservesDeploymentSemantics(t *testing.T) {
	source := []byte("sdwan:\n  edge_feature_templates:\n    banner_templates:\n      - name: fixture\n        description: example\n")
	before, e := Compile(source)
	if e != nil {
		t.Fatal(e)
	}
	resolved, e := Resolve(source)
	if e != nil {
		t.Fatal(e)
	}
	after, e := Compile(resolved)
	if e != nil {
		t.Fatal(e)
	}
	same, e := Equal(before[0], after[0].Body)
	if e != nil || !same {
		t.Fatal("resolution changed intent", e)
	}
	if !strings.Contains(string(resolved), "device_types") {
		t.Fatal("defaults not expanded")
	}
}
