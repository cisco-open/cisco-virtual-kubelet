// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func rfTestModel(rows ...map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": map[string]any{"rf_profiles": rows}}})
	return raw
}

func rfTestRow() map[string]any {
	return map[string]any{"name": "CVK_RF", "enable_radio_type_a": true, "enable_radio_type_b": false, "enable_radio_type_c": false}
}

func TestWirelessRFModelProjection(t *testing.T) {
	row := rfTestRow()
	row["radio_type_c_properties"] = map[string]any{
		"min_dbs_width": 20, "max_dbs_width": 320,
		"fra_properties_c":       map[string]any{"client_reset_count": 2},
		"multi_bssid_properties": map[string]any{"dot11be_parameters": map[string]any{"ofdma_multi_ru": false}},
	}
	profiles, err := parseWirelessRFProfiles(rfTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].fields["defaultRfProfile"] != false {
		t.Fatal(profiles)
	}
	for path, want := range map[string]any{
		"enableRadioType6GHz":                                                         false,
		"radioType6GHzProperties.minDbsWidth":                                         int64(20),
		"radioType6GHzProperties.maxDbsWidth":                                         int64(320),
		"radioType6GHzProperties.fraPropertiesC.clientResetCount":                     int64(2),
		"radioType6GHzProperties.multiBssidProperties.dot11beParameters.ofdmaMultiRu": false,
	} {
		got, ok := getWirelessAPField(profiles[0].fields, path)
		if !ok || got != want {
			t.Fatalf("%s: got %v want %v", path, got, want)
		}
	}
	if _, exists := profiles[0].fields["radioTypeAProperties"]; exists {
		t.Fatal("invented optional settings")
	}
}

func TestWirelessRFRejectInvalidModels(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"missing required band": func(r map[string]any) { delete(r, "enable_radio_type_c") },
		"string boolean":        func(r map[string]any) { r["enable_radio_type_a"] = "true" },
		"unknown root":          func(r map[string]any) { r["typo"] = true },
		"unknown nested":        func(r map[string]any) { r["radio_type_a_properties"] = map[string]any{"typo": 1} },
		"literal dotted key":    func(r map[string]any) { r["radio_type_a_properties.min_power_level"] = 1 },
		"empty nested":          func(r map[string]any) { r["radio_type_a_properties"] = map[string]any{} },
		"null":                  func(r map[string]any) { r["default_rf_profile"] = nil },
		"fractional":            func(r map[string]any) { r["radio_type_a_properties"] = map[string]any{"min_power_level": 1.5} },
		"inverted power": func(r map[string]any) {
			r["radio_type_a_properties"] = map[string]any{"min_power_level": 20, "max_power_level": 10}
		},
		"bad width":    func(r map[string]any) { r["radio_type_c_properties"] = map[string]any{"min_dbs_width": 30} },
		"out of range": func(r map[string]any) { r["radio_type_a_properties"] = map[string]any{"power_threshold_v1": -90} },
	} {
		t.Run(name, func(t *testing.T) {
			row := rfTestRow()
			change(row)
			if _, err := parseWirelessRFProfiles(rfTestModel(row)); err == nil {
				t.Fatal("accepted invalid model")
			}
		})
	}
	one, two := rfTestRow(), rfTestRow()
	two["name"] = "CVK_RF2"
	one["default_rf_profile"] = true
	two["default_rf_profile"] = true
	if _, err := parseWirelessRFProfiles(rfTestModel(one, two)); err == nil {
		t.Fatal("accepted two defaults")
	}
	if _, err := parseWirelessRFProfiles(rfTestModel(one, one)); err == nil {
		t.Fatal("accepted duplicate name")
	}
}

func TestWirelessRFAllPinnedFields(t *testing.T) {
	if len(wirelessRFFields) != 88 {
		t.Fatal("pinned RF field coverage changed", len(wirelessRFFields))
	}
	row := rfTestRow()
	for path, field := range wirelessRFFields {
		if path == "name" {
			continue
		}
		var value any
		switch field.kind {
		case "bool":
			value = false
		case "int":
			value = field.min
		case "string":
			value = "6"
			if field.enums != "" {
				value = strings.Split(field.enums, "|")[0]
			}
		}
		setWirelessAPField(row, path, value)
	}
	if _, err := parseWirelessRFProfiles(rfTestModel(row)); err != nil {
		t.Fatal(err)
	}
}

func TestWirelessRFSetNormalization(t *testing.T) {
	row := rfTestRow()
	row["radio_type_a_properties"] = map[string]any{"radio_channels": "40,36,44", "data_rates": "24,6.0,12,5.5", "mandatory_data_rates": "12,6"}
	desired, err := parseWirelessRFProfiles(rfTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"radioChannels": "36,40,44", "dataRates": "5.5,6,12,24", "mandatoryDataRates": "6,12"} {
		got, _ := getWirelessAPField(desired[0].fields, "radioTypeAProperties."+path)
		if got != want {
			t.Fatalf("%s: %v", path, got)
		}
	}
	for _, value := range []string{"6,6.0", "NaN", "+6", "1e2", "6,", "0", "-1"} {
		if _, err := normalizeWirelessRFList("radioTypeAProperties.dataRates", value); err == nil {
			t.Fatal("invalid list accepted", value)
		}
	}
	if _, err := normalizeWirelessRFList("radioTypeAProperties.radioChannels", "5.5"); err == nil {
		t.Fatal("fractional channel accepted")
	}
	row["radio_type_a_properties"] = map[string]any{"data_rates": "6,12", "mandatory_data_rates": "24"}
	if _, err := parseWirelessRFProfiles(rfTestModel(row)); err == nil {
		t.Fatal("mandatory rate outside supported rates accepted")
	}
}
