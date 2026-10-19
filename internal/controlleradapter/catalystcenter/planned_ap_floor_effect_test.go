// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func TestPlannedAPFloorEffect(t *testing.T) {
	for _, mode := range []string{"unchanged", "width", "length", "both", "shrink", "height", "units-equivalent", "fractional", "origin", "target-ID", "parent-ID", "path", "old-width", "new-width", "new-units", "missing-position", "outside-before", "height-below-AP", "raw-coordinate", "raw-ID", "unknown-fields", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			_, before, _ := plannedAPKeyedFixture(t)
			before.floor = plannedAPTestFloor(before.floorID, before.want.floor)
			// Preserve exact spelling on untouched modeled axes as well as opaque
			// fields. The typed values deliberately remain numerically identical.
			for axis, value := range map[string]*float64{"x": before.actual.Position.X, "y": before.actual.Position.Y, "z": before.actual.Position.Z} {
				before.native["position"].(map[string]any)[axis] = json.Number(strconv.FormatFloat(*value, 'f', 4, 64))
			}
			after := before.floor
			num := func(n float64) *float64 { return &n }
			factorX, factorY := 1.0, 1.0
			switch mode {
			case "width":
				after.Width, factorX = num(200), 2
			case "length":
				after.Length, factorY = num(200), 2
			case "both":
				after.Width, after.Length, factorX, factorY = num(200), num(300), 2, 3
			case "shrink":
				after.Width, after.Length, factorX, factorY = num(50), num(25), 0.5, 0.25
			case "height":
				after.Height = num(200)
			case "units-equivalent":
				u := "meters"
				after.UnitsOfMeasure = &u
				after.Width, after.Length, after.Height = num(30.48), num(30.48), num(30.48)
			case "fractional":
				after.Width, factorX = num(140), 1.4
			case "origin":
				before.actual.Position.X, before.actual.Position.Y = num(0), num(0)
				before.native["position"].(map[string]any)["x"] = json.Number("0")
				before.native["position"].(map[string]any)["y"] = json.Number("0")
				after.Width, after.Length = num(200), num(300)
			case "target-ID":
				after.ID = plannedAPIdentityTestUUID
			case "parent-ID":
				after.ParentID = "other"
			case "path":
				after.NameHierarchy += "Other"
			case "old-width":
				before.floor.Width = nil
			case "new-width":
				after.Width = num(0)
			case "new-units":
				u := "unknown"
				after.UnitsOfMeasure = &u
			case "missing-position":
				before.actual.Position = nil
			case "outside-before":
				before.actual.Position.X = num(101)
			case "height-below-AP":
				after.Height = num(0.1)
			case "raw-coordinate":
				before.native["position"].(map[string]any)["x"] = json.Number("7")
			case "raw-ID":
				before.native["id"] = "33333333-3333-4333-8333-333333333333"
			case "overflow":
				after.Width = num(math.Inf(1))
			}
			original, _ := json.Marshal(before.native)
			originalTyped, _ := json.Marshal(before.actual)
			projected, err := plannedAPFloorEffect(before, after)
			valid := mode == "unchanged" || mode == "width" || mode == "length" || mode == "both" || mode == "shrink" || mode == "height" || mode == "units-equivalent" || mode == "fractional" || mode == "origin" || mode == "unknown-fields"
			if (err == nil) != valid {
				t.Fatal("invalid geometry, scope or starting native state accepted", err)
			}
			fresh, _ := json.Marshal(before.native)
			if !bytes.Equal(original, fresh) {
				t.Fatal("calculation mutated the private starting snapshot")
			}
			freshTyped, _ := json.Marshal(before.actual)
			if !bytes.Equal(originalTyped, freshTyped) {
				t.Fatal("calculation mutated the typed starting observation")
			}
			if !valid {
				return
			}
			expectedX, expectedY := *before.actual.Position.X*factorX, *before.actual.Position.Y*factorY
			// These selected fixture values have exact expected native decimals;
			// do not derive expectations with the production conversion helper.
			if mode == "fractional" {
				expectedX = 6.2999997
			}
			if *projected.actual.Position.X != expectedX || *projected.actual.Position.Y != expectedY || *projected.actual.Position.Z != *before.actual.Position.Z {
				t.Fatal("coordinate projection changed an unselected axis or used the wrong physical scale")
			}
			for axis, unchanged := range map[string]bool{"x": factorX == 1, "y": factorY == 1, "z": true} {
				if unchanged {
					oldRaw, _ := json.Marshal(before.native["position"].(map[string]any)[axis])
					newRaw, _ := json.Marshal(projected.native["position"].(map[string]any)[axis])
					if !bytes.Equal(oldRaw, newRaw) {
						t.Fatal("untouched coordinate lost its exact native representation", axis)
					}
				}
			}
			projected.native["position"].(map[string]any)["x"] = before.native["position"].(map[string]any)["x"]
			projected.native["position"].(map[string]any)["y"] = before.native["position"].(map[string]any)["y"]
			preserved, _ := json.Marshal(projected.native)
			if !bytes.Equal(original, preserved) {
				t.Fatal("projection lost opaque native values, exact numbers, identities or radio state")
			}
			*projected.actual.Position.X = 99
			*projected.actual.Radios[0].Channel = 44
			projected.native["opaque"].(map[string]any)["nullable"] = "changed"
			fresh, _ = json.Marshal(before.native)
			freshTyped, _ = json.Marshal(before.actual)
			if !bytes.Equal(original, fresh) || !bytes.Equal(originalTyped, freshTyped) {
				t.Fatal("projected state aliases the original native or typed observation")
			}
		})
	}
}

func TestPlannedAPFloorIntermediatePrecision(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		value, before, after, want float64
	}{
		{"first-native-resize", 5.5, 41.51575, 45.93176, 6.0850325},
		{"second-native-resize", 5.5, 45.93176, 52.49344, 6.2857146},
		{"fractional-scale", 4.5, 100, 140, 6.2999997},
		{"exact-scale", 4.5, 100, 200, 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := plannedAPFloorScaledCoordinate(tc.value, tc.before, tc.after)
			if err != nil || got != tc.want {
				t.Fatal("native intermediate precision mismatch", err)
			}
		})
	}
}

func TestPlannedAPFloorCoordinatePrecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want float64
		invalid  bool
	}{
		{"native-width-effect", 6.085032307016012, 6.0850325, false},
		{"exact-integer", 11, 11, false},
		{"exact-half", 5.5, 5.5, false},
		{"decimal", 6.3, 6.3, false},
		{"origin", 0, 0, false},
		{"negative", -1, 0, true},
		{"not-a-number", math.NaN(), 0, true},
		{"infinite", math.Inf(1), 0, true},
		{"binary32-overflow", math.MaxFloat64, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := plannedAPFloorCoordinate(tc.in)
			if (err != nil) != tc.invalid || !tc.invalid && got != tc.want {
				t.Fatal("native coordinate conversion mismatch", err)
			}
		})
	}
}
