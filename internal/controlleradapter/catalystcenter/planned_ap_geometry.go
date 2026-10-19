// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "math"

type plannedAPGeometry struct{ width, length, height float64 }

func plannedAPGeometryFromValues(width, length, height float64, units string) (plannedAPGeometry, error) {
	if units != "feet" && units != "meters" {
		return plannedAPGeometry{}, errInvalidResponse
	}
	for _, n := range []float64{width, length, height} {
		if n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return plannedAPGeometry{}, errInvalidResponse
		}
	}
	if units == "meters" {
		width /= 0.3048
		length /= 0.3048
		height /= 0.3048
	}
	for _, n := range []float64{width, length, height} {
		if math.IsInf(n, 0) {
			return plannedAPGeometry{}, errInvalidResponse
		}
	}
	return plannedAPGeometry{width, length, height}, nil
}

func plannedAPObservedGeometry(site Site) (plannedAPGeometry, error) {
	if site.Type != "floor" || site.Width == nil || site.Length == nil || site.Height == nil || site.UnitsOfMeasure == nil {
		return plannedAPGeometry{}, errInvalidResponse
	}
	return plannedAPGeometryFromValues(*site.Width, *site.Length, *site.Height, *site.UnitsOfMeasure)
}

func plannedAPDesiredGeometry(site desiredSite) (plannedAPGeometry, error) {
	w, wok := site.fields["width"].(float64)
	l, lok := site.fields["length"].(float64)
	h, hok := site.fields["height"].(float64)
	u, uok := site.fields["units_of_measure"].(string)
	if site.kind != "floor" || !wok || !lok || !hok || !uok {
		return plannedAPGeometry{}, errPlannedAPModel
	}
	return plannedAPGeometryFromValues(normalizedSiteNumber("width", w), normalizedSiteNumber("length", l), normalizedSiteNumber("height", h), u)
}

// Position values are already feet, independently of the floor's representation.
// Check physical extents without inventing controller-specific minimum mounting
// height or optional Z defaults. Native validation may impose tighter bounds.
func validatePlannedAPPosition(payload map[string]any, geometry plannedAPGeometry) error {
	position, ok := payload["position"].(map[string]any)
	if !ok {
		return errPlannedAPModel
	}
	for axis, maximum := range map[string]float64{"x": geometry.width, "y": geometry.length, "z": geometry.height} {
		value, present := position[axis]
		if axis == "z" && !present {
			continue
		}
		n, err := plannedAPNumber(value, false)
		if !present || err != nil || n < 0 || n > maximum {
			return errPlannedAPModel
		}
	}
	return nil
}

func frozenPlannedAPPosition(floor string, payload map[string]any) plannedAPIntent {
	position := map[string]any{}
	for axis, n := range payload["position"].(map[string]any) {
		position[axis] = n
	}
	return plannedAPIntent{floor: floor, payload: map[string]any{"position": position}}
}
