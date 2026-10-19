// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPnPPreparedClaimHandoff(t *testing.T) {
	for _, mode := range []string{"valid", "ignored-native-output", "state", "workflow-state", "work-item", "replaced-id", "legacy-prepared", "future-version", "corrupt-binding", "source", "owner", "missing-receipt", "wrong-operation", "no-admission", "late-state", "late-source", "denied-permit", "lost-response", "submitting", "unknown", "verifying"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []pnpDeviceIntent{want}}}})
			config.Spec.Source.Inline.Raw = raw
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			nativeState, workflowState, itemState, recordID := "Unclaimed", "native-state", "original-item", "owned-id"
			writes, claims, checks := 0, 0, 0
			prepared, resuming := false, false
			events := []string{}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" {
					if resuming {
						events = append(events, "native-read")
					}
					switch r.URL.Path {
					case pnpDevicesPath:
						if prepared && mode == "late-state" && journalState(t, a).Operation.Phase == "Submitting" {
							nativeState = "changed"
						}
						if prepared && mode == "late-source" && journalState(t, a).Operation.Phase == "Submitting" {
							copy := config.DeepCopy()
							copy.Generation++
							if err := a.statusWriter.Update(ctx, copy); err != nil {
								t.Error(err)
							}
						}
						row := pnpFixture(recordID, want.Serial)
						info := row["deviceInfo"].(map[string]any)
						info["hostname"] = want.Name
						info["state"] = nativeState
						row["workflow"] = map[string]any{"_id": "workflow", "state": workflowState, "tasks": []any{map[string]any{"state": "parent-stable", "workItemList": []any{map[string]any{"state": itemState, "command": "PRIVATE-COMMAND", "outputStr": "PRIVATE-OUTPUT"}}}}}
						if prepared && mode == "ignored-native-output" {
							row["dayZeroConfig"] = map[string]any{"config": "CHANGED-PRIVATE-CONFIG"}
							row["workflow"].(map[string]any)["tasks"].([]any)[0].(map[string]any)["workItemList"].([]any)[0].(map[string]any)["outputStr"] = "CHANGED-PRIVATE-OUTPUT"
						}
						json.NewEncoder(w).Encode([]any{row})
					case devicesPath:
						fmt.Fprint(w, `{"response":[]}`)
					case sitesPath:
						json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					default:
						t.Error("unexpected read", r.URL.Path)
						w.WriteHeader(400)
					}
					return
				}
				if r.Method != "POST" || r.URL.Path != pnpSiteClaimPath {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				events = append(events, "claim-post")
				writes++
				if mode == "lost-response" {
					w.WriteHeader(502)
					return
				}
				fmt.Fprint(w, `{"response":"accepted"}`)
			})
			body, _ := pnpRegistrationPayload(want)
			registration := &configurationWrite{resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), action: "Create", submitCreated: func(context.Context) (string, error) { return "owned-id", nil }}
			if err := a.submitConfigurationWrite(ctx, config, nil, "inventory", registration); err != nil {
				t.Fatal(err)
			}
			id := journalState(t, a).Operation.ID
			if err := a.recordOwnedPnPRegistration(ctx, config, nil, want, id); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, id); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareOwnedPnPSiteClaim(ctx, config, nil, want, nil)
			if err != nil {
				t.Fatal(err)
			}
			if writes != 0 || claims != 0 || op.Phase != "Prepared" || !strings.HasPrefix(op.Resource, "pnp-claim:v2:") {
				t.Fatal("preparation dispatched or lacked its native baseline")
			}
			nc, _ := a.currentController(ctx)
			encoded, _ := json.Marshal(nc.Status.Configuration)
			if strings.Contains(string(encoded), "PRIVATE-") {
				t.Fatal("private native values entered the journal")
			}
			copy := nc.DeepCopy()
			if err := json.Unmarshal(encoded, &copy.Status.Configuration); err != nil {
				t.Fatal(err)
			}
			if err := a.statusWriter.Status().Update(ctx, copy); err != nil {
				t.Fatal(err)
			}
			rebuilt := *a
			a = &rebuilt
			prepared = true
			switch mode {
			case "state":
				nativeState = "changed"
			case "workflow-state":
				workflowState = "changed"
			case "work-item":
				itemState = "changed"
			case "replaced-id":
				recordID = "replacement"
			case "source":
				config = config.DeepCopy()
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			nc, _ = a.currentController(ctx)
			switch mode {
			case "legacy-prepared":
				parts := strings.Split(nc.Status.Configuration.Operation.Resource, ":")
				nc.Status.Configuration.Operation.Resource = "pnp-claim:" + parts[2]
			case "future-version":
				nc.Status.Configuration.Operation.Resource = strings.Replace(nc.Status.Configuration.Operation.Resource, ":v2:", ":v9:", 1)
			case "corrupt-binding":
				nc.Status.Configuration.Operation.Resource = "pnp-claim:v2:" + strings.Repeat("0", 64) + ":" + strings.Repeat("1", 64)
			case "owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "missing-receipt":
				nc.Status.Configuration.ActionApplications = nil
			case "submitting", "unknown", "verifying":
				nc.Status.Configuration.Operation.Phase = strings.ToUpper(mode[:1]) + mode[1:]
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			op = journalState(t, a).Operation.DeepCopy()
			if mode == "wrong-operation" {
				op.ID = strings.Repeat("0", 32)
			}
			before := journalState(t, a).DeepCopy()
			admit := configurationAdmissionStep(func(context.Context) (configurationDispatchCheck, error) {
				claims++
				events = append(events, "admit")
				return journalTestPermit(func(context.Context, time.Time) error {
					checks++
					events = append(events, "permit")
					if mode == "denied-permit" {
						return errConfigurationDeviceAdmission
					}
					return nil
				}), nil
			})
			if mode == "no-admission" {
				admit = nil
			}
			resuming = true
			err = a.resumeOwnedPnPSiteClaim(ctx, config, nil, want, nil, op, admit)
			valid := mode == "valid" || mode == "ignored-native-output"
			if (err == nil) != valid {
				t.Fatal("handoff outcome", err)
			}
			submitted := valid || mode == "lost-response"
			if submitted {
				if writes != 1 || claims != 1 || checks != 1 || len(events) < 2 || events[len(events)-2] != "permit" || events[len(events)-1] != "claim-post" {
					t.Fatal("permit was not consumed after native reads at the POST boundary", events)
				}
				expected := "Verifying"
				if mode == "lost-response" {
					expected = "Unknown"
				}
				if journalState(t, a).Operation.Phase != expected {
					t.Fatal("acknowledgement advanced completion")
				}
				retained := journalState(t, a).DeepCopy()
				if err := a.resumeOwnedPnPSiteClaim(ctx, config, nil, want, nil, retained.Operation, admit); err == nil || writes != 1 || !reflect.DeepEqual(retained, journalState(t, a)) {
					t.Fatal("accepted or uncertain work replayed")
				}
			} else if mode == "late-state" || mode == "late-source" || mode == "denied-permit" {
				if writes != 0 || claims != 1 || journalState(t, a).Operation.Phase != "Submitting" {
					t.Fatal("late refusal escaped its fence")
				}
				if mode != "denied-permit" && checks != 0 {
					t.Fatal("native/source drift consumed admission")
				}
			} else if writes != 0 || claims != 0 || checks != 0 || !reflect.DeepEqual(before, journalState(t, a)) {
				t.Fatal("stale preparation changed or requested authority")
			}
		})
	}
}
