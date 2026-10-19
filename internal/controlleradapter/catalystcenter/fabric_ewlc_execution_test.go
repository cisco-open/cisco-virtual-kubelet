// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFabricEWLCGuardedWrite(t *testing.T) {
	for _, mode := range []string{"update", "enable", "disable", "noop", "disabled-needs-reload", "denied", "late-settings", "lost-task", "accepted-no-body", "server-error", "nil-guard"} {
		t.Run(mode, func(t *testing.T) {
			var writes, guards atomic.Int32
			c, want := ewlcComposedFixture(t, mode, func(w http.ResponseWriter, r *http.Request) bool {
				endpoint := "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting"
				if r.Method == "GET" && r.URL.Path == endpoint && (mode == "enable" || mode == "disabled-needs-reload") {
					rows := []fabricEWLCSetting{}
					if mode == "disabled-needs-reload" {
						rows = append(rows, fabricEWLCSetting{ID: "edge-id", Enabled: false, Rolling: fabricEWLCRolling{Percentage: 25}})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return true
				}
				if r.Method != "PUT" {
					return false
				}
				writes.Add(1)
				if r.URL.Path != endpoint || guards.Load() != 1 {
					t.Error("write escaped endpoint or authorization")
				}
				var body fabricEWLCSetting
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "edge-id" || body.Enabled != (mode != "disable") || body.Rolling.Percentage != 25 {
					t.Error("invalid native body", body)
				}
				switch mode {
				case "lost-task":
					fmt.Fprint(w, `{"response":{}}`)
				case "accepted-no-body":
					w.WriteHeader(202)
				case "server-error":
					w.WriteHeader(500)
				default:
					fmt.Fprint(w, `{"response":{"taskId":"task-one"}}`)
				}
				return true
			})
			want.settings.rolling = true
			if mode == "noop" || mode == "disabled-needs-reload" {
				want.settings.rolling = false
			}
			if mode == "disable" || mode == "disabled-needs-reload" {
				want.settings.enabled = false
			}
			guard := func(context.Context) error {
				guards.Add(1)
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			}
			if mode == "nil-guard" {
				guard = nil
			}
			write, err := c.planGuardedFabricEWLC(context.Background(), want, guard)
			if mode == "disabled-needs-reload" || mode == "nil-guard" {
				if err == nil || write != nil || writes.Load() != 0 {
					t.Fatal("unsafe plan", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "noop" {
				if write != nil || writes.Load() != 0 || guards.Load() != 0 {
					t.Fatal("noop wrote")
				}
				return
			}
			if write == nil || !strings.HasPrefix(write.resource, "fabric-ewlc-update:") || write.identity.target != "edge-id" || write.identity.parent != "fabric" {
				t.Fatal("wrong plan", write)
			}
			task, err := write.submit(context.Background())
			success := mode == "enable" || mode == "disable" || mode == "update"
			if success {
				if err != nil || task.ID != "task-one" {
					t.Fatal(task, err)
				}
			} else if err == nil || task.ID != "" {
				t.Fatal("failure treated as acceptance", task, err)
			}
			expectedWrites := int32(1)
			if mode == "denied" || mode == "late-settings" {
				expectedWrites = 0
			}
			if writes.Load() != expectedWrites {
				t.Fatal("unexpected writes", writes.Load())
			}
			if mode == "late-settings" && (!errors.Is(err, errConfigurationStale) || guards.Load() != 0) {
				t.Fatal("wrong stale refusal", err)
			}
			if _, err := write.submit(context.Background()); !errors.Is(err, errConfigurationTransition) || writes.Load() != expectedWrites {
				t.Fatal("write replayed", err)
			}
		})
	}
}
