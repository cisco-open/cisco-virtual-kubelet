// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

const wirelessFeatureGroupModel = `{"catalyst_center":{"feature_templates":{"wireless":{"rrm_fra":[{"name":"FRA","radio_band":"2.4GHz_5GHz","fra_interval":3}],"cleanair":[{"name":"CA","radio_band":"2.4GHz","enable":false}]}}}}`

func TestWirelessFeatureGroupDispatchFence(t *testing.T) {
	for _, selected := range []string{"fra", "ca"} {
		modes := []string{"stable", "parallel", "companion-edit", "companion-id", "companion-system", "companion-missing", "companion-malformed", "desired-companion", "unselected", "ambiguous", "noop"}
		if selected == "fra" {
			modes = append(modes, "companion-appearance")
		}
		for _, mode := range modes {
			t.Run(selected+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				fra := &wirelessFRA{ID: "fra-id", Name: "FRA", Attributes: wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"}}
				ca := &wirelessCleanAir{ID: "ca-id", Name: "CA", Attributes: wirelessCleanAirAttributes{Band: "2_4GHZ"}}
				fraSystem, caSystem := false, false
				unselected := false
				if mode != "noop" {
					if selected == "fra" {
						fra.Attributes.Interval = 6
					} else {
						ca.Attributes.Enable = true
					}
				}
				if mode == "companion-appearance" {
					ca = nil
				}
				var writes, reads atomic.Int32
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == authPath:
						fmt.Fprint(w, `{"Token":"test"}`)
					case r.URL.Path == wirelessFeatureSummaryPath:
						reads.Add(1)
						kind := r.URL.Query().Get("type")
						rows := []wirelessFeatureInstance{}
						if kind == wirelessFRAKind && fra != nil {
							rows = append(rows, wirelessFeatureInstance{ID: fra.ID, Name: fra.Name, System: &fraSystem})
						}
						if kind == wirelessCleanAirKind && ca != nil {
							rows = append(rows, wirelessFeatureInstance{ID: ca.ID, Name: ca.Name, System: &caSystem})
						}
						if unselected && kind == wirelessFRAKind {
							rows = append(rows, wirelessFeatureInstance{ID: "outside-id", Name: "UNSELECTED", System: &fraSystem})
						}
						json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": kind, "count": len(rows), "instances": rows}}})
					case r.Method == http.MethodGet && fra != nil && r.URL.Path == wirelessFRAPath+"/"+fra.ID:
						reads.Add(1)
						json.NewEncoder(w).Encode(map[string]any{"response": fra})
					case r.Method == http.MethodGet && ca != nil && r.URL.Path == wirelessCleanAirPath+"/"+ca.ID:
						reads.Add(1)
						json.NewEncoder(w).Encode(map[string]any{"response": ca})
					case r.Method == http.MethodPut && (r.URL.Path == wirelessFRAPath+"/fra-id" || r.URL.Path == wirelessCleanAirPath+"/ca-id"):
						writes.Add(1)
						if mode == "ambiguous" {
							fmt.Fprint(w, `{}`)
							return
						}
						fmt.Fprint(w, `{"response":{"taskId":"feature-task"}}`)
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
						w.WriteHeader(404)
					}
				})
				plan, err := c.compileConfigurationApply([]byte(wirelessFeatureGroupModel), []string{"feature_templates"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				write, err := plan.executor.next(ctx)
				if mode == "noop" {
					if err != nil || write != nil || reads.Load() != 4 || writes.Load() != 0 {
						t.Fatal("no-op added inventory passes or writes", reads.Load(), writes.Load(), err)
					}
					return
				}
				prefix := "rrm_fra/rrm-fra:FRA"
				if selected == "ca" {
					prefix = "cleanair/cleanair:CA"
				}
				if err != nil || write == nil || write.resource != prefix {
					t.Fatal("incorrect selection", selected, write, err)
				}
				if mode != "companion-appearance" && reads.Load() != 4 {
					t.Fatal("planning repeated selected inventory", reads.Load())
				}
				switch mode {
				case "companion-edit":
					if selected == "fra" {
						ca.Attributes.Reporting = true
					} else {
						fra.Attributes.Freeze = true
					}
				case "companion-id":
					if selected == "fra" {
						ca.ID = "replacement-id"
					} else {
						fra.ID = "replacement-id"
					}
				case "companion-system":
					if selected == "fra" {
						caSystem = true
					} else {
						fraSystem = true
					}
				case "companion-missing":
					if selected == "fra" {
						ca = nil
					} else {
						fra = nil
					}
				case "companion-malformed":
					if selected == "fra" {
						ca.Attributes.Band = "unknown"
					} else {
						fra.Attributes.Interval = 0
					}
				case "companion-appearance":
					ca = &wirelessCleanAir{ID: "ca-id", Name: "CA", Attributes: wirelessCleanAirAttributes{Band: "2_4GHZ"}}
				case "desired-companion":
					group := plan.executor.(*wirelessConfiguration).configurationExecutor.(*wirelessFeatureGroup)
					for _, part := range group.parts {
						if e, ok := part.executor.(*wirelessCleanAirExecutor); ok && selected == "fra" {
							e.desired[0].Attributes.Reporting = true
						}
						if e, ok := part.executor.(*wirelessFRAExecutor); ok && selected == "ca" {
							e.desired[0].Attributes.Freeze = true
						}
					}
				case "unselected":
					unselected = true
				}
				if mode == "parallel" {
					results := make(chan error, 16)
					for range 16 {
						go func() { _, err := write.submit(ctx); results <- err }()
					}
					success := 0
					for range 16 {
						err := <-results
						if err == nil {
							success++
						} else if err != errConfigurationTransition {
							t.Fatal(err)
						}
					}
					if success != 1 {
						t.Fatal("parallel permit count", success)
					}
				} else {
					_, err = write.submit(ctx)
				}
				allowed := mode == "stable" || mode == "parallel" || mode == "unselected" || mode == "ambiguous"
				if allowed && writes.Load() != 1 || !allowed && (err == nil || writes.Load() != 0) {
					t.Fatal("cohort fence escaped", mode, writes.Load(), err)
				}
				if allowed && mode != "ambiguous" && err != nil {
					t.Fatal(err)
				}
				if mode == "stable" && reads.Load() != 8 {
					t.Fatal("dispatch repeated selected inventory", reads.Load())
				}
				if _, err = write.submit(ctx); err != errConfigurationTransition {
					t.Fatal("cohort permit reused", err)
				}
				if allowed && writes.Load() != 1 || !allowed && writes.Load() != 0 {
					t.Fatal("native replay", writes.Load())
				}
			})
		}
	}
}
