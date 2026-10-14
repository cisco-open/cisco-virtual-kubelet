// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
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

func TestClassicBFDTree(t *testing.T) {
	resources := parcelFixtures(t, "classic-bfd")
	if len(resources) != 1 {
		t.Fatal("expected BFD template")
	}
	if _, err := normalized(resources[0].Body); err != nil {
		t.Fatal(err)
	}
	tree := resources[0].Body["templateDefinition"].(map[string]any)["color"].(map[string]any)
	rows := tree["vipValue"].([]any)
	if rows[1].(map[string]any)["vipOptional"] != true {
		t.Fatal("optional row flag lost")
	}
	for _, colors := range []any{[]any{map[string]any{"hello_interval": float64(1000)}}, []any{map[string]any{"color": "blue"}, map[string]any{"color": "blue"}}} {
		input := map[string]any{"name": "test", "description": "test", "colors": colors}
		if _, err := compileClassicTemplates("bfd_templates", []map[string]any{input}, nil); err == nil {
			t.Fatal("invalid row identity accepted")
		}
	}
}
func TestLiveBFDNamesAbsent(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, "classic-bfd")) }
func TestLiveBFDLifecycle(t *testing.T)   { liveParcelLifecycle(t, parcelFixtures(t, "classic-bfd")) }
func TestLiveBFDOracle(t *testing.T)      { liveParcelOracle(t, parcelFixtures(t, "classic-bfd")) }

func TestClassicIGMPNestedTrees(t *testing.T) {
	resources := parcelFixtures(t, "classic-igmp")
	if len(resources) != 1 {
		t.Fatal("expected IGMP template")
	}
	if _, err := normalized(resources[0].Body); err != nil {
		t.Fatal(err)
	}
}
func TestLiveIGMPNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "classic-igmp"))
}
func TestLiveIGMPLifecycle(t *testing.T) { liveParcelLifecycle(t, parcelFixtures(t, "classic-igmp")) }
func TestLiveIGMPOracle(t *testing.T)    { liveParcelOracle(t, parcelFixtures(t, "classic-igmp")) }

func TestClassicNTPLists(t *testing.T) {
	resources := parcelFixtures(t, "classic-ntp")
	if len(resources) != 1 {
		t.Fatal("expected NTP template")
	}
	if _, err := normalized(resources[0].Body); err != nil {
		t.Fatal(err)
	}
	for _, r := range resources {
		data := r.Body["templateDefinition"].(map[string]any)
		trusted := data["keys"].(map[string]any)["trusted"].(map[string]any)
		if trusted["vipObjectType"] != "list" {
			t.Fatal("trusted keys must be a VIP list")
		}
	}
}
func TestLiveNTPNamesAbsent(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, "classic-ntp")) }
func TestLiveNTPLifecycle(t *testing.T)   { liveParcelLifecycle(t, parcelFixtures(t, "classic-ntp")) }
func TestLiveNTPOracle(t *testing.T)      { liveParcelOracle(t, parcelFixtures(t, "classic-ntp")) }

func TestClassicOMPConditionalRoute(t *testing.T) {
	resources := parcelFixtures(t, "classic-omp")
	if len(resources) != 1 {
		t.Fatal("expected OMP template")
	}
	if _, err := normalized(resources[0].Body); err != nil {
		t.Fatal(err)
	}
	data := resources[0].Body["templateDefinition"].(map[string]any)
	encoded, _ := json.Marshal(data)
	if !strings.Contains(string(encoded), "external") {
		t.Fatal("OSPF external advertisement lost")
	}
	for _, holdtime := range []float64{1, 2, 65536} {
		_, err := compileClassicTemplates("omp_templates", []map[string]any{{"name": "test", "description": "test", "holdtime": holdtime}}, nil)
		if err == nil {
			t.Fatal("invalid OMP holdtime accepted")
		}
	}
}
func TestLiveOMPNamesAbsent(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, "classic-omp")) }
func TestLiveOMPLifecycle(t *testing.T)   { liveParcelLifecycle(t, parcelFixtures(t, "classic-omp")) }
func TestLiveOMPOracle(t *testing.T)      { liveParcelOracle(t, parcelFixtures(t, "classic-omp")) }

func TestClassicPIMFixture(t *testing.T) {
	resources := parcelFixtures(t, "classic-pim")
	if len(resources) != 1 {
		t.Fatal("expected PIM template")
	}
	if _, err := normalized(resources[0].Body); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"name": "test", "description": "test", "rp_addresses": []any{map[string]any{"ip_address": "192.0.2.1", "optional": false}}}
	if _, err := compileClassicTemplates("pim_templates", []map[string]any{input}, nil); err == nil {
		t.Fatal("provider-ignored field accepted")
	}
}
func TestLivePIMNamesAbsent(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, "classic-pim")) }
func TestLivePIMLifecycle(t *testing.T)   { liveParcelLifecycle(t, parcelFixtures(t, "classic-pim")) }
func TestLivePIMOracle(t *testing.T)      { liveParcelOracle(t, parcelFixtures(t, "classic-pim")) }

func TestClassicApplicationWrappers(t *testing.T) {
	resources := parcelFixtures(t, "classic-applications")
	if len(resources) != 2 {
		t.Fatal("expected two application templates")
	}
	for _, r := range resources {
		if _, err := normalized(r.Body); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-applications.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Compile(resolved)
	if err != nil || !reflect.DeepEqual(resources, again) {
		t.Fatal("resolution changes application payloads", err)
	}
}
func TestLiveApplicationsNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "classic-applications"))
}
func TestLiveApplicationsLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "classic-applications"))
}
func TestLiveApplicationsOracle(t *testing.T) {
	liveParcelOracle(t, parcelFixtures(t, "classic-applications"))
}

func TestClassicDHCPRangesAndRows(t *testing.T) {
	resources := parcelFixtures(t, "classic-dhcp")
	if len(resources) != 1 {
		t.Fatal("expected DHCP template")
	}
	if _, err := normalized(resources[0].Body); err != nil {
		t.Fatal(err)
	}
	for _, span := range []map[string]any{{"from": "192.0.2.9", "to": "192.0.2.1"}, {"from": "192.0.2.1"}, {"from": "bad", "to": "192.0.2.1"}} {
		input := map[string]any{"name": "test", "description": "test", "exclude_addresses_ranges": []any{span}}
		if _, err := compileClassicTemplates("dhcp_server_templates", []map[string]any{input}, nil); err == nil {
			t.Fatal("invalid address range accepted")
		}
	}
}
func TestLiveDHCPNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "classic-dhcp"))
}
func TestLiveDHCPLifecycle(t *testing.T) { liveParcelLifecycle(t, parcelFixtures(t, "classic-dhcp")) }
func TestLiveDHCPOracle(t *testing.T)    { liveParcelOracle(t, parcelFixtures(t, "classic-dhcp")) }
