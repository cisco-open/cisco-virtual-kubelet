// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFloorImageModelSnapshot(t *testing.T) {
	raw := []byte(strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"maps/floor.pdf"`, 1))
	original := bytes.Clone(raw)
	assets := map[string][]byte{"maps/floor.pdf": []byte("%PDF-1.7\nfirst")}
	sites, aps, images, err := parseSitesWithFloorImages(raw, assets)
	if err != nil || len(images) != 1 {
		t.Fatal("image model rejected", err)
	}
	plainSites, plainAPs, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
	if err != nil || !reflect.DeepEqual(sites, plainSites) || !reflect.DeepEqual(aps, plainAPs) || !bytes.Equal(raw, original) {
		t.Fatal("geometry, placements, or source changed")
	}
	image := images[0]
	if image.floor != "Global/Lab/Building/Floor" || image.filename != "maps/floor.pdf" || len(image.digest) != 64 {
		t.Fatal("wrong image identity")
	}
	assets["maps/floor.pdf"][len(assets["maps/floor.pdf"])-1] = 'X'
	if string(image.content) != "%PDF-1.7\nfirst" {
		t.Fatal("payload aliases source")
	}
	_, _, changed, err := parseSitesWithFloorImages(raw, assets)
	if err != nil || changed[0].digest == image.digest || changed[0].resource() != image.resource() {
		t.Fatal("content change lost stable floor identity", err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(format, image); got != "<floor image snapshot>" {
			t.Fatal("snapshot format leaks data")
		}
	}
	if _, _, err := parseSitesWithPlannedAPs(raw); err == nil {
		t.Fatal("ordinary runtime silently ignored image")
	}
}

func TestFloorImageModelRejectsInvalidIntent(t *testing.T) {
	for _, value := range []string{`""`, `false`, `1`, `[]`, `{}`, `"missing.pdf"`, `"https://example.invalid/floor.pdf"`} {
		raw := []byte(strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":`+value, 1))
		sites, aps, images, err := parseSitesWithFloorImages(raw, nil)
		if err == nil || sites != nil || aps != nil || images != nil {
			t.Fatalf("invalid image accepted: %s", value)
		}
	}
	for _, fragment := range []string{`"image_path":null,`, ``} {
		raw := []byte(strings.Replace(plannedAPFixture, `"floor_number":1`, fragment+`"floor_number":1`, 1))
		_, _, images, err := parseSitesWithFloorImages(raw, nil)
		if err != nil || len(images) != 0 {
			t.Fatal("absent image rejected", err)
		}
	}
	raw := []byte(strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"floor.pdf"`, 1))
	if _, _, _, err := parseSitesWithFloorImages(raw, map[string][]byte{"floor.pdf": []byte("invalid")}); err == nil {
		t.Fatal("invalid bytes accepted")
	}
	for _, change := range [][2]string{{`"position_x":4.5`, `"position_x":null`}, {`"units_of_measure":"meters"`, `"units_of_measure":"unknown"`}} {
		if _, _, _, err := parseSitesWithFloorImages([]byte(strings.Replace(string(raw), change[0], change[1], 1)), map[string][]byte{"floor.pdf": []byte("%PDF-1.7")}); err == nil {
			t.Fatal("image extraction bypassed underlying validation")
		}
	}
}
