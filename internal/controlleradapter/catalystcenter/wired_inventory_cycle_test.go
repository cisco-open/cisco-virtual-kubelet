// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

// Stateful manifest/compiler/native/journal qualification. Worker/manager
// reservation evidence is supplied by fixtures; this is not a real Lease test.
func TestWiredInventoryManifestCycle(t *testing.T) {
	for _, mode := range []string{"provision", "reprovision", "lost-receipt", "failed-task"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			raw := wiredInventoryModel()
			if mode == "reprovision" {
				raw = strings.Replace(raw, `"PROVISION"`, `"REPROVISION"`, 1)
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
			assigned := false
			provisioned := mode == "reprovision"
			writes := []string{}
			submitted := time.Now()
			taskID := ""
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if strings.HasPrefix(r.URL.Path, taskPath) {
					id := strings.TrimPrefix(r.URL.Path, taskPath)
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "isError": mode == "failed-task" && id == "provision-task", "errorCode": "FAILED", "startTime": submitted.UnixMilli(), "endTime": time.Now().UnixMilli()}})
					return
				}
				if r.URL.Path == assignDevicesPath && r.Method == "POST" {
					assigned = true
					writes = append(writes, "assign")
					taskID = "assign-task"
					submitted = time.Now()
					fmt.Fprint(w, `{"response":{"taskId":"assign-task"}}`)
					return
				}
				if r.URL.Path == wiredProvisionPath && r.Method != "GET" {
					if !assigned {
						t.Error("provisioning before assignment")
					}
					method := "POST"
					if mode == "reprovision" {
						method = "PUT"
					}
					if r.Method != method {
						t.Error("wrong provision method", r.Method)
					}
					provisioned = true
					writes = append(writes, "provision")
					taskID = "provision-task"
					submitted = time.Now()
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"provision-task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}}
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					values := []map[string]string{}
					if assigned {
						values = append(values, map[string]string{"deviceId": "edge-id", "siteId": "building"})
					}
					rows = values
				case wiredProvisionPath:
					values := []wiredProvisionedDevice{}
					if provisioned {
						values = append(values, wiredProvisionedDevice{ID: "provisioned", SiteID: "building", DeviceID: "edge-id"})
					}
					rows = values
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
					return
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
					t.Fatal("public manifest router rejected intent", reason)
				}
				return reason
			}
			for _, step := range []string{"assign", "provision"} {
				evaluate()
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Prepared" {
					t.Fatal("no prepared operation", nc.Status.Configuration)
				}
				op := nc.Status.Configuration.Operation.DeepCopy()
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
					t.Fatal("prepared restart mismatch", write, err)
				}
				row, _ := settledConfigurationFixture(t, a)
				row.Status.ManagerSettledAt = nil
				row.Status.Grant.Phase = "Held"
				row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: row.Status.Grant.GrantedAt}
				if err := a.statusWriter.Status().Update(ctx, row); err != nil {
					t.Fatal(err)
				}
				// The common dispatch implementation is used with its test-only permit;
				// native calls are never made by the planner or recovery observation.
				err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "inventory", write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
					task, err := write.submit(ctx)
					return configurationReceipt{task: task}, err
				}, journalTestAdmission, write.identity)
				if step == "provision" && mode == "lost-receipt" {
					if err == nil || journalState(t, a).Operation.Phase != "Unknown" {
						t.Fatal("ambiguous receipt not retained", err)
					}
					evaluate()
					if len(writes) != 2 {
						t.Fatal("ambiguous submission replayed", writes)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if reason := evaluate(); step == "provision" && mode == "failed-task" {
					if reason != "ControllerTaskFailed" || journalState(t, a).Operation.Phase != "Accepted" {
						t.Fatal(reason)
					}
					return
				}
				if journalState(t, a).Operation.Phase != "Verifying" {
					t.Fatal("task did not reach verification", taskID)
				}
				if reason := evaluate(); journalState(t, a).Operation.Phase != "Verified" {
					t.Fatal("manifest verification failed", reason)
				}
				nc, err = a.currentController(ctx)
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
					t.Fatal("settled journal retained")
				}
			}
			if strings.Join(writes, ",") != "assign,provision" {
				t.Fatal(writes)
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
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("public status did not converge", config.Status)
			}
			if len(writes) != 2 || journalState(t, a).Operation != nil || len(journalState(t, a).ActionApplications) != 2 {
				t.Fatal("revision replay or lost receipts", writes)
			}
		})
	}
}
