// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestPnPRegistrationCreatedJournalRecovery(t *testing.T) {
	for _, mode := range []string{"verified", "lost", "foreign-readback", "pruned-receipt", "foreign-owner", "changed-source", "late-source", "undeclared-intent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(pnpIntentFixture())
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			wants, err := parsePnPInventory(config.Spec.Source.Inline.Raw)
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			foreign := false
			lateSource := false
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == devicesPath && r.Method == "GET" {
					fmt.Fprint(w, `{"response":[]}`)
					return
				}
				if r.URL.Path != pnpDevicesPath {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				row := pnpFixture("created-id", "FOC12345678")
				row["deviceInfo"].(map[string]any)["hostname"] = "edge"
				if foreign {
					row["id"] = "foreign-id"
				}
				if r.Method == "GET" {
					if lateSource {
						lateSource = false
						config.Spec.Source.Inline.Raw = []byte(`{"changed":true}`)
						if err := a.statusWriter.Update(ctx, config); err != nil {
							t.Error(err)
						}
					}
					rows := []map[string]any{}
					if writes > 0 {
						rows = append(rows, row)
					}
					json.NewEncoder(w).Encode(rows)
					return
				}
				if r.Method != "POST" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				writes++
				if mode == "lost" {
					fmt.Fprint(w, `{`)
					return
				}
				json.NewEncoder(w).Encode(row)
			})
			write, existingID, err := a.planPnPRegistrationStage(ctx, config, nil, wants[0])
			if err != nil || write == nil || existingID != "" {
				t.Fatal("new registration stage", err)
			}
			err = a.submitConfigurationWrite(ctx, config, nil, "inventory", write)
			if (err == nil) != (mode != "lost") {
				t.Fatal("wrong submission result", err)
			}
			// Serialize/reconstruct the journal to prove verification does not use
			// an in-memory receipt captured by the submission closure.
			raw, _ := json.Marshal(journalState(t, a).Operation)
			var recovered cvk.NetworkControllerConfigurationOperation
			if json.Unmarshal(raw, &recovered) != nil {
				t.Fatal("journal decoding")
			}
			if mode == "pruned-receipt" {
				recovered.CreatedResourceID = ""
			}
			foreign = mode == "foreign-readback"
			done, err := a.client.observePnPRegistrationOperation(ctx, wants[0], &recovered)
			if mode != "lost" && mode != "foreign-readback" && mode != "pruned-receipt" {
				if err != nil || !done {
					t.Fatal("restart readback failed", err)
				}
			} else if err == nil || done {
				t.Fatal("unowned/pruned receipt accepted")
			}
			if mode != "lost" && mode != "foreign-readback" && mode != "pruned-receipt" {
				if err := a.advanceConfiguration(ctx, recovered.ID, "Verified", ""); err == nil {
					t.Fatal("generic verification bypass")
				}
				staleConfig := config.DeepCopy()
				switch mode {
				case "foreign-owner":
					nc, _ := a.currentController(ctx)
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				case "changed-source":
					config.Spec.Source.Inline.Raw = []byte(`{"changed":true}`)
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				case "late-source":
					lateSource = true
				case "undeclared-intent":
					wants[0].Site = "Global/Other"
				}
				err := a.recordOwnedPnPRegistration(ctx, staleConfig, nil, wants[0], recovered.ID)
				if mode == "verified" {
					if err != nil {
						t.Fatal("owned verification", err)
					}
					state := journalState(t, a)
					if !pnpRegistrationReceiptPresent(state, state.Operation) {
						t.Fatal("missing durable ownership")
					}
					for _, mutation := range []string{"missing", "duplicate", "pruned", "foreign-uuid"} {
						nc, _ := a.currentController(ctx)
						original := nc.DeepCopy()
						switch mutation {
						case "missing":
							nc.Status.Configuration.ActionApplications = nil
						case "duplicate":
							nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, nc.Status.Configuration.ActionApplications[0])
						case "pruned":
							nc.Status.Configuration.Operation.CreatedResourceID = ""
							nc.Status.Configuration.ActionApplications[0].Operation.CreatedResourceID = ""
						case "foreign-uuid":
							nc.Status.Configuration.ActionApplications[0].Operation.CreatedResourceID = "foreign-id"
						}
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						if err := a.finishConfiguration(ctx, recovered.ID); err == nil {
							t.Fatal("released invalid receipt", mutation)
						}
						restored, _ := a.currentController(ctx)
						restored.Status = original.Status
						if err := a.statusWriter.Status().Update(ctx, restored); err != nil {
							t.Fatal(err)
						}
					}
					if err := a.finishConfiguration(ctx, recovered.ID); err != nil {
						t.Fatal(err)
					}
					state = journalState(t, a)
					if state.Operation != nil || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.CreatedResourceID != "created-id" {
						t.Fatal("ownership lost on finish")
					}
					return
				}
				if err == nil || len(journalState(t, a).ActionApplications) != 0 {
					t.Fatal("stale/foreign intent finalized", mode)
				}
			}
			if err := a.submitConfigurationWrite(ctx, config, nil, "inventory", write); err == nil || writes != 1 {
				t.Fatal("registration replayed")
			}
		})
	}
}
