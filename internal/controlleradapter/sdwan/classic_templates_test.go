// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"reflect"
	"testing"
)

func TestClassicTemplateFixtureAndDefaults(t *testing.T) {
	resources := parcelFixtures(t, "classic-templates")
	if len(resources) != 3 {
		t.Fatal("expected CLI, global and multicast templates")
	}
	for _, r := range resources {
		if _, err := normalized(r.Body); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-templates.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Compile(resolved)
	if err != nil || !reflect.DeepEqual(resources, again) {
		t.Fatal("resolver changed classic intent", err)
	}
	global := resources[1].Body["templateDefinition"].(map[string]any)
	ip := global["services-global"].(map[string]any)["services-ip"].(map[string]any)
	if ip["http-server"].(map[string]any)["vipValue"] != "false" {
		t.Fatal("provider Boolean string encoding lost")
	}
}
func TestClassicTemplateValidation(t *testing.T) {
	for _, input := range []map[string]any{
		{"name": "test", "description": "test", "ssh_version": float64(3)},
		{"name": "test", "description": "test", "cdp": "true"},
		{"name": "test", "description": "test", "unknown": true},
	} {
		if _, err := compileClassicTemplates("global_settings_templates", []map[string]any{input}, nil); err == nil {
			t.Fatal("invalid global template accepted")
		}
	}
}
func TestLiveClassicNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "classic-templates"))
}
func TestLiveClassicLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "classic-templates"))
}
func TestLiveClassicOracle(t *testing.T) { liveParcelOracle(t, parcelFixtures(t, "classic-templates")) }

func TestClassicCellularFixture(t *testing.T) {
	resources := parcelFixtures(t, "classic-cellular")
	if len(resources) != 2 {
		t.Fatal("expected two cellular templates")
	}
	for _, r := range resources {
		if _, err := normalized(r.Body); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-cellular.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Compile(resolved)
	if err != nil || !reflect.DeepEqual(resources, again) {
		t.Fatal("cellular defaults changed during resolution", err)
	}
}
func TestLiveCellularNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "classic-cellular"))
}
func TestLiveCellularLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "classic-cellular"))
}
func TestLiveCellularOracle(t *testing.T) { liveParcelOracle(t, parcelFixtures(t, "classic-cellular")) }

func TestClassicCollectionsRemainStrict(t *testing.T) {
	for _, source := range []string{
		"sdwan:\n  edge_feature_templates:\n    unsupported_templates: []\n",
		"defaults:\n  sdwan:\n    edge_feature_templates:\n      unsupported_templates: {}\nsdwan:\n  edge_feature_templates:\n    cli_templates:\n      - name: test\n        description: test\n        cli_config: test\n",
	} {
		if _, err := Compile([]byte(source)); err == nil {
			t.Fatal("unknown template collection accepted")
		}
	}
}
