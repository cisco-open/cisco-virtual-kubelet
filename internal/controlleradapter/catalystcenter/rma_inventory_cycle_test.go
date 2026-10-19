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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Full public compiler/reconciler/journal cycle with synthetic reservation
// claims/settlement. It is not qualification of real manager/device actors.
func TestRMAMarkManifestCycle(t *testing.T) {
	for _, mode := range []string{"mark", "two-devices", "prune-multi", "prune-multi-mixed-retain", "prune-multi-mixed-add", "prune-multi-lost", "prune-multi-failed", "prune-multi-foreign", "prune-multi-late", "lost-receipt", "failed-task", "changed-record", "prune-owned", "prune-admitted", "prune-admitted-reapply", "prune-admitted-finalizer", "prune-admitted-lost", "prune-absent", "prune-missing-origin", "prune-replaced-origin", "prune-recovery-source", "prune-recovery-owner", "prune-retain", "prune-foreign", "prune-late-source", "prune-late-owner", "prune-during-owner", "prune-during-source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			raw := strings.Replace(rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`), "FOC12345678", "OLD-SERIAL", 1)
			targets := []Device{{ID: "old-id", Hostname: "edge", Serial: "OLD-SERIAL", Reachability: "Unreachable"}}
			if mode == "two-devices" || strings.HasPrefix(mode, "prune-multi") {
				targets = append(targets, Device{ID: "other-id", Hostname: "other", Serial: "OTHER-SERIAL", Reachability: "Unreachable"})
				var root map[string]any
				if err := json.Unmarshal([]byte(raw), &root); err != nil {
					t.Fatal(err)
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				rows := inventory["devices"].([]any)
				inventory["devices"] = append(rows, map[string]any{"name": "other", "serial_number": "OTHER-SERIAL", "site": "Global/Lab/Building", "state": "PROVISION", "rma": map[string]any{"action": rmaMark}})
				encoded, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(encoded)
			}
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(raw)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			records := map[string]deviceReplacementRecord{}
			writes := []string{}
			unmarks := 0
			duringUnmark := false
			changedDuringUnmark := false
			started := map[string]time.Time{}
			markCounts := map[string]int{}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == deviceReplacementPath {
					var payload []struct {
						ID     string `json:"faultyDeviceId"`
						Status string `json:"replacementStatus"`
					}
					if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || payload[0].Status != "MARKED-FOR-REPLACEMENT" {
						t.Error("invalid mark payload")
						w.WriteHeader(400)
						return
					}
					id := payload[0].ID
					serial := ""
					for _, target := range targets {
						if target.ID == id {
							serial = target.Serial
						}
					}
					if serial == "" || records[id].ID != "" {
						t.Error("unknown or repeated target", id)
						w.WriteHeader(409)
						return
					}
					writes = append(writes, id)
					at := time.Now().UTC().Truncate(time.Second)
					markCounts[id]++
					suffix := ""
					if markCounts[id] > 1 {
						suffix = fmt.Sprintf("-%d", markCounts[id])
					}
					taskID := "mark-" + id + suffix
					started[taskID] = at
					records[id] = deviceReplacementRecord{ID: "record-" + id + suffix, FaultyID: id, FaultySerial: serial, Status: "MARKED-FOR-REPLACEMENT", CreatedAt: at.UnixMilli()}
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprintf(w, `{"response":{"taskId":%q}}`, taskID)
					return
				}
				if r.Method == "PUT" && r.URL.Path == deviceReplacementPath && strings.HasPrefix(mode, "prune-") {
					var payload []struct {
						ID     string `json:"id"`
						Status string `json:"replacementStatus"`
					}
					if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || payload[0].Status != "NON-FAULTY" {
						t.Error("wrong owned cleanup target")
						w.WriteHeader(400)
						return
					}
					id := ""
					for key, record := range records {
						if record.ID == payload[0].ID {
							id = key
						}
					}
					if id == "" || records[id].Status != "MARKED-FOR-REPLACEMENT" {
						t.Error("unknown or repeated cleanup target")
						w.WriteHeader(409)
						return
					}
					unmarks++
					taskID := "unmark-task"
					if strings.HasPrefix(mode, "prune-multi") {
						taskID = "unmark-" + id
					}
					started[taskID] = time.Now().UTC().Truncate(time.Second)
					record := records[id]
					record.Status = "NON-FAULTY"
					records[id] = record
					if mode == "prune-admitted-lost" || mode == "prune-multi-lost" && id == "other-id" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprintf(w, `{"response":{"taskId":%q}}`, taskID)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected native mutation", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				var rows any
				if strings.HasPrefix(r.URL.Path, taskPath) {
					id := strings.TrimPrefix(r.URL.Path, taskPath)
					if id == "unmark-task" {
						if mode == "prune-recovery-source" {
							changed := config.DeepCopy()
							changed.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
							if err := a.statusWriter.Update(ctx, changed); err != nil {
								t.Error(err)
							}
						}
						if mode == "prune-recovery-owner" {
							changed, err := a.currentController(ctx)
							if err != nil {
								t.Error(err)
								return
							}
							changed.Status.Configuration.Owners[0].ConfigUID = "foreign"
							if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
								t.Error(err)
							}
						}
					}
					rows = map[string]any{"id": id, "startTime": started[id].UnixMilli(), "endTime": time.Now().UnixMilli(), "isError": mode == "failed-task" || mode == "prune-multi-failed" && id == "unmark-other-id", "errorCode": "FAILED"}
				} else {
					switch r.URL.Path {
					case devicesPath:
						if duringUnmark && !changedDuringUnmark {
							changedDuringUnmark = true
							if mode == "prune-during-owner" {
								changed, err := a.currentController(ctx)
								if err != nil {
									t.Error(err)
									return
								}
								changed.Status.Configuration.Owners[0].ConfigUID = "foreign"
								if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
									t.Error(err)
								}
							}
							if mode == "prune-during-source" {
								changed := config.DeepCopy()
								changed.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
								if err := a.statusWriter.Update(ctx, changed); err != nil {
									t.Error(err)
								}
							}
						}
						rows = targets
					case sitesPath:
						rows = sampleSites()
					case assignedDevicesPath:
						members := []map[string]string{}
						for _, target := range targets {
							members = append(members, map[string]string{"deviceId": target.ID, "siteId": "building"})
						}
						rows = members
					case wiredProvisionPath:
						id := r.URL.Query().Get("networkDeviceId")
						rows = []wiredProvisionedDevice{{ID: "provision-" + id, SiteID: "building", DeviceID: id}}
					case deviceReplacementPath:
						list := []deviceReplacementRecord{}
						for _, target := range targets {
							if record, ok := records[target.ID]; ok {
								list = append(list, record)
							}
						}
						rows = list
					default:
						t.Error("unexpected endpoint", r.URL.Path)
						w.WriteHeader(404)
						return
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			evaluate := func() string {
				t.Helper()
				restarted := *a
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, handled, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, &configapi.NetworkControllerConfigStatus{})
				if !handled {
					t.Fatal("public RMA manifest rejected", reason)
				}
				return reason
			}
			for _, target := range targets {
				reason := evaluate()
				state := journalState(t, a)
				if state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.TargetID != target.ID {
					t.Fatal("incorrect preparation", reason, state.Operation)
				}
				op := state.Operation.DeepCopy()
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := a.compileAdmittedInventoryApply(config, source)
				if err != nil {
					t.Fatal(err)
				}
				write, err := plan.executor.next(ctx)
				if err != nil || write == nil || write.resource != op.Resource {
					t.Fatal("Prepared recovery mismatch", err)
				}
				row, _ := settledConfigurationFixture(t, a)
				row.Status.ManagerSettledAt = nil
				row.Status.Grant.Phase = "Held"
				row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: row.Status.Grant.GrantedAt}
				if err := a.statusWriter.Status().Update(ctx, row); err != nil {
					t.Fatal(err)
				}
				err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "inventory", write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
					task, err := write.submit(ctx)
					return configurationReceipt{task: task}, err
				}, journalTestAdmission, write.identity)
				if mode == "lost-receipt" {
					if err == nil || journalState(t, a).Operation.Phase != "Unknown" {
						t.Fatal("lost receipt not fenced", err)
					}
					for range 3 {
						evaluate()
					}
					if len(writes) != 1 || journalState(t, a).Operation.Phase != "Unknown" {
						t.Fatal("ambiguous mark replayed")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				reason = evaluate()
				if mode == "failed-task" {
					if reason != "ControllerTaskFailed" || journalState(t, a).Operation.Phase != "Accepted" || len(writes) != 1 {
						t.Fatal("failed task advanced", reason)
					}
					return
				}
				if journalState(t, a).Operation.Phase != "Verifying" {
					t.Fatal("mark not independently verified", reason)
				}
				reason = evaluate()
				state = journalState(t, a)
				if state.Operation.Phase != "Verified" {
					t.Fatal("atomic completion failed", reason)
				}
				found := false
				for _, receipt := range state.ActionApplications {
					if receipt.Operation.ID == op.ID {
						found = receipt.Resource == "rma-mark-record:record-"+target.ID && receipt.Operation.Resource == op.Resource
					}
				}
				if !found {
					t.Fatal("native UUID lost through public reconciler")
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), row); err != nil {
					t.Fatal(err)
				}
				row.Status.Completion, err = contract.CompletionForReceipt(row, nc, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				row.Status.Grant.Phase = "Released"
				row.Status.ManagerSettledAt = &metav1.Time{Time: time.Now()}
				if err := a.statusWriter.Status().Update(ctx, row); err != nil {
					t.Fatal(err)
				}
				evaluate()
				if journalState(t, a).Operation != nil {
					t.Fatal("settled operation retained")
				}
			}
			if strings.HasPrefix(mode, "prune-") {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				var root map[string]any
				if err := json.Unmarshal([]byte(raw), &root); err != nil {
					t.Fatal(err)
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				delete(inventory["devices"].([]any)[0].(map[string]any), "rma")
				if strings.HasPrefix(mode, "prune-multi") && mode != "prune-multi-mixed-retain" {
					for _, device := range inventory["devices"].([]any) {
						delete(device.(map[string]any), "rma")
					}
				}
				if mode == "prune-multi-mixed-add" {
					targets = append(targets, Device{ID: "fresh-id", Hostname: "fresh", Serial: "FRESH-SERIAL", Reachability: "Unreachable"})
					inventory["devices"] = append(inventory["devices"].([]any), map[string]any{"name": "fresh", "serial_number": "FRESH-SERIAL", "site": "Global/Lab/Building", "state": "PROVISION", "rma": map[string]any{"action": rmaMark}})
				}
				encoded, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = encoded
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
				if mode == "prune-retain" {
					config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
				}
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(mode, "prune-multi") {
					qualifyRMAMultiCleanup(t, a, config, source, targets, records, evaluate, func() int { return unmarks }, mode)
					return
				}
				if mode == "prune-foreign" {
					record := records["old-id"]
					record.ID = "foreign"
					records["old-id"] = record
				}
				write, err := a.planOwnedDeviceReplacementUnmark(ctx, config, source, "edge")
				if mode == "prune-retain" || mode == "prune-foreign" {
					if err == nil || write != nil || unmarks != 0 {
						t.Fatal("retained or unowned mark allowed cleanup", err)
					}
					return
				}
				if err != nil || write == nil || write.action != "Delete" {
					t.Fatal("owned mark removal not planned", err)
				}
				if strings.HasPrefix(mode, "prune-admitted") {
					executor := &deviceReplacementUnmarkExecutor{adapter: a, config: config, source: source, name: "edge"}
					reason := evaluate()
					if journalState(t, a).Operation == nil || journalState(t, a).Operation.Phase != "Prepared" {
						t.Fatal("public cleanup not prepared", reason)
					}
					op := journalState(t, a).Operation.DeepCopy()
					for _, invalid := range []string{"Accepted", "Verifying", "Verified", "Unknown", "task", "scope", "serial", "origin"} {
						nc, err := a.currentController(ctx)
						if err != nil {
							t.Fatal(err)
						}
						saved := nc.Status.Configuration.DeepCopy()
						switch invalid {
						case "Accepted", "Verifying", "Verified", "Unknown":
							nc.Status.Configuration.Operation.Phase = invalid
						case "task":
							nc.Status.Configuration.Operation.TaskID = "existing-task"
						case "scope":
							nc.Status.Configuration.Operation.Resource = "rma-unmark:" + strings.Repeat("f", 64) + ":record-old-id:1"
						case "serial":
							nc.Status.Configuration.Operation.TargetSerial = "foreign"
						case "origin":
							nc.Status.Configuration.ActionApplications = nil
						}
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						if next, err := executor.next(ctx); err == nil || next != nil || unmarks != 0 {
							t.Fatal("invalid cleanup resumed", invalid, err)
						}
						nc, err = a.currentController(ctx)
						if err != nil {
							t.Fatal(err)
						}
						nc.Status.Configuration = saved
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
					}
					// An exact Prepared journal can reconstruct its guarded PUT.
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					publicPlan, err := a.compileOwnedInventoryApply(current, config, source)
					if err != nil {
						t.Fatal(err)
					}
					resumed, err := publicPlan.executor.next(ctx)
					if err != nil || resumed == nil || resumed.resource != op.Resource {
						t.Fatal("cleanup Prepared recovery", err)
					}
					row, _ := settledConfigurationFixture(t, a)
					row.Status.ManagerSettledAt = nil
					row.Status.Grant.Phase = "Held"
					row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: row.Status.Grant.GrantedAt}
					if err := a.statusWriter.Status().Update(ctx, row); err != nil {
						t.Fatal(err)
					}
					err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "inventory", resumed.resource, resumed.action, func(ctx context.Context) (configurationReceipt, error) {
						task, err := resumed.submit(ctx)
						return configurationReceipt{task: task}, err
					}, journalTestAdmission, resumed.identity)
					if mode == "prune-admitted-lost" {
						if err == nil || journalState(t, a).Operation.Phase != "Unknown" {
							t.Fatal("ambiguous cleanup not fenced", err)
						}
						if next, err := executor.next(ctx); err == nil || next != nil || unmarks != 1 {
							t.Fatal("ambiguous cleanup replayed")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					op = journalState(t, a).Operation.DeepCopy()
					if next, err := executor.next(ctx); err == nil || next != nil {
						t.Fatal("accepted cleanup reissued")
					}
					done, err := executor.observeCompletion(ctx, op, time.Now())
					if err != nil || !done {
						t.Fatal("cleanup observation", err)
					}
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					reason = evaluate()
					if journalState(t, a).Operation.Phase != "Verifying" {
						t.Fatal("public cleanup observation failed", reason)
					}
					op = journalState(t, a).Operation.DeepCopy()
					restarted := *a
					executor.adapter = &restarted
					done, err = executor.verify(ctx, op)
					if err != nil || !done {
						t.Fatal("cleanup verification", err)
					}
					reason = evaluate()
					if journalState(t, a).Operation.Phase != "Verified" {
						t.Fatal("public cleanup completion failed", reason)
					}
					if err := a.finishConfiguration(ctx, op.ID); err == nil {
						t.Fatal("cleanup released before worker settlement")
					}
					nc, err = a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					row.Status.Completion, err = contract.CompletionForReceipt(row, nc, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					row.Status.Grant.Phase = "Released"
					row.Status.ManagerSettledAt = &metav1.Time{Time: time.Now()}
					if err := a.statusWriter.Status().Update(ctx, row); err != nil {
						t.Fatal(err)
					}
					if err := a.finishConfiguration(ctx, op.ID); err != nil {
						t.Fatal(err)
					}
					state := journalState(t, a)
					if state.Operation != nil || len(state.ActionApplications) != 2 || unmarks != 1 || len(writes) != 1 {
						t.Fatal("incorrect cleanup settlement")
					}
					for range 2 {
						if reason := evaluate(); reason != "ApplyFinalizationPending" {
							t.Fatal("retained reservation did not defer finalization", reason)
						}
						if len(journalState(t, a).ActionApplications) != 2 {
							t.Fatal("receipt retired before manager")
						}
						if journalState(t, a).Operation != nil || unmarks != 1 {
							t.Fatal("public cleanup reconverged with mutation")
						}
						restarted := *a
						probe := &deviceReplacementUnmarkExecutor{adapter: &restarted, config: config, source: source, name: "edge"}
						if next, err := probe.next(ctx); err != nil || next != nil {
							t.Fatal("completed cleanup replayed before retirement", err)
						}
					}
					if err := a.retireDeviceReplacementMarkReceipt(ctx, config, source, "edge", op.ID); err == nil {
						t.Fatal("retained reservations lost receipts")
					}
					// Emulate the admitted manager's existing retirement routine.
					var reservations ops.ControllerConfigurationReservationList
					if err := a.statusWriter.List(ctx, &reservations, ctrlclient.InNamespace(config.Namespace)); err != nil {
						t.Fatal(err)
					}
					for i := range reservations.Items {
						if mode == "prune-admitted-finalizer" && reservations.Items[i].Spec.OperationID == op.ID {
							reservations.Items[i].Finalizers = append(reservations.Items[i].Finalizers, "test.cisco.vk/hold")
							if err := a.statusWriter.Update(ctx, &reservations.Items[i]); err != nil {
								t.Fatal(err)
							}
						}
						if err := contract.RetireSettledReservation(ctx, a.statusReader, a.statusWriter, &reservations.Items[i], time.Now()); err != nil {
							t.Fatal("manager retirement", err)
						}
					}
					if mode == "prune-admitted-finalizer" {
						if reason := evaluate(); reason != "ApplyFinalizationPending" || len(journalState(t, a).ActionApplications) != 2 {
							t.Fatal("acknowledged Delete retired ownership before object disappearance", reason)
						}
						var held ops.ControllerConfigurationReservation
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), &held); err != nil {
							t.Fatal(err)
						}
						held.Finalizers = nil
						if err := a.statusWriter.Update(ctx, &held); err != nil {
							t.Fatal(err)
						}
					}
					original := records["old-id"]
					changed := original
					changed.ID = "foreign-record"
					records["old-id"] = changed
					if err := a.retireDeviceReplacementMarkReceipt(ctx, config, source, "edge", op.ID); err == nil {
						t.Fatal("foreign native state authorized retirement")
					}
					records["old-id"] = original
					saved, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					savedState := saved.Status.Configuration.DeepCopy()
					saved.Status.Configuration.ActionApplications = saved.Status.Configuration.ActionApplications[:1]
					if err := a.statusWriter.Status().Update(ctx, saved); err != nil {
						t.Fatal(err)
					}
					delete(records, "old-id")
					if err := a.retireDeviceReplacementMarkReceipt(ctx, config, source, "edge", op.ID); err == nil {
						t.Fatal("absence alone authorized retirement")
					}
					saved, err = a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					saved.Status.Configuration = savedState
					if err := a.statusWriter.Status().Update(ctx, saved); err != nil {
						t.Fatal(err)
					}
					records["old-id"] = original
					if reason := evaluate(); reason != "ApplyVerified" {
						t.Fatal("automatic owned mark retirement", reason)
					}
					state = journalState(t, a)
					if len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID != op.ID {
						t.Fatal("cleanup tombstone was not preserved")
					}
					if err := a.retireDeviceReplacementMarkReceipt(ctx, config, source, "edge", op.ID); err != nil {
						t.Fatal("retirement not idempotent", err)
					}
					for _, native := range []string{"nonfaulty", "absent", "foreign", "remarked"} {
						records["old-id"] = original
						switch native {
						case "absent":
							delete(records, "old-id")
						case "foreign":
							other := original
							other.ID = "other"
							records["old-id"] = other
						case "remarked":
							other := original
							other.Status = "MARKED-FOR-REPLACEMENT"
							records["old-id"] = other
						}
						next, err := executor.next(ctx)
						valid := native == "nonfaulty" || native == "absent"
						if (err == nil) != valid || next != nil {
							t.Fatal("incorrect cleanup tombstone convergence", native, err)
						}
					}
					records["old-id"] = original
					if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
						t.Fatal(err)
					}
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
						t.Fatal(err)
					}
					if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
						t.Fatal("public cleanup did not publish InSync", config.Status.Phase)
					}
					if mode == "prune-admitted-reapply" {
						qualifyRMAReapply(t, a, config, raw, records, evaluate, func() (int, int) { return len(writes), unmarks })
						return
					}
					invalid, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					invalid.Status.Configuration.ActionApplications[0].Operation.Section = "sites"
					if err := a.statusWriter.Status().Update(ctx, invalid); err != nil {
						t.Fatal(err)
					}
					if err := a.retireDeviceReplacementMarkReceipt(ctx, config, source, "edge", op.ID); err == nil {
						t.Fatal("invalid cleanup tombstone accepted")
					}
					if unmarks != 1 || len(writes) != 1 {
						t.Fatal("retirement mutated device")
					}
					return
				}
				if mode == "prune-late-source" {
					changed := config.DeepCopy()
					changed.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
					if err := a.statusWriter.Update(ctx, changed); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "prune-late-owner" {
					changed, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					changed.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
						t.Fatal(err)
					}
				}
				duringUnmark = true
				task, err := write.submit(ctx)
				if mode != "prune-owned" && mode != "prune-absent" && mode != "prune-missing-origin" && mode != "prune-replaced-origin" && mode != "prune-recovery-source" && mode != "prune-recovery-owner" {
					if err == nil || unmarks != 0 {
						t.Fatal("late policy/owner change allowed cleanup", err)
					}
					return
				}
				if err != nil || task.ID != "unmark-task" || unmarks != 1 || len(writes) != 1 {
					t.Fatal("incorrect cleanup effect", err)
				}
				// The old mark receipt must survive native cleanup.
				if len(journalState(t, a).ActionApplications) != 1 {
					t.Fatal("mark ownership retired before cleanup settlement")
				}
				// Recovery is exercised through an explicitly prepared cleanup
				// journal. This fixture still does not exercise cleanup admission.
				if _, err := a.prepareConfiguration(ctx, config, source, "inventory", write.resource, write.action, write.identity); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation.Phase = "Verifying"
				nc.Status.Configuration.Operation.TaskID = task.ID
				// Keep ordering exact; Kubernetes metav1.Time persists seconds.
				nc.Status.Configuration.Operation.SubmittedAt = &metav1.Time{Time: started[task.ID]}
				if mode == "prune-missing-origin" {
					nc.Status.Configuration.ActionApplications = nil
				}
				if mode == "prune-replaced-origin" {
					nc.Status.Configuration.ActionApplications[0].Resource = "rma-mark-record:foreign"
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if mode == "prune-absent" {
					delete(records, "old-id")
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				op := nc.Status.Configuration.Operation.DeepCopy()
				restarted := *a
				plan := configurationApplyPlan{section: "inventory", recordCompletion: func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
					return restarted.recordOwnedDeviceReplacementUnmark(ctx, config, source, "edge", op.ID, time.Now())
				}}
				err = plan.completeWithReceipt(ctx, &restarted, op)
				success := mode == "prune-owned" || mode == "prune-absent"
				if (err == nil) != success {
					t.Fatal("unexpected owned cleanup recovery", err)
				}
				state := journalState(t, a)
				if success {
					if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 2 || state.ActionApplications[0].Resource != "rma-mark-record:record-old-id" {
						t.Fatal("cleanup lost original ownership")
					}
				} else if state.Operation.Phase != "Verifying" {
					t.Fatal("invalid cleanup completed")
				}
				if unmarks != 1 || len(writes) != 1 {
					t.Fatal("recovery replayed mutation")
				}
				return
			}
			if mode == "changed-record" {
				record := records["old-id"]
				record.ID = "foreign-record"
				records["old-id"] = record
				if reason := evaluate(); reason != "ObservationOrPlanFailed" {
					t.Fatal("recreated mark adopted", reason)
				}
				if len(writes) != 1 {
					t.Fatal("recreated mark replayed")
				}
				return
			}
			for range 3 {
				evaluate()
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(writes) != len(targets) || len(journalState(t, a).ActionApplications) != len(targets) {
				t.Fatal("cycle did not converge without replay", config.Status.Phase, writes)
			}
		})
	}
}
