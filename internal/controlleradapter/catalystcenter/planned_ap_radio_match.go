// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"slices"
)

// Every desired radio must match a different observed radio. There are at most
// four radios, so bounded backtracking avoids order-dependent greedy matches
// when one selector omits fields that another selector specifies. This proves
// selected values; it does not choose a radio to mutate or fabricate an ID.
func plannedAPRadiosMatch(desired []any, observed []plannedAPRadio) bool {
	if len(desired) == 0 || len(desired) > 4 || len(desired) > len(observed) || len(observed) > 4 {
		return false
	}
	candidates := make([][]int, len(desired))
	for i, value := range desired {
		want, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for j, radio := range observed {
			raw, err := json.Marshal(radio)
			var got map[string]any
			if err != nil || json.Unmarshal(raw, &got) != nil {
				return false
			}
			matches := true
			for field, value := range want {
				if field == "bands" {
					bands, ok := value.([]float64)
					if !ok || len(bands) != 1 || !slices.Contains(radio.Bands, bands[0]) {
						matches = false
						break
					}
				} else if field == "antenna" {
					antenna, ok := value.(map[string]any)
					actual, valid := got["antenna"].(map[string]any)
					if !ok || !valid {
						matches = false
						break
					}
					for k, v := range antenna {
						if !reflect.DeepEqual(actual[k], v) {
							matches = false
							break
						}
					}
				} else if !reflect.DeepEqual(got[field], value) {
					matches = false
					break
				}
			}
			if matches {
				candidates[i] = append(candidates[i], j)
			}
		}
	}
	var match func(int, uint8) bool
	match = func(index int, used uint8) bool {
		if index == len(candidates) {
			return true
		}
		for _, candidate := range candidates[index] {
			bit := uint8(1) << candidate
			if used&bit == 0 && match(index+1, used|bit) {
				return true
			}
		}
		return false
	}
	return match(0, 0)
}
