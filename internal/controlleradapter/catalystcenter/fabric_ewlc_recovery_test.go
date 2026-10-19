// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestFabricEWLCRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "publish-denied", "publish-lost", "running", "failed", "unconverged", "denied", "source-changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			writes := 0
			a, config, source, physical, _ := ewlcJournalFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) {
				writes++
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			if _, err := a.prepareFabricEWLC(ctx, config, source, "edge"); err != nil {
				t.Fatal(err)
			}
			permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
			if _, err := a.dispatchFabricEWLC(ctx, config, source, "edge", permit); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown" {
				nc.Status.Configuration.Operation.Phase = "Unknown"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			initial := nc.Status.Configuration.Operation.Phase
			if mode == "source-changed" {
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			members := []*ops.ControllerConfigurationReservation{}
			for i := range list.Items {
				members = append(members, list.Items[i].DeepCopy())
			}
			stamp := time.Now().UnixMilli()
			c, _ := ewlcComposedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != "GET" {
					t.Error("recovery mutated appliance")
					w.WriteHeader(400)
					return true
				}
				reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
				if r.URL.Path == taskPath+"task/tree" {
					end := stamp
					if mode == "running" {
						end = 0
					}
					reply([]map[string]any{{"id": "task", "startTime": stamp - 1000, "endTime": end, "isError": mode == "failed"}})
					return true
				}
				if r.URL.Path == "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting" {
					reply([]fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Enabled: mode != "unconverged", Percentage: 25}}})
					return true
				}
				return false
			})
			a.client = c
			err = a.beginFabricEWLCVerification(ctx, config, source, "edge", members, func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			}, time.Now().Add(time.Second))
			valid := mode == "accepted" || mode == "unknown" || mode == "publish-denied" || mode == "publish-lost"
			if (err == nil) != valid {
				t.Fatal("recovery outcome", err)
			}
			state := journalState(t, a)
			expected := initial
			if valid {
				expected = "Verifying"
			}
			if state.Operation == nil || state.Operation.Phase != expected || state.Operation.TaskID != "task" || len(state.ActionApplications) != 0 || writes != 1 {
				t.Fatal("recovery lost journal or published prematurely", state)
			}
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			for _, member := range list.Items {
				if member.Status.Grant == nil || member.Status.Grant.Phase != "Held" {
					t.Fatal("recovery released claim")
				}
			}
			if !valid {
				return
			}
			allow := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			if mode == "publish-denied" {
				err := a.recordFabricEWLCApplication(ctx, config, state.Operation.ID, "edge", members, func(context.Context, []*ops.ControllerConfigurationReservation) error {
					return errConfigurationDeviceAdmission
				}, time.Now())
				unchanged := journalState(t, a)
				if err == nil || unchanged.Operation.Phase != "Verifying" || len(unchanged.ActionApplications) != 0 {
					t.Fatal("denied publication changed journal", err)
				}
			}
			writer := a.statusWriter
			if mode == "publish-lost" {
				a.statusWriter = lostJournalAckClient{writer}
			}
			err = a.recordFabricEWLCApplication(ctx, config, state.Operation.ID, "edge", members, allow, time.Now())
			a.statusWriter = writer
			if (err != nil) != (mode == "publish-lost") {
				t.Fatal("publication outcome", err)
			}
			published := journalState(t, a)
			if published.Operation.Phase != "Verified" || len(published.ActionApplications) != 1 || published.ActionApplications[0].Section != "inventory" {
				t.Fatal("missing atomic publication", published)
			}
			if err := a.recordFabricEWLCApplication(ctx, config, state.Operation.ID, "edge", members, allow, time.Now()); err == nil {
				t.Fatal("publication replay accepted")
			}
			testFabricHandoffCompletion(t, a, config, true)
			if next, err := a.prepareFabricEWLC(ctx, config, source, "edge"); err != nil || next != nil {
				t.Fatal("converged state prepared another operation", next, err)
			}
			if final := journalState(t, a); final.Operation != nil || len(final.ActionApplications) != 1 || writes != 1 {
				t.Fatal("terminal lifecycle lost ownership or replayed write", final)
			}

		})
	}
}
