// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Stateful native/compiler integration, not a Kubernetes actor/Lease test.
// Reservation status is supplied explicitly; no lab execution is implied.
func TestWirelessInventoryCompiledCycle(t *testing.T) {
	for _, scenario := range []string{"unchanged", "prepared replacement", "verifying replacement"} {
		t.Run(scenario, func(t *testing.T) {
			replace := scenario == "prepared replacement"
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			raw := []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"WLC","serial_number":"serial","site":"Global/Lab","state":"REPROVISION","type":"WirelessController","primary_managed_ap_locations":["Global/Lab"],"secondary_managed_ap_locations":[]}]}}}`)
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source := &configSourceSnapshot{raw: raw}
			assigned, located, provisioned := false, false, false
			taskDone := false
			submitted := metav1.NewTime(time.Now().UTC().Add(-time.Second).Truncate(time.Second))
			ssidID := "ssid-id"
			stamp := strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)
			writes := []string{}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case taskPath + "provision-task":
					end := int64(0)
					if taskDone {
						end = time.Now().UnixMilli()
					}
					fmt.Fprintf(w, `{"response":{"id":"provision-task","isError":false,"startTime":%d,"endTime":%d}}`, submitted.UnixMilli(), end)
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case devicesPath:
					fmt.Fprint(w, `{"response":[{"id":"wlc","hostname":"WLC","serialNumber":"serial","managementIpAddress":"198.51.100.10","family":"Wireless Controller","reachabilityStatus":"Reachable"}]}`)
				case sitesPath:
					fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"},{"id":"lab","name":"Lab","nameHierarchy":"Global/Lab","parentId":"global","type":"area"}]}`)
				case wirelessProfilesPath:
					fmt.Fprint(w, `{"response":[{"id":"profile","wirelessProfileName":"P","ssidDetails":[{"ssidName":"S","enableFabric":false}]}]}`)
				case profileSiteAssignmentsPath("profile"):
					fmt.Fprint(w, `{"response":[{"id":"lab"}]}`)
				case wirelessSSIDPath("global"):
					fmt.Fprintf(w, `{"response":[{"id":%q,"ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`, ssidID)
				case assignedDevicesPath:
					if assigned {
						fmt.Fprint(w, `{"response":[{"deviceId":"wlc","siteId":"lab"}]}`)
					} else {
						fmt.Fprint(w, `{"response":[]}`)
					}
				case assignDevicesPath:
					writes = append(writes, "site")
					fmt.Fprint(w, `{"response":{"taskId":"site-task"}}`)
				case "/dna/intent/api/v1/wirelessControllers/wlc/managedApLocations/count":
					count := 0
					if located {
						count = 1
					}
					fmt.Fprintf(w, `{"response":{"primaryManagedApLocationsCount":%d,"secondaryManagedApLocationsCount":0,"anchorManagedApLocationsCount":0}}`, count)
				case "/dna/intent/api/v1/wirelessControllers/wlc/primaryManagedApLocations", "/dna/intent/api/v1/wirelessControllers/wlc/secondaryManagedApLocations":
					rows := []wirelessManagedAPLocation{}
					if located && strings.HasSuffix(r.URL.Path, "/primaryManagedApLocations") {
						rows = append(rows, wirelessManagedAPLocation{SiteID: "lab", SitePath: "Global/Lab"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"managedApLocations": rows}}})
				case "/dna/intent/api/v1/wirelessControllers/wlc/assignManagedApLocations":
					writes = append(writes, "locations")
					fmt.Fprint(w, `{"response":{"taskId":"locations-task"}}`)
				case "/dna/intent/api/v1/wirelessControllers/wlc/provisionStatus":
					model := "NOT_STARTED"
					if provisioned {
						model = "SUCCESS"
					}
					fmt.Fprintf(w, `{"response":{"networkDeviceId":"wlc","deviceName":"WLC","siteId":"lab","networkIntentProvisionStatus":"SUCCESS","modelConfigProvisionStatus":%q,"outOfSync":false,"lastProvisionedTimeStamp":%q}}`, model, stamp)
				case "/dna/intent/api/v1/wirelessControllers/wlc/provision":
					writes = append(writes, "provision")
					fmt.Fprint(w, `{"response":{"taskId":"provision-task"}}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			a.workerPodName, a.workerPodUID = "connector", "pod-uid"
			if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			plan, err := a.compileWirelessInventoryApply(config, source)
			if err != nil {
				t.Fatal(err)
			}
			for _, stage := range []string{"site", "locations"} {
				write, err := plan.executor.next(ctx)
				if err != nil || write == nil {
					t.Fatal(stage, "plan", err)
				}
				if _, err := write.submit(ctx); err != nil {
					t.Fatal(stage, "submit", err)
				}
				op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, TargetSerial: write.identity.serial, ParentID: write.identity.parent}
				if done, err := plan.executor.verify(ctx, op); err != nil || done {
					t.Fatal("acceptance mistaken for readback", stage, err)
				}
				if stage == "site" {
					assigned = true
				} else {
					located = true
				}

				if done, err := plan.executor.verify(ctx, op); err != nil || !done {
					t.Fatal(stage, "readback", err)
				}
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil {
				t.Fatal("provision plan", err)
			}
			record, err := decodeWirelessProvisionRecord(write.resource)
			if err != nil || record.DependencyDigest == "" || !record.ModelRequired {
				t.Fatal("dependency binding missing", err)
			}
			if _, err := a.prepareConfiguration(ctx, config, source, "inventory", write.resource, write.action, write.identity); err != nil {
				t.Fatal(err)
			}
			if _, err := plan.executor.next(ctx); err == nil {
				t.Fatal("missing reservation passed")
			}
			row, _ := settledConfigurationFixture(t, a)
			row.Status.ManagerSettledAt = nil
			row.Status.Grant.Phase = "Held"
			// Bind fixture chronology to the simulated submission, not a later
			// wall-clock second reached while the native preflight is running.
			row.Status.Grant.GrantedAt = metav1.NewTime(submitted.Add(-time.Second))
			row.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
			desiredStatus := row.DeepCopy().Status
			if err := a.statusWriter.Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			row.Status = desiredStatus
			if err := a.statusWriter.Status().Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			if replace {
				ssidID = "replacement-ssid"
			}
			restarted := *a
			plan, err = restarted.compileWirelessInventoryApply(config, source)
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := plan.executor.next(ctx)
			if replace {
				if err == nil || len(writes) != 2 {
					t.Fatal("dependency replacement resumed old operation", err)
				}
				return
			}
			if err != nil || resumed == nil || resumed.resource != write.resource {
				t.Fatal("Prepared restart failed", err)
			}
			if _, err := resumed.submit(ctx); err != nil {
				t.Fatal("native provisioning", err)
			}
			if strings.Join(writes, ",") != "site,locations,provision" {
				t.Fatal("wrong write order", writes)
			}
			nc, err := restarted.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation
			op.Phase, op.TaskID, op.SubmittedAt = "Verifying", "provision-task", &submitted
			if err := restarted.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if done, err := plan.executor.verify(ctx, op); err != nil || done {
				t.Fatal("pending task considered verified", err)
			}
			taskDone = true
			if done, err := plan.executor.verify(ctx, op); err != nil || done {
				t.Fatal("successful task substituted for model convergence", err)
			}
			provisioned = true
			stamp = strconv.FormatInt(time.Now().UnixMilli(), 10)
			if err := restarted.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), row); err != nil {
				t.Fatal(err)
			}
			row.Spec.ControllerWorkerPod.Name, row.Spec.ControllerWorkerPod.UID = restarted.workerPodName, restarted.workerPodUID
			if err := restarted.statusWriter.Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: row.Spec.OperationID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: submitted}
			if err := restarted.statusWriter.Status().Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			if scenario == "verifying replacement" {
				ssidID = "replacement-after-submit"
				if done, err := plan.executor.verify(ctx, op); err == nil || done {
					t.Fatal("changed dependencies verified after submission")
				}
				if err := restarted.recordWirelessProvisionApplication(ctx, config, source, op.ID, "WLC"); err == nil {
					t.Fatal("changed dependencies received a durable receipt")
				}
				state := journalState(t, &restarted)
				if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
					t.Fatal("failed verification released operation evidence")
				}
				status := &configapi.NetworkControllerConfigStatus{}
				_, handled, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, status)
				if !handled || reason != "VerificationPending" {
					t.Fatal("manifest path did not retain changed dependencies", reason)
				}
				ssidID = "ssid-id"
			}
			if done, err := plan.executor.verify(ctx, op); err != nil || !done {
				t.Fatal("converged provisioning not verified", err)
			}
			status := &configapi.NetworkControllerConfigStatus{}
			_, handled, reason, _ := (&configReconciler{a: &restarted}).evaluateConfigurationApply(ctx, nc, config, status)
			if !handled || reason != "ApplyProgressing" {
				t.Fatal("manifest verification did not retain receipt", reason)
			}
			nc, err = restarted.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(row), row); err != nil {
				t.Fatal(err)
			}
			row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: row.Spec.OperationID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: submitted}
			row.Status.Completion, err = contract.CompletionForReceipt(row, nc, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			row.Status.Grant.Phase = "Released"
			row.Status.ManagerSettledAt = &metav1.Time{Time: time.Now()}
			if err := restarted.statusWriter.Status().Update(ctx, row); err != nil {
				t.Fatal(err)
			}
			if err := restarted.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal("settled journal release", err)
			}
			plan, err = restarted.compileWirelessInventoryApply(config, source)
			if err != nil {
				t.Fatal(err)
			}
			if next, err := plan.executor.next(ctx); err != nil || next != nil {
				t.Fatal("verified revision replayed", err)
			}
			if len(writes) != 3 {
				t.Fatal("duplicate native submission", writes)
			}
		})
	}
}
