// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
)

// Predict the complete AP state for a dimension edit, preserving every field
// outside the native horizontal coordinate effect. This is an expected-state
// calculation, not permission to accept an observed post-task baseline. A
// journaled handoff must bind its result before dispatch and compare full native
// readback after the original floor task; that publication protocol is separate.
func plannedAPFloorEffect(before plannedAPState, after Site) (plannedAPState, error) {
	if before.floor.ID != before.floorID || after.ID != before.floorID || before.floor.NameHierarchy != after.NameHierarchy || before.floor.ParentID != after.ParentID || !canonicalTemplateUUID(before.floorID) {
		return plannedAPState{}, errConfigurationOwned
	}
	oldGeometry, err := plannedAPObservedGeometry(before.floor)
	if err != nil {
		return plannedAPState{}, err
	}
	newGeometry, err := plannedAPObservedGeometry(after)
	if err != nil {
		return plannedAPState{}, err
	}
	if before.actual.Position == nil || before.actual.Position.X == nil || before.actual.Position.Y == nil || before.actual.Position.Z == nil {
		return plannedAPState{}, errInvalidResponse
	}
	// Check the entire current position as well as the predicted result. A
	// dimensional edit must not turn invalid or ambiguous input into authority.
	if err := validatePlannedAPPosition(map[string]any{"position": map[string]any{"x": *before.actual.Position.X, "y": *before.actual.Position.Y, "z": *before.actual.Position.Z}}, oldGeometry); err != nil {
		return plannedAPState{}, err
	}
	x, y := *before.actual.Position.X, *before.actual.Position.Y
	// Avoid changing floating representations on an unchanged physical axis.
	if oldGeometry.width != newGeometry.width {
		x, err = plannedAPFloorScaledCoordinate(x, oldGeometry.width, newGeometry.width)
		if err != nil {
			return plannedAPState{}, err
		}
	}
	if oldGeometry.length != newGeometry.length {
		y, err = plannedAPFloorScaledCoordinate(y, oldGeometry.length, newGeometry.length)
		if err != nil {
			return plannedAPState{}, err
		}
	}
	if math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
		return plannedAPState{}, errInvalidResponse
	}
	// Clone through the exact-number decoder. Raw values, nested unknown fields
	// and radio ordering must survive without template-specific normalization.
	raw, err := json.Marshal(before.native)
	if err != nil {
		return plannedAPState{}, errInvalidResponse
	}
	value, err := decodeExactNativeJSON(raw)
	if err != nil {
		return plannedAPState{}, err
	}
	native, ok := value.(map[string]any)
	if !ok || native == nil {
		return plannedAPState{}, errInvalidResponse
	}
	position, ok := native["position"].(map[string]any)
	if !ok {
		return plannedAPState{}, errInvalidResponse
	}
	// Do not allow typed/raw divergence to hide a different native identity or
	// starting coordinate. Both projections must originate in the same response.
	var observed plannedAPObservation
	if json.Unmarshal(raw, &observed) != nil || validatePlannedAPObservation(&observed) != nil || !reflect.DeepEqual(before.actual, observed) {
		return plannedAPState{}, errConfigurationStale
	}
	if oldGeometry.width != newGeometry.width {
		position["x"] = x
	}
	if oldGeometry.length != newGeometry.length {
		position["y"] = y
	}
	projected := plannedAPState{want: before.want, floorID: before.floorID, floor: after, native: native}
	raw, err = json.Marshal(native)
	if err != nil || json.Unmarshal(raw, &projected.actual) != nil || validatePlannedAPObservation(&projected.actual) != nil {
		return plannedAPState{}, errInvalidResponse
	}
	if err := validatePlannedAPPosition(map[string]any{"position": map[string]any{"x": x, "y": y, "z": *projected.actual.Position.Z}}, newGeometry); err != nil {
		return plannedAPState{}, err
	}
	return projected, nil
}

func plannedAPFloorScaledCoordinate(value, before, after float64) (float64, error) {
	if before <= 0 || after <= 0 || float32(before) == 0 || float32(after) == 0 || math.IsInf(float64(float32(before)), 0) || math.IsInf(float64(float32(after)), 0) {
		return 0, errInvalidResponse
	}
	// Explicit conversions force native binary32 rounding at the intermediate
	// division and product, not just when formatting the final coordinate.
	scale := float32(float32(after) / float32(before))
	result := float32(float32(value) * scale)
	return plannedAPFloorCoordinate(float64(result))
}

// The qualified lab returns changed map coordinates using the shortest decimal
// representation of a binary32 value. Reproduce that precise expected value;
// never round unrelated observations or use an epsilon in private comparisons.
// Other controller versions remain unqualified until complete readback agrees.
func plannedAPFloorCoordinate(n float64) (float64, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || math.IsInf(float64(float32(n)), 0) {
		return 0, errInvalidResponse
	}
	v, err := strconv.ParseFloat(strconv.FormatFloat(float64(float32(n)), 'g', -1, 32), 64)
	if err != nil {
		return 0, errInvalidResponse
	}
	return v, nil
}
