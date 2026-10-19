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
)

const plannedAPCatalogFixture = `[{"apType":"C9130AXI","antennaPatterns":[{"band":"5","names":["Internal"]}]}]`

func TestPlannedAPCatalogPreflight(t *testing.T) {
	for _, mode := range []string{"valid", "numbered-choice", "raw-choice", "duplicate-choice", "unknown-model", "unknown-band", "unknown-antenna", "antenna-without-band", "band-without-antenna", "default-radio", "later-invalid", "null", "envelope", "duplicate-model", "duplicate-band", "empty-names", "partial", "missing-patterns"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			radio := desired[0].payload["radios"].([]any)[0].(map[string]any)
			body := plannedAPCatalogFixture
			switch mode {
			case "numbered-choice":
				body = strings.Replace(body, `"Internal"`, `"1_Internal"`, 1)
			case "raw-choice":
				body = strings.Replace(body, `"Internal"`, `"1_Internal"`, 1)
				radio["antenna"].(map[string]any)["name"] = "1_Internal"
			case "duplicate-choice":
				body = strings.Replace(body, `["Internal"]`, `["1_Internal","2_Internal"]`, 1)
			case "unknown-model":
				desired[0].payload["type"] = "unsupported"
			case "unknown-band":
				radio["bands"] = []float64{6}
			case "unknown-antenna":
				radio["antenna"].(map[string]any)["name"] = "unsupported"
			case "antenna-without-band":
				delete(radio, "bands")
			case "band-without-antenna":
				delete(radio, "antenna")
			case "default-radio":
				desired[0].payload["radios"] = []any{map[string]any{}}
			case "later-invalid":
				desired = append(desired, plannedAPIntent{floor: desired[0].floor, payload: map[string]any{"type": "unsupported"}})
			case "null":
				body = "null"
			case "envelope":
				body = `{"response":` + body + `}`
			case "duplicate-model":
				body = "[" + body[1:len(body)-1] + "," + body[1:len(body)-1] + "]"
			case "duplicate-band":
				body = `[{"apType":"C9130AXI","antennaPatterns":[{"band":"5","names":["Internal"]},{"band":"5","names":["Other"]}]}]`
			case "empty-names":
				body = strings.Replace(body, `["Internal"]`, `[]`, 1)
			case "missing-patterns":
				body = `[{"apType":"C9130AXI"}]`
			}
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != http.MethodGet || r.URL.Path != plannedAPCatalogPath {
					t.Error("unexpected API call")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				fmt.Fprint(w, body)
			})
			err = c.validatePlannedAPModels(context.Background(), desired)
			valid := mode == "valid" || mode == "numbered-choice" || mode == "antenna-without-band" || mode == "band-without-antenna" || mode == "default-radio"
			if (err == nil) != valid || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestPlannedAPInvalidCatalogSelectionPrecedesGeometry(t *testing.T) {
	sites, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
	if err != nil {
		t.Fatal(err)
	}
	desired[0].payload["type"] = "unsupported"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case plannedAPCatalogPath:
			fmt.Fprint(w, plannedAPCatalogFixture)
		default:
			t.Error("invalid planned AP reached geometry or mutation API")
			w.WriteHeader(400)
		}
	})
	e := &sitePlannedAPExecutor{sites: &siteConfigurationExecutor{client: c, desired: sites}, placements: &plannedAPExecutor{client: c, desired: desired}}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("unsupported model escaped whole-source preflight")
	}
}

func TestPlannedAPCatalogChangedBeforeSubmission(t *testing.T) {
	_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
	if err != nil {
		t.Fatal(err)
	}
	catalog := plannedAPCatalogFixture
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case plannedAPCatalogPath:
			fmt.Fprint(w, catalog)
		case sitesPath:
			sites := append(sampleSites(), plannedAPTestFloor("floor", desired[0].floor))
			json.NewEncoder(w).Encode(map[string]any{"response": sites})
		case plannedAPPositionsPath("floor"):
			fmt.Fprint(w, `{"response":[]}`)
		default:
			writes++
			t.Error("unsupported selection reached mutation")
			w.WriteHeader(400)
		}
	})
	write, err := (&plannedAPExecutor{client: c, desired: desired}).next(context.Background())
	if err != nil || write == nil {
		t.Fatal("valid original plan", err)
	}
	catalog = strings.Replace(catalog, `"Internal"`, `"Different"`, 1)
	if _, err := write.submit(context.Background()); err == nil || writes != 0 {
		t.Fatal("changed catalog allowed stale submission")
	}
}
