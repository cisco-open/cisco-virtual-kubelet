// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSiteBulkPlansCompleteHierarchyWithoutSyntheticWireIDs(t *testing.T) {
	desired, err := parseSites([]byte(executionSiteModel))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planSiteBulkCreate(desired, sampleSites()[:1])
	if err != nil || plan == nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(plan.body, &rows); err != nil || len(rows) != 3 {
		t.Fatal("incomplete batch", err)
	}
	for _, row := range rows {
		if row["parentId"] != nil || row["parentNameHierarchy"] == nil || row["type"] == nil {
			t.Fatal("wrong bulk parent representation")
		}
	}
	if strings.Contains(string(plan.body), "bulk-parent") {
		t.Fatal("synthetic planning identity escaped into wire payload")
	}
	if rows[1]["country"] != "Germany" || rows[2]["floorNumber"] != float64(1) {
		t.Fatal("leaf attributes missing")
	}
}

func TestSiteBulkRefusesExistingDivergenceAndInvalidDescendants(t *testing.T) {
	for _, model := range []string{
		strings.Replace(executionSiteModel, "country: Germany, ", "", 1),
		strings.Replace(executionSiteModel, "width: 10", "width: 0.0004", 1),
		`catalyst_center: {sites: {buildings: [{name: Building, parent_name: Global/Lab, address: Changed}]}}`,
	} {
		desired, err := parseSites([]byte(model))
		if err != nil {
			t.Fatal(err)
		}
		actual := sampleSites()
		address := "Original"
		actual[2].Address = &address
		if plan, err := planSiteBulkCreate(desired, actual); err == nil || plan != nil {
			t.Fatal("unsafe bulk create accepted")
		}
	}
}

func TestSiteBulkGuardSubmissionAndReplay(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "stale-parent", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			reads, writes, guards := 0, 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reads++
					actual := sampleSites()[:1]
					if mode == "stale-parent" && reads > 1 {
						actual[0].ID = "replacement"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": actual})
				case siteBulkPath:
					writes++
					if r.Method != http.MethodPost {
						t.Error("wrong mutation verb")
					}
					if mode == "ambiguous" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"bulk-task"}}`)
					}
				default:
					t.Error("unexpected endpoint")
				}
			})
			desired, _ := parseSites([]byte(executionSiteModel))
			if submit, err := c.planGuardedSiteBulkCreate(context.Background(), desired, nil); err == nil || submit != nil {
				t.Fatal("missing guard accepted")
			}
			submit, err := c.planGuardedSiteBulkCreate(context.Background(), desired, func(context.Context) error {
				guards++
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			})
			if err != nil || submit == nil {
				t.Fatal(err)
			}
			task, err := submit(context.Background())
			if (err == nil) != (mode == "accepted") || (mode == "accepted" && task.ID != "bulk-task") {
				t.Fatalf("mode=%s task=%v err=%v", mode, task, err)
			}
			if mode == "stale-parent" && guards != 0 {
				t.Fatal("authority requested after parent replacement")
			}
			if (mode == "denied" || mode == "stale-parent") && writes != 0 {
				t.Fatal("write after refusal")
			}
			if (mode == "accepted" || mode == "ambiguous") && writes != 1 {
				t.Fatal("native batch not submitted exactly once")
			}
			if _, err := submit(context.Background()); err == nil {
				t.Fatal("closure replay accepted")
			}
		})
	}
}
