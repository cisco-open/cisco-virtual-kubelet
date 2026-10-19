// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestPlannedAPUpdatePreservesUnselected(t *testing.T) {
	_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
	if err != nil {
		t.Fatal(err)
	}
	var got plannedAPObservation
	json.Unmarshal([]byte(plannedAPObservedFixture), &got)
	want := desired[0]
	delete(want.payload, "macAddress")
	delete(want.payload["position"].(map[string]any), "z")
	want.payload["position"].(map[string]any)["x"] = float64(9)
	want.payload["radios"] = []any{map[string]any{"bands": []float64{5}, "txPower": float64(12)}}
	body, changed, err := plannedAPUpdatePayload(want, got)
	if err != nil || !changed {
		t.Fatal("no update", err)
	}
	radio := body["radios"].([]any)[0].(map[string]any)
	if radio["id"] != "radio-id" || radio["channel"] != float64(36) || radio["txPower"] != float64(12) || radio["bands"] != nil || body["position"].(map[string]any)["z"] != float64(2) || body["macAddress"] != "aa:bb:cc:dd:ee:ff" {
		t.Fatal("unselected fields or identity lost")
	}
	if *got.Radios[0].TxPower != 12 || *got.Position.X != 4.5 {
		t.Fatal("readback mutated")
	}
	duplicate := got.Radios[0]
	duplicate.ID = "second-radio"
	got.Radios = append(got.Radios, duplicate)
	if _, _, err := plannedAPUpdatePayload(want, got); err != nil {
		t.Fatal("equivalent radio attributes should verify without selecting a mutation target", err)
	}
}

func TestPlannedAPExecutorLifecycle(t *testing.T) {
	for _, mode := range []string{"create", "update", "incomplete-create", "bad-task", "radio-change", "optional-create"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			actual := []plannedAPObservation{}
			if mode == "update" || mode == "radio-change" {
				var ap plannedAPObservation
				json.Unmarshal([]byte(plannedAPObservedFixture), &ap)
				n := float64(1)
				ap.Position.X = &n
				actual = append(actual, ap)
			}
			if mode == "radio-change" {
				desired[0].payload["radios"].([]any)[0].(map[string]any)["txPower"] = float64(18)
			}
			if mode == "incomplete-create" {
				delete(desired[0].payload, "radios")
			}
			if mode == "optional-create" {
				delete(desired[0].payload, "macAddress")
				delete(desired[0].payload["position"].(map[string]any), "z")
				desired[0].payload["radios"] = []any{map[string]any{}}
			}

			floorID := "floor"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, plannedAPCatalogFixture)
				case sitesPath:
					sites := sampleSites()
					sites = append(sites, plannedAPTestFloor(floorID, "Global/Lab/Building/Floor"))
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case plannedAPPositionsPath("floor"), plannedAPPositionsPath("replacement"):
					json.NewEncoder(w).Encode(map[string]any{"response": actual})
				case plannedAPPositionsPath("floor") + "/bulk", plannedAPPositionsPath("floor") + "/bulkChange":
					if r.Method != http.MethodPost {
						t.Error("wrong mutation verb")
					}
					writes++
					var batch []plannedAPObservation
					if json.NewDecoder(r.Body).Decode(&batch) != nil || len(batch) != 1 {
						t.Error("invalid batch")
						w.WriteHeader(400)
						return
					}
					if mode == "update" {
						if batch[0].ID != "ap-id" || batch[0].Radios[0].ID != "radio-id" || len(batch[0].Radios[0].Bands) != 0 {
							t.Error("edit identity lost or read-only bands submitted")
						}
						batch[0].Radios[0].Bands = []float64{5}
					} else {
						if batch[0].ID != "" {
							t.Error("create supplied fabricated identity")
						}
						batch[0].ID = "ap-id"
						batch[0].Radios[0].ID = "radio-id"
					}
					if mode == "optional-create" {
						if batch[0].MAC != nil || batch[0].Position.Z != nil || batch[0].Radios[0].Channel != nil || batch[0].Radios[0].Antenna != nil || len(batch[0].Radios[0].Bands) != 0 {
							t.Error("optional defaults fabricated in request")
						}
						var defaults plannedAPObservation
						json.Unmarshal([]byte(plannedAPObservedFixture), &defaults)
						batch[0].MAC = defaults.MAC
						batch[0].Position.Z = defaults.Position.Z
						batch[0].Radios = defaults.Radios
					}
					actual = batch
					if mode == "bad-task" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"planned-task"}}`)
					}
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &plannedAPExecutor{client: c, desired: desired}
			write, err := e.next(context.Background())
			if mode == "incomplete-create" || mode == "radio-change" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("incomplete create accepted")
				}
				return
			}
			if err != nil || write == nil || writes != 0 {
				t.Fatal("planning failed", err)
			}
			task, err := write.submit(context.Background())
			if mode == "bad-task" {
				if err == nil {
					t.Fatal("unknown acceptance treated as receipt")
				}
				return
			}
			if err != nil || task.ID != "planned-task" || writes != 1 {
				t.Fatal("submission failed", err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
			ok, err := e.verify(context.Background(), op)
			if err != nil || !ok {
				t.Fatal("readback failed", err)
			}
			if next, err := e.next(context.Background()); err != nil || next != nil {
				t.Fatal("converged position replayed", err)
			}
			floorID = "replacement"
			if ok, err := e.verify(context.Background(), op); err == nil || ok {
				t.Fatal("replaced floor accepted")
			}
		})
	}
}
