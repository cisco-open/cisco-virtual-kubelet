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
	"time"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAOwnedRecovery(t *testing.T) {
	for _, mode := range []string{"observe", "record", "owner", "no-owner", "duplicate-owner", "source", "wrong-operation", "late-source", "late-journal", "record-owner", "record-late-source", "record-late-journal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(rmaModel(`{"action":"REPLACE","replacement_serial_number":"NEW-SERIAL"}`), "FOC12345678", "OLD-SERIAL", 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			ref, want, devices, records := rmaBindingFixture()
			now := time.Now().UTC().Truncate(time.Second)
			submitted := now.Add(-time.Minute)
			records[0].CreatedAt = submitted.UnixMilli()
			records[0].Status = "MARKED-FOR-REPLACEMENT"
			snapshot, err := bindDeviceReplacement(ref, want, devices, nil)
			if err != nil {
				t.Fatal(err)
			}
			resource, err := deviceReplacementMarkResource(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.prepareConfiguration(ctx, config, nil, "inventory", resource, "Create", configurationResourceIdentity{target: "old-id", serial: "old-serial"})
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = "Accepted"
			if strings.HasPrefix(mode, "record") {
				nc.Status.Configuration.Operation.Phase = "Verifying"
			}
			nc.Status.Configuration.Operation.TaskID = "mark-task"
			nc.Status.Configuration.Operation.SubmittedAt = &metav1.Time{Time: submitted}
			kind := strings.TrimPrefix(mode, "record-")
			switch kind {
			case "owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "no-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "source":
				nc.Status.Configuration.Operation.SourceVersion = "other"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation.DeepCopy()
			if mode == "wrong-operation" {
				op.ID = "other"
			}
			lateDone := false
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("recovery mutated native state")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					rows = records
				case taskPath + "mark-task":
					if !lateDone {
						lateDone = true
						switch kind {
						case "late-source":
							changed := config.DeepCopy()
							changed.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)
							if err := a.statusWriter.Update(ctx, changed); err != nil {
								t.Error(err)
							}
						case "late-journal":
							changed, err := a.currentController(ctx)
							if err != nil {
								t.Error(err)
								break
							}
							changed.Status.Configuration.Operation.Phase = "Unknown"
							if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
								t.Error(err)
							}
						}
					}
					rows = map[string]any{"id": "mark-task", "isError": false, "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli()}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			executor := &deviceReplacementMarkExecutor{adapter: a, config: config, reference: ref, desired: want}
			if mode == "record" {
				verified, err := executor.verify(ctx, op)
				if err != nil || !verified {
					t.Fatal("executor verification failed", err)
				}
				if journalState(t, a).Operation.Phase != "Verifying" {
					t.Fatal("verification advanced journal")
				}
			}
			if strings.HasPrefix(mode, "record") {
				plan := configurationApplyPlan{section: "inventory", recordCompletion: executor.recordCompletion}
				err = plan.completeWithReceipt(ctx, a, op)
				if (err == nil) != (mode == "record") {
					t.Fatal("unexpected receipt result", err)
				}
			} else {
				done, err := executor.observeCompletion(ctx, op, now)
				if (err == nil) != (mode == "observe") || done != (mode == "observe") {
					t.Fatal("unowned evidence accepted", err)
				}
			}
			fresh, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state := fresh.Status.Configuration
			if mode != "record" {
				if state.Operation.Phase == "Verified" || len(state.ActionApplications) != 0 {
					t.Fatal("invalid completion published")
				}
				return
			}
			if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || state.ActionApplications[0].Resource != "rma-mark-record:record" || state.ActionApplications[0].Operation.Resource != resource {
				t.Fatal("record UUID or admitted operation lost")
			}
			before, err := contract.OperationDigest(op)
			if err != nil {
				t.Fatal(err)
			}
			after, err := contract.OperationDigest(state.Operation)
			if err != nil || before != after {
				t.Fatal("completion changed reservation binding", err)
			}
			// Synthetic actor state only: proves that the existing completion contract
			// accepts the unchanged operation with the native-result receipt key.
			reservation, _ := settledConfigurationFixture(t, a)
			if reservation.Status.Completion == nil {
				t.Fatal("shared completion contract rejected native-result receipt")
			}
			if err := a.recordOwnedDeviceReplacementMark(ctx, config, nil, ref, want, op.ID, now); err == nil {
				t.Fatal("repeated completion accepted")
			}
			// Simulate completed settlement; no real worker/Lease is exercised.
			fresh, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			fresh.Status.Configuration.Operation = nil
			if err := a.statusWriter.Status().Update(ctx, fresh); err != nil {
				t.Fatal(err)
			}
			restarted := *a
			if next, err := restarted.planOwnedDeviceReplacementMark(ctx, config, nil, ref, want); err != nil || next != nil {
				t.Fatal("owned native mark replayed after settlement", err)
			}
			records[0].ID = "replacement-record"
			if next, err := restarted.planOwnedDeviceReplacementMark(ctx, config, nil, ref, want); err == nil || next != nil {
				t.Fatal("replaced native record adopted")
			}
		})
	}
}

func TestRMAOwnedNewMarkPlanning(t *testing.T) {
	for _, mode := range []string{"new", "active", "no-owner", "late-owner", "late-source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`), "FOC12345678", "OLD-SERIAL", 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			ref, _, devices, _ := rmaBindingFixture()
			want := deviceReplacementIntent{action: rmaMark}
			if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", "pending", "Create", configurationResourceIdentity{target: "old-id", serial: "old-serial"}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "active" {
				nc.Status.Configuration.Operation = nil
			}
			if mode == "no-owner" {
				nc.Status.Configuration.Owners = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			reads := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("planner submitted native write")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					reads++
					if reads == 2 {
						if mode == "late-owner" {
							changed, err := a.currentController(ctx)
							if err != nil {
								t.Error(err)
								return
							}
							changed.Status.Configuration.Owners[0].ConfigUID = "other"
							if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
								t.Error(err)
							}
						}
						if mode == "late-source" {
							changed := config.DeepCopy()
							changed.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)
							if err := a.statusWriter.Update(ctx, changed); err != nil {
								t.Error(err)
							}
						}
					}
					rows = devices
				case deviceReplacementPath:
					rows = []deviceReplacementRecord{}
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			write, err := a.planOwnedDeviceReplacementMark(ctx, config, nil, ref, want)
			if (err == nil) != (mode == "new") || (write != nil) != (mode == "new") {
				t.Fatal("unexpected plan", write, err)
			}
			if mode == "new" && (write.identity.target != "old-id" || write.identity.serial != "old-serial") {
				t.Fatal("lost physical binding")
			}
			if mode == "active" && reads != 0 {
				t.Fatal("active journal was replanned")
			}
		})
	}
}
