// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func sampleSites() []Site {
	return []Site{
		{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"},
		{ID: "area", Name: "Lab", NameHierarchy: "Global/Lab", ParentID: "root", Type: "area"},
		{ID: "building", Name: "Building", NameHierarchy: "Global/Lab/Building", ParentID: "area", Type: "building"},
	}
}
func TestSitesObservationRequiresCompleteHierarchy(t *testing.T) {
	for _, mode := range []string{"valid", "partial", "missing-root", "missing-parent", "duplicate", "wrong-parent", "invalid-type", "missing-response"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != sitesPath || r.URL.Query().Get("offset") != "1" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				sites := sampleSites()
				switch mode {
				case "partial":
					w.WriteHeader(206)
				case "missing-root":
					sites = sites[1:]
				case "missing-parent":
					sites = append(sites[:1], sites[2])
				case "duplicate":
					sites = append(sites, sites[1])
				case "wrong-parent":
					sites[2].ParentID = "root"
				case "invalid-type":
					sites[1].Type = "unsupported"
				case "missing-response":
					fmt.Fprint(w, `{}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": sites})
			})
			sites, err := c.ListSites(context.Background())
			if mode == "valid" {
				if err != nil || len(sites) != 3 {
					t.Fatalf("%v %d", err, len(sites))
				}
			} else if err == nil || sites != nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}
func TestSitesPlanPreservesUnmanagedAndNormalizesNumbers(t *testing.T) {
	raw := []byte(`catalyst_center:
  sites:
    areas:
      - {name: New, parent_name: Global}
    buildings:
      - {name: Building, parent_name: Global/Lab, latitude: 1.234567, address: Different}
`)
	desired, err := parseSites(raw)
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleSites()
	lat := 1.23457
	addr := "Original"
	actual[2].Latitude = &lat
	actual[2].Address = &addr
	drift, err := planSites(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 2 || drift[0].Path != "/address" || drift[1].Change != configapi.NetworkControllerDriftChangeAdd {
		t.Fatalf("unexpected drift: %+v", drift)
	}
	rawReport, _ := json.Marshal(drift)
	if strings.Contains(string(rawReport), "Different") || strings.Contains(string(rawReport), "Original") {
		t.Fatal("values disclosed")
	}
	actual[2].Latitude = nil
	if _, err = planSites(desired, actual); err == nil {
		t.Fatal("missing managed field treated as empty")
	}
}
func TestSitesRejectUnsupportedOrUnresolvedIntent(t *testing.T) {
	for _, body := range []string{
		`areas: [{name: Lab}]`,
		`areas: [{name: ../Bad, parent_name: Global}]`,
		`areas: [{name: Lab, parent_name: Global}, {name: Lab, parent_name: Global}]`,
		`areas: [{name: Lab, parent_name: Global, unknown: secret}]`,
		`buildings: [{name: B, parent_name: Global, latitude: 95}]`,
		`floors: [{name: F, parent_name: Global/B, image_path: secret}]`,
		`floors: [{name: F, parent_name: Global/B, height: 3}]`,
		`unknown: []`,
		`areas: [{name: B, name: C, parent_name: Global}]`,
	} {
		if _, err := parseSites([]byte("catalyst_center:\n  sites:\n    " + body)); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}
func TestSitesPlanRejectsParentAndTypeConflicts(t *testing.T) {
	for _, body := range []string{
		`floors: [{name: F, parent_name: Global/Lab, width: 2, length: 2, height: 2, rf_model: Free Space, units_of_measure: meters}]`,
		`areas: [{name: Child, parent_name: Global/Missing}]`,
		`areas: [{name: Building, parent_name: Global/Lab}]`,
	} {
		desired, err := parseSites([]byte("catalyst_center:\n  sites:\n    " + body))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = planSites(desired, sampleSites()); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestSitesFloorUnitsAndMissingObservations(t *testing.T) {
	desired, err := parseSites([]byte(`catalyst_center:
  sites:
    floors:
      - name: Floor
        parent_name: Global/Lab/Building
        width: 3.048
        length: 6.096
        height: 3.048
        units_of_measure: meters
        rf_model: Free Space
        floor_number: 0
`))
	if err != nil {
		t.Fatal(err)
	}
	units, rf := "feet", "Free Space"
	width, length, height, floor := 10.0, 20.0, 10.0, 0.0
	actual := append(sampleSites(), Site{ID: "floor", Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor", Width: &width, Length: &length, Height: &height, FloorNumber: &floor, UnitsOfMeasure: &units, RFModel: &rf})
	drift, err := planSites(desired, actual)
	if err != nil || len(drift) != 0 {
		t.Fatalf("physical dimensions differ after unit conversion: %v %+v", err, drift)
	}
	actual[3].Width = nil
	if _, err = planSites(desired, actual); err == nil {
		t.Fatal("missing floor width accepted")
	}
}
