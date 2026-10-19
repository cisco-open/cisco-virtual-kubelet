// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestPlannedAPRadioMatching(t *testing.T) {
	var ap plannedAPObservation
	if json.Unmarshal([]byte(plannedAPObservedFixture), &ap) != nil {
		t.Fatal("fixture")
	}
	first := ap.Radios[0]
	second := first
	second.ID = "second"
	channel := 44
	second.Channel = &channel
	observed := []plannedAPRadio{first, second}
	for _, tc := range []struct {
		name    string
		desired []any
		matches bool
	}{
		{"omitted-band", []any{map[string]any{"channel": float64(36)}}, true},
		{"wildcard-before-specific", []any{map[string]any{}, map[string]any{"channel": float64(36)}}, true},
		{"same-band-different-channel", []any{map[string]any{"bands": []float64{5}, "channel": float64(44)}, map[string]any{"bands": []float64{5}, "channel": float64(36)}}, true},
		{"cannot-reuse-one-radio", []any{map[string]any{"channel": float64(36)}, map[string]any{"channel": float64(36)}}, false},
		{"changed-radio", []any{map[string]any{"txPower": float64(18)}}, false},
		{"unknown-band", []any{map[string]any{"bands": []float64{6}}}, false},
		{"empty", []any{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if plannedAPRadiosMatch(tc.desired, observed) != tc.matches {
				t.Fatal("incorrect matching")
			}
			if plannedAPRadiosMatch(tc.desired, []plannedAPRadio{second, first}) != tc.matches {
				t.Fatal("response order changed outcome")
			}
		})
	}
	radio, err := parsePlannedAPRadio(map[string]any{"channel": 36})
	if err != nil || radio["bands"] != nil {
		t.Fatal("missing band fabricated or rejected", err)
	}
}
