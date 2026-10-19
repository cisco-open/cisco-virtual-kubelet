// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSiteExecutionPrecisionConvergesWithoutLosingIntent(t *testing.T) {
	model := strings.Replace(executionSiteModel, "latitude: 52.5, longitude: 13.4", "latitude: -1.234565, longitude: 13.1234567", 1)
	model = strings.Replace(model, "width: 10, length: 15, height: 3", "width: 10.123456, length: 15.123456, height: 3.123456", 1)
	desired, err := parseSites([]byte(model))
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleSites()[:1]
	for i := 0; i < 3; i++ {
		m, err := nextSiteMutation(desired, actual)
		if err != nil || m == nil {
			t.Fatalf("step %d: mutation=%v error=%v", i, m, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(m.body, &payload); err != nil {
			t.Fatal(err)
		}
		if m.desired.kind == "building" {
			if payload["latitude"] != -1.23456 || payload["longitude"] != 13.12346 {
				t.Fatalf("coordinate normalization: %s", m.body)
			}
			if m.desired.fields["latitude"] != -1.234565 {
				t.Fatal("normalization changed source intent")
			}
		}
		if m.desired.kind == "floor" && (payload["width"] != 10.123 || payload["length"] != 15.123 || payload["height"] != 3.123) {
			t.Fatalf("geometry normalization: %s", m.body)
		}
		var site Site
		if err := json.Unmarshal(m.body, &site); err != nil {
			t.Fatal(err)
		}
		site.ID, site.NameHierarchy, site.Type = fmt.Sprintf("precision-%d", i), m.desired.path, m.desired.kind
		actual = append(actual, site)
	}
	if next, err := nextSiteMutation(desired, actual); err != nil || next != nil {
		t.Fatalf("normalized readback did not converge: %v %v", next, err)
	}
}

func TestSiteExecutionRejectsGeometryRoundedToZeroBeforeParentCreation(t *testing.T) {
	for _, key := range []string{"width", "length", "height"} {
		t.Run(key, func(t *testing.T) {
			desired, err := parseSites([]byte(executionSiteModel))
			if err != nil {
				t.Fatal(err)
			}
			for i := range desired {
				if desired[i].kind == "floor" {
					desired[i].fields[key] = 0.0004
				}
			}
			if next, err := nextSiteMutation(desired, sampleSites()[:1]); err == nil || next != nil {
				t.Fatal("parent creation accepted invalid normalized descendant geometry")
			}
		})
	}
}
