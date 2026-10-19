// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestPlannedAPDispatchRechecksNativeObservation(t *testing.T) {
	for _, mode := range []string{"stable", "appeared", "replaced", "mac", "unselected-z", "unselected-radio", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			var actual plannedAPObservation
			if err := json.Unmarshal([]byte(plannedAPObservedFixture), &actual); err != nil {
				t.Fatal(err)
			}
			delete(desired[0].payload, "macAddress")
			delete(desired[0].payload, "radios")
			delete(desired[0].payload["position"].(map[string]any), "z")
			desired[0].payload["position"].(map[string]any)["x"] = 8.0
			present := mode != "appeared"
			if !present { // Creation requires explicit radios.
				_, desired, err = parseSitesWithPlannedAPs([]byte(plannedAPFixture))
				if err != nil {
					t.Fatal(err)
				}
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, plannedAPCatalogFixture)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), plannedAPTestFloor("floor", desired[0].floor))})
				case plannedAPPositionsPath("floor"):
					aps := []plannedAPObservation{}
					if present {
						aps = append(aps, actual)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": aps})
				case plannedAPPositionsPath("floor") + "/bulk", plannedAPPositionsPath("floor") + "/bulkChange":
					writes++
					if mode == "lost-response" {
						w.WriteHeader(503)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(400)
				}
			})
			write, err := (&plannedAPExecutor{client: c, desired: desired}).next(context.Background())
			if err != nil || write == nil {
				t.Fatal("planning", err)
			}
			switch mode {
			case "appeared":
				present = true
			case "replaced":
				actual.ID = "replacement"
			case "mac":
				value := "02:00:00:00:00:02"
				actual.MAC = &value
			case "unselected-z":
				value := 3.0
				actual.Position.Z = &value
			case "unselected-radio":
				value := 13
				actual.Radios[0].TxPower = &value
			}
			_, err = write.submit(context.Background())
			proof := write.noDispatch(err)
			if mode == "stable" || mode == "lost-response" {
				if writes != 1 || proof != nil || (err != nil) != (mode == "lost-response") {
					t.Fatal("native result acquired refusal authority")
				}
			} else if writes != 0 || err == nil || proof == nil {
				t.Fatal("changed AP observation reached native dispatch")
			}
			before := writes
			if _, err := write.submit(context.Background()); err == nil || writes != before {
				t.Fatal("consumed sender replayed")
			}
		})
	}
}
