// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func plannedAPTestFloor(id, path string) Site {
	w, l, h := 100.0, 100.0, 100.0
	u := "feet"
	return Site{ID: id, Name: lastSiteName(path), NameHierarchy: path, ParentID: "building", Type: "floor", Width: &w, Length: &l, Height: &h, UnitsOfMeasure: &u}
}

func TestPlannedAPPositionBounds(t *testing.T) {
	for _, units := range []string{"feet", "meters"} {
		for _, axis := range []string{"x", "y", "z"} {
			for _, mode := range []string{"zero", "boundary", "negative", "above", "nan", "missing"} {
				t.Run(units+"/"+axis+"/"+mode, func(t *testing.T) {
					g, err := plannedAPGeometryFromValues(10, 10, 10, units)
					if err != nil {
						t.Fatal(err)
					}
					position := map[string]any{"x": 1.0, "y": 1.0, "z": 1.0}
					switch mode {
					case "zero":
						position[axis] = 0.0
					case "boundary":
						position[axis] = g.width
					case "negative":
						position[axis] = -0.001
					case "above":
						position[axis] = g.width + 0.001
					case "nan":
						position[axis] = math.NaN()
					case "missing":
						delete(position, axis)
					}
					err = validatePlannedAPPosition(map[string]any{"position": position}, g)
					valid := mode == "zero" || mode == "boundary" || mode == "missing" && axis == "z"
					if (err == nil) != valid {
						t.Fatal("unexpected extent result", err)
					}
				})
			}
		}
	}
}

func TestPlannedAPGeometryUsesSubmittedPrecision(t *testing.T) {
	d := desiredSite{kind: "floor", fields: map[string]any{"width": 1.0004, "length": 2.0, "height": 3.0, "units_of_measure": "meters"}}
	g, err := plannedAPDesiredGeometry(d)
	if err != nil || math.Abs(g.width-1/0.3048) > 1e-12 {
		t.Fatal("geometry did not use the submitted rounded dimension", err)
	}
	if err := validatePlannedAPPosition(map[string]any{"position": map[string]any{"x": 1.0002 / 0.3048, "y": 1.0}}, g); err == nil {
		t.Fatal("unsubmitted fractional extent admitted a placement")
	}
	d.fields["width"] = 0.0004
	if _, err := plannedAPDesiredGeometry(d); err == nil {
		t.Fatal("zero-rounded extent accepted")
	}
	floor := plannedAPTestFloor("floor", "Global/Lab/Building/Floor")
	floor.Width = nil
	if _, err := plannedAPObservedGeometry(floor); err == nil {
		t.Fatal("missing native extent accepted")
	}
}

func TestPlannedAPInvalidPositionPrecedesGeometry(t *testing.T) {
	for _, axis := range []string{"x", "y", "z"} {
		t.Run(axis, func(t *testing.T) {
			sites, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			desired[0].payload["position"].(map[string]any)[axis] = 1000.0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, plannedAPCatalogFixture)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				default:
					t.Error("out-of-bounds position reached an AP read or geometry write")
					w.WriteHeader(400)
				}
			})
			e := &sitePlannedAPExecutor{sites: &siteConfigurationExecutor{client: c, desired: sites}, placements: &plannedAPExecutor{client: c, desired: desired}}
			if write, err := e.next(context.Background()); err == nil || write != nil {
				t.Fatal("out-of-bounds model escaped preflight")
			}
		})
	}
}

func TestPlannedAPFutureGeometryAllowsExpansion(t *testing.T) {
	sites, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
	if err != nil {
		t.Fatal(err)
	}
	floor := plannedAPTestFloor("floor", desired[0].floor)
	one := 1.0
	rf := "Free Space"
	floor.Width, floor.FloorNumber, floor.RFModel = &one, &one, &rf
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case plannedAPCatalogPath:
			fmt.Fprint(w, plannedAPCatalogFixture)
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), floor)})
		case plannedAPPositionsPath("floor"):
			fmt.Fprint(w, `{"response":[]}`)
		default:
			t.Error("preflight mutated or used an unknown endpoint")
			w.WriteHeader(400)
		}
	})
	e := &sitePlannedAPExecutor{sites: &siteConfigurationExecutor{client: c, desired: sites}, placements: &plannedAPExecutor{client: c, desired: desired}}
	write, err := e.next(context.Background())
	if err != nil || write == nil || write.resource != desired[0].floor || write.action != "Update" {
		t.Fatal("valid future extent blocked or placement returned before expansion", err)
	}
}

func TestPlannedAPSubmissionRechecksAllNativeExtents(t *testing.T) {
	for _, mode := range []string{"valid", "selected-shrink", "later-shrink", "replaced-floor", "missing-extent", "frozen-position", "missing-receipt"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			_, later, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			later[0].floor = "Global/Lab/Building/Later"
			desired = append(desired, later[0])
			floors := []Site{plannedAPTestFloor("floor", desired[0].floor), plannedAPTestFloor("later", desired[1].floor)}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, plannedAPCatalogFixture)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), floors...)})
				case plannedAPPositionsPath("floor"), plannedAPPositionsPath("later"):
					fmt.Fprint(w, `{"response":[]}`)
				case plannedAPPositionsPath("floor") + "/bulk":
					writes++
					if mode == "missing-receipt" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
				default:
					t.Error("unexpected API")
					w.WriteHeader(400)
				}
			})
			write, err := (&plannedAPExecutor{client: c, desired: desired}).next(context.Background())
			if err != nil || write == nil {
				t.Fatal("planning", err)
			}
			one := 1.0
			switch mode {
			case "selected-shrink":
				floors[0].Width = &one
			case "later-shrink":
				floors[1].Length = &one
			case "replaced-floor":
				floors[0].ID = "replacement"
			case "missing-extent":
				floors[0].Height = nil
			case "frozen-position":
				desired[0].payload["position"].(map[string]any)["x"] = 0.0
				floors[0].Width = &one
			}
			_, err = write.submit(context.Background())
			if (err == nil) != (mode == "valid") {
				t.Fatal("submission", err)
			}
			expected := 0
			if mode == "valid" || mode == "missing-receipt" {
				expected = 1
			}
			if writes != expected {
				t.Fatal("stale write reached API")
			}
			if _, err := write.submit(context.Background()); err == nil || writes != expected {
				t.Fatal("consumed plan replayed")
			}
		})
	}
}

func TestPlannedAPVerificationRequiresCurrentExtents(t *testing.T) {
	for _, mode := range []string{"valid", "shrunk-floor", "retained-z-outside"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			var actual plannedAPObservation
			if err := json.Unmarshal([]byte(plannedAPObservedFixture), &actual); err != nil {
				t.Fatal(err)
			}
			floor := plannedAPTestFloor("floor", desired[0].floor)
			if mode == "shrunk-floor" {
				one := 1.0
				floor.Width = &one
			}
			if mode == "retained-z-outside" {
				delete(desired[0].payload["position"].(map[string]any), "z")
				z := 1000.0
				actual.Position.Z = &z
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, plannedAPCatalogFixture)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), floor)})
				case plannedAPPositionsPath("floor"):
					json.NewEncoder(w).Encode(map[string]any{"response": []plannedAPObservation{actual}})
				default:
					t.Error("verification mutated or queried unknown API")
					w.WriteHeader(400)
				}
			})
			e := &plannedAPExecutor{client: c, desired: desired}
			ok, err := e.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: plannedAPResource(desired[0]), TargetID: "ap-id", ParentID: "floor"})
			if mode == "valid" {
				if err != nil || !ok {
					t.Fatal("valid native point did not verify", err)
				}
			} else if err == nil || ok {
				t.Fatal("matching selected values certified an out-of-bounds complete position")
			}
		})
	}
}
