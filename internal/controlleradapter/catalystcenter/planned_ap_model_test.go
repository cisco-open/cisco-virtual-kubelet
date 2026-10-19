// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

const plannedAPFixture = `{"catalyst_center":{"sites":{"floors":[{"name":"Floor","parent_name":"Global/Lab/Building","floor_number":1,"rf_model":"Free Space","width":10,"length":15,"height":3,"units_of_measure":"meters","planned_access_points":[{"name":"AP1","ap_type":"C9130AXI","position_x":4.5,"position_y":6,"position_z":2,"mac_address":"AA-BB-CC-DD-EE-FF","radios":[{"band":5,"channel":36,"tx_power":12,"antenna_name":"Internal","antenna_azimuth":90,"antenna_elevation":-10}]}]}]}}}`

func TestPlannedAPModel(t *testing.T) {
	raw := []byte(plannedAPFixture)
	before := string(raw)
	sites, aps, err := parseSitesWithPlannedAPs(raw)
	if err != nil || len(sites) != 1 || len(aps) != 1 {
		t.Fatal("model rejected", err)
	}
	if string(raw) != before || sites[0].fields["planned_access_points"] != nil || aps[0].floor != "Global/Lab/Building/Floor" {
		t.Fatal("geometry or source corrupted")
	}
	expected := map[string]any{"name": "AP1", "type": "C9130AXI", "position": map[string]any{"x": 4.5, "y": float64(6), "z": float64(2)}, "macAddress": "aa:bb:cc:dd:ee:ff", "radios": []any{map[string]any{"bands": []float64{5}, "channel": float64(36), "txPower": float64(12), "antenna": map[string]any{"name": "Internal", "azimuth": float64(90), "elevation": float64(-10)}}}}
	if !reflect.DeepEqual(aps[0].payload, expected) {
		t.Fatal("model mapping lost fields")
	}
	if _, err := parseSites(raw); err == nil {
		t.Fatal("ordinary runtime silently ignored planned APs")
	}
	// No radio defaults, position Z, or MAC is invented when omitted upstream.
	payload, err := parsePlannedAPPayload(map[string]any{"name": "AP", "ap_type": "C9130AXI", "position_x": 0, "position_y": 0})
	if err != nil || len(payload) != 3 || len(payload["position"].(map[string]any)) != 2 {
		t.Fatal("optional fields were fabricated", err)
	}
}
func TestPlannedAPModelRejectsInvalidInput(t *testing.T) {
	for _, change := range [][2]string{
		{`"planned_access_points":[`, `"unknown":[`},
		{`"position_x":4.5`, `"position_x":null`},
		{`"position_x":4.5`, `"position_x":"4.5"`},
		{`"position_x":4.5`, `"position_z":4.5`},
		{`"name":"AP1"`, `"name":""`},
		{`"ap_type":"C9130AXI"`, `"ap_type":null`},
		{`"band":5`, `"band":7`},
		{`"band":5`, `"band":null`},
		{`"channel":36`, `"channel":36.5`},
		{`"antenna_azimuth":90`, `"antenna_azimuth":361`},
		{`"antenna_elevation":-10`, `"antenna_elevation":-91`},
		{`"antenna_name":"Internal"`, `"antenna_name":""`},
		{`"mac_address":"AA-BB-CC-DD-EE-FF"`, `"mac_address":"invalid"`},
		{`"parent_name":"Global/Lab/Building"`, `"parent_name":"invalid"`},
		{`"units_of_measure":"meters"`, `"units_of_measure":"unknown"`},
	} {
		if _, _, err := parseSitesWithPlannedAPs([]byte(strings.Replace(plannedAPFixture, change[0], change[1], 1))); err == nil {
			t.Fatalf("invalid field accepted: %s", change[0])
		}
	}
	for _, value := range []any{math.NaN(), math.Inf(1), true, "4", nil} {
		if _, err := plannedAPNumber(value, false); err == nil {
			t.Fatal("invalid number accepted")
		}
	}
}
func TestPlannedAPDuplicateAndOrdering(t *testing.T) {
	var root map[string]any
	if json.Unmarshal([]byte(plannedAPFixture), &root) != nil {
		t.Fatal("bad fixture")
	}
	floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	list := floor["planned_access_points"].([]any)
	floor["planned_access_points"] = append(list, list[0])
	raw, _ := json.Marshal(root)
	if _, _, err := parseSitesWithPlannedAPs(raw); err == nil {
		t.Fatal("duplicate AP accepted")
	}
	second := map[string]any{"name": "AP0", "ap_type": "C9130AXI", "position_x": 1, "position_y": 2}
	floor["planned_access_points"] = append(list, second)
	raw, _ = json.Marshal(root)
	_, aps, err := parseSitesWithPlannedAPs(raw)
	if err != nil || aps[0].payload["name"] != "AP0" {
		t.Fatal("noncanonical AP ordering", err)
	}
	floor["image_path"] = "floor.png"
	raw, _ = json.Marshal(root)
	if _, _, err := parseSitesWithPlannedAPs(raw); err == nil {
		t.Fatal("unimplemented floor image ignored")
	}
}
