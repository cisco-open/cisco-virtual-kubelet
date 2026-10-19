// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const plannedAPAlternateType = "C9130AXI-ALT"
const plannedAPTypeCatalogFixture = `[{"apType":"C9130AXI","antennaPatterns":[{"band":"5","names":["Internal"]}]},{"apType":"C9130AXI-ALT","antennaPatterns":[{"band":"5","names":["Internal"]}]}]`

func TestPlannedAPTypeUpdate(t *testing.T) {
	for _, mode := range []string{"update", "omitted-radios", "incompatible-antenna", "incompatible-band", "unselected-radio", "late-catalog", "late-native-type", "mutated-intent-type", "radio-change", "missing-task", "ignored-type", "no-op"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			desired[0].payload["type"] = plannedAPAlternateType
			var actual plannedAPObservation
			json.Unmarshal([]byte(plannedAPObservedFixture), &actual)
			if err := validatePlannedAPObservation(&actual); err != nil {
				t.Fatal(err)
			}
			if mode == "omitted-radios" || mode == "incompatible-antenna" || mode == "incompatible-band" || mode == "unselected-radio" || mode == "late-catalog" {
				delete(desired[0].payload, "radios")
			}
			if mode == "radio-change" {
				desired[0].payload["radios"].([]any)[0].(map[string]any)["txPower"] = 18.0
			}
			if mode == "unselected-radio" {
				other := actual.Radios[0]
				other.ID = "second-radio"
				other.Bands = []float64{2.4}
				actual.Radios = append(actual.Radios, other)
			}
			if mode == "no-op" {
				actual.Type = plannedAPAlternateType
			}
			catalog := plannedAPTypeCatalogFixture
			if mode == "incompatible-antenna" {
				catalog = strings.Replace(catalog, `"C9130AXI-ALT","antennaPatterns":[{"band":"5","names":["Internal"]}`, `"C9130AXI-ALT","antennaPatterns":[{"band":"5","names":["Other"]}`, 1)
			}
			if mode == "incompatible-band" {
				catalog = strings.Replace(catalog, `"C9130AXI-ALT","antennaPatterns":[{"band":"5"`, `"C9130AXI-ALT","antennaPatterns":[{"band":"6"`, 1)
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, catalog)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), plannedAPTestFloor("floor", desired[0].floor))})
				case plannedAPPositionsPath("floor"):
					json.NewEncoder(w).Encode(map[string]any{"response": []plannedAPObservation{actual}})
				case plannedAPPositionsPath("floor") + "/bulkChange":
					writes++
					var batch []plannedAPObservation
					if json.NewDecoder(r.Body).Decode(&batch) != nil || len(batch) != 1 {
						t.Error("invalid type update")
						w.WriteHeader(400)
						return
					}
					got := batch[0]
					if got.ID != actual.ID || got.Type != plannedAPAlternateType || got.Radios[0].ID != actual.Radios[0].ID || len(got.Radios[0].Bands) != 0 || *got.Position.X != *actual.Position.X || *got.MAC != *actual.MAC {
						t.Error("type-only edit changed native identity or other fields")
					}
					if mode != "ignored-type" {
						actual.Type = got.Type
					}
					if mode == "missing-task" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(400)
				}
			})
			e := &plannedAPExecutor{client: c, desired: desired}
			write, err := e.next(context.Background())
			refused := mode == "incompatible-antenna" || mode == "incompatible-band" || mode == "unselected-radio" || mode == "radio-change"
			if refused {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("incompatible retained model/radios accepted", err)
				}
				return
			}
			if mode == "no-op" {
				if err != nil || write != nil || writes != 0 {
					t.Fatal("converged type replayed", err)
				}
				return
			}
			if err != nil || write == nil || write.action != "Update" || write.identity.target != actual.ID {
				t.Fatal("type update not planned", err)
			}
			if mode == "late-catalog" {
				catalog = strings.ReplaceAll(catalog, `"Internal"`, `"Gone"`)
			}
			if mode == "late-native-type" {
				actual.Type = plannedAPAlternateType
			}
			if mode == "mutated-intent-type" {
				desired[0].payload["type"] = "C9130AXI"
			}
			_, err = write.submit(context.Background())
			proof := write.noDispatch(err)
			late := mode == "late-catalog" || mode == "late-native-type" || mode == "mutated-intent-type"
			if late {
				if err == nil || writes != 0 || proof == nil {
					t.Fatal("late model/native change was dispatched")
				}
				return
			}
			if writes != 1 || proof != nil || (err != nil) != (mode == "missing-task") {
				t.Fatal("native outcome acquired replay authority", err)
			}
			if mode != "missing-task" {
				verified, err := e.verify(context.Background(), &cvk.NetworkControllerConfigurationOperation{Section: "sites", Resource: write.resource, Action: "Update", ParentID: "floor", TargetID: actual.ID})
				if err != nil || verified != (mode != "ignored-type") {
					t.Fatal("type readback failed or ignored type falsely verified", err)
				}
			}
			if _, err := write.submit(context.Background()); err == nil || writes != 1 {
				t.Fatal("type update replayed")
			}
		})
	}
}
