// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const siteDefaultsInput = `catalyst_center:
  sites:
    areas: [{name: DefaultsLab, parent_name: Global}]
    buildings: [{name: Building, parent_name: Global/DefaultsLab, country: United States, latitude: 37.33812, longitude: -121.88612}]
    floors: [{name: Floor, parent_name: Global/DefaultsLab/Building, floor_number: 1}]
`

func TestSiteDefaultsExport(t *testing.T) {
	out, meta, err := ExportSiteDefaults([]byte(siteDefaultsInput), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	wants, err := parseSites(out)
	if err != nil || len(wants) != 3 {
		t.Fatal("existing parser rejected resolved sites", err)
	}
	var floor desiredSite
	for _, w := range wants {
		if w.kind == "floor" {
			floor = w
		}
	}
	if floor.fields["width"] != float64(30) || floor.fields["length"] != float64(30) || floor.fields["height"] != float64(3) || floor.fields["units_of_measure"] != "feet" || floor.fields["rf_model"] != "Cubes And Walled Offices" {
		t.Fatal("pinned floor defaults lost")
	}
	if meta.Exporter != SiteDefaultsExporter || !meta.ExecutionSyntaxValidated || meta.LeafSchemaValidated || meta.ConfigurationParity || meta.DefaultedFields != 5 || meta.Resources != 3 || meta.SiteExpressionsSHA256 != nacmodel.SiteExpressionsSHA256 {
		t.Fatal("incorrect provenance")
	}
	again, second, err := ExportSiteDefaults([]byte(siteDefaultsInput), nil, false)
	if err != nil || !bytes.Equal(out, again) || second.OutputSHA256 != meta.OutputSHA256 {
		t.Fatal("nondeterministic export")
	}
	// Runtime parsing remains strict; offline export is an explicit operation.
	if _, err := parseSites([]byte(siteDefaultsInput)); err == nil {
		t.Fatal("runtime silently defaulted unresolved floor")
	}
}
func TestSiteDefaultsNullAndBulkSemantics(t *testing.T) {
	for _, mode := range []string{"numeric-null", "explicit", "bulk-parent", "singular-parent", "rf-null", "units-null", "zero", "string-number"} {
		t.Run(mode, func(t *testing.T) {
			raw := siteDefaultsInput
			bulk := false
			switch mode {
			case "numeric-null":
				raw = strings.Replace(raw, "floor_number: 1", "floor_number: 1, width: null", 1)
			case "explicit":
				raw = strings.Replace(raw, "floor_number: 1", "floor_number: 1, width: 45, units_of_measure: meters", 1)
			case "bulk-parent", "singular-parent":
				raw = strings.Replace(raw, ", parent_name: Global}", "}", 1)
				bulk = mode == "bulk-parent"
			case "rf-null":
				raw = strings.Replace(raw, "floor_number: 1", "floor_number: 1, rf_model: null", 1)
			case "units-null":
				raw = strings.Replace(raw, "floor_number: 1", "floor_number: 1, units_of_measure: null", 1)
			case "zero":
				raw = strings.Replace(raw, "floor_number: 1", "floor_number: 1, width: 0", 1)
			case "string-number":
				raw = strings.Replace(raw, "floor_number: 1", "floor_number: 1, width: '45'", 1)
			}
			out, meta, err := ExportSiteDefaults([]byte(raw), nil, bulk)
			valid := mode == "numeric-null" || mode == "explicit" || mode == "bulk-parent"
			if (err == nil) != valid {
				t.Fatal("wrong expression/null handling", err)
			}
			if !valid {
				if len(out) != 0 {
					t.Fatal("partial output on refusal")
				}
				return
			}
			if meta.UseBulkAPI != bulk {
				t.Fatal("bulk expression provenance lost")
			}
			var d map[string]any
			json.Unmarshal(out, &d)
			sites := d["catalyst_center"].(map[string]any)["sites"].(map[string]any)
			floor := sites["floors"].([]any)[0].(map[string]any)
			expected := float64(30)
			if mode == "explicit" {
				expected = 45
			}
			if floor["width"] != expected {
				t.Fatal("explicit/null dimension precedence lost")
			}
		})
	}
}
func TestSiteDefaultsOverridesAndHiddenInvalidInput(t *testing.T) {
	override := []byte("defaults:\n  catalyst_center:\n    sites:\n      floors:\n        width: 40\n        height: 5\n      buildings:\n        country: Canada\n")
	out, meta, err := ExportSiteDefaults([]byte(siteDefaultsInput), override, false)
	if err != nil || meta.OverridesSHA256 == "" || !bytes.Contains(out, []byte(`"width": 40`)) || !bytes.Contains(out, []byte(`"country": "United States"`)) {
		t.Fatal("override or explicit precedence failed", err)
	}
	for _, bad := range []string{
		"defaults: {catalyst_center: {sites: {floors: {width: -1}}}}",
		"defaults: {catalyst_center: {sites: {buildings: {latitude: 100}}}}",
		"defaults: {catalyst_center: {sites: {areas: {parent_name: Somewhere}}}}",
		"defaults: {catalyst_center: {sites: {floors: {height: null}}}}",
		"defaults: {catalyst_center: {sites: {floors: {unknown: SHOULD_NOT_PRINT}}}}",
		"defaults: {catalyst_center: {sites: {}, network_settings: {}}}",
		"defaults: {catalyst_center: {sites: {floors: {height: 3, height: 4}}}}",
		"defaults: &a {catalyst_center: {sites: {}}}",
		"defaults: {catalyst_center: {sites: {}}}\n---\nextra: 1",
	} {
		if result, _, err := ExportSiteDefaults([]byte(siteDefaultsInput), []byte(bad), false); err == nil || len(result) > 0 {
			t.Fatal("invalid unused or shadowed override accepted")
		} else if strings.Contains(err.Error(), "SHOULD_NOT_PRINT") {
			t.Fatal("input leaked")
		}
	}
	for _, bad := range []string{
		strings.Replace(siteDefaultsInput, "name: DefaultsLab", "name: DefaultsLab, unknown: SHOULD_NOT_PRINT", 1),
		strings.Replace(siteDefaultsInput, "floor_number: 1", "floor_number: 1, image_path: secret.png", 1),
		strings.Replace(siteDefaultsInput, "    areas:", "    unknown:", 1),
		siteDefaultsInput + "---\nextra: 1",
		strings.Repeat("x", (1<<20)+1),
	} {
		if result, _, err := ExportSiteDefaults([]byte(bad), nil, false); err == nil || len(result) > 0 {
			t.Fatal("invalid model accepted")
		}
	}
}

// Resolve each source revision before sending it through the unchanged public
// site lifecycle. This removes only five known test geometry fields so the
// offline default expansion, not a duplicate model builder, supplies them.
func siteDefaultResolvedFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("invalid fixture")
	}
	sites := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)
	if rows, ok := sites["floors"].([]any); ok {
		for _, v := range rows {
			row := v.(map[string]any)
			for _, field := range []string{"width", "length", "height", "rf_model", "units_of_measure"} {
				delete(row, field)
			}
		}
	}
	input, _ := json.Marshal(root)
	out, meta, err := ExportSiteDefaults(input, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Resources > 0 && meta.DefaultedFields != 5 {
		t.Fatal("fixture did not resolve pinned defaults")
	}
	return out
}
func TestSiteDefaultsManifestLifecycle(t *testing.T) {
	for _, cm := range []bool{false, true} {
		t.Run(fmt.Sprintf("cm-%t", cm), func(t *testing.T) {
			exerciseSitePruneManifestKinds(t, "complete", cm, nil, true, func(raw []byte) []byte { return siteDefaultResolvedFixture(t, raw) })
		})
	}
}
