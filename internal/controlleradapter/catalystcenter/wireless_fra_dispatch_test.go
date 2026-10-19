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

func TestWirelessFRADispatchFence(t *testing.T) {
	for _, mode := range []string{"stable", "parallel", "band", "freeze", "status", "interval", "sensitivity", "unlocked", "identity", "system", "sibling", "appearance", "desired", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			desired, err := parseWirelessFRA([]byte(wirelessFRAModel))
			if err != nil {
				t.Fatal(err)
			}
			sibling := desired[0]
			sibling.Name = "sibling"
			desired = append(desired, sibling)
			actual := desired[0]
			actual.ID = "fra-id"
			actual.Attributes.Interval = 6
			sibling.ID = "sibling-id"
			rows := map[string]wirelessFRA{actual.Name: actual, sibling.Name: sibling}
			if mode == "appearance" {
				delete(rows, actual.Name)
			}
			system := false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessFeatureSummaryPath:
					instances := []wirelessFeatureInstance{}
					for _, row := range rows {
						instances = append(instances, wirelessFeatureInstance{ID: row.ID, Name: row.Name, System: &system})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": wirelessFRAKind, "count": len(instances), "instances": instances}}})
				case r.Method == http.MethodGet:
					for _, row := range rows {
						if r.URL.Path == wirelessFRAPath+"/"+row.ID {
							json.NewEncoder(w).Encode(map[string]any{"response": row})
							return
						}
					}
					w.WriteHeader(404)
				default:
					writes++
					if mode == "ambiguous" {
						fmt.Fprint(w, `{}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"fra-task"}}`)
				}
			})
			e := &wirelessFRAExecutor{client: c, desired: desired}
			write, err := e.next(ctx)
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			switch mode {
			case "band":
				actual.Attributes.Band = "5GHZ_6GHZ"
				actual.Attributes.Sensitivity = ""
			case "freeze":
				actual.Attributes.Freeze = true
			case "status":
				actual.Attributes.Status = true
			case "interval":
				actual.Attributes.Interval = 24
			case "sensitivity":
				actual.Attributes.Sensitivity = "High"
			case "unlocked":
				actual.Unlocked = []string{"fraStatus"}
			case "identity":
				actual.ID = "replacement-id"
			case "system":
				system = true
			case "sibling":
				sibling.Attributes.Interval = 12
				rows[sibling.Name] = sibling
			case "desired":
				e.desired[0].Attributes.Interval = 12
			}
			rows[actual.Name] = actual
			if mode == "parallel" {
				results := make(chan error, 16)
				for range 16 {
					go func() { _, submitErr := write.submit(ctx); results <- submitErr }()
				}
				success := 0
				for range 16 {
					submitErr := <-results
					if submitErr == nil {
						success++
					} else if submitErr != errConfigurationTransition {
						t.Fatal(submitErr)
					}
				}
				if success != 1 {
					t.Fatal("parallel permit count", success)
				}
			} else {
				_, err = write.submit(ctx)
			}
			allowed := mode == "stable" || mode == "parallel" || mode == "ambiguous"
			if allowed && writes != 1 || !allowed && (err == nil || writes != 0) {
				t.Fatal("incorrect dispatch", writes, err)
			}
			if mode == "stable" && err != nil {
				t.Fatal(err)
			}
			if _, err = write.submit(ctx); err != errConfigurationTransition {
				t.Fatal("prepared write reused", err)
			}
			if allowed && writes != 1 || !allowed && writes != 0 {
				t.Fatal("native replay", writes)
			}
		})
	}
}
