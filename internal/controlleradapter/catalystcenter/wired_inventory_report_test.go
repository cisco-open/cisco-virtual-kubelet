// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestWiredInventoryReport(t *testing.T) {
	for _, mode := range []string{"current", "missing", "unassigned", "wrong-site", "reprovision", "reprovision-missing", "replacement", "invalid-inventory"} {
		t.Run(mode, func(t *testing.T) {
			raw := wiredInventoryModel()
			if strings.HasPrefix(mode, "reprovision") {
				raw = strings.Replace(raw, `"PROVISION"`, `"REPROVISION"`, 1)
			}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Errorf("Report mutated %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					reads++
					id := "edge-id"
					if mode == "replacement" && reads > 2 {
						id = "replacement"
					}
					rows = []Device{{ID: id, Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}}
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					values := []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
					if mode == "unassigned" {
						values = []map[string]string{}
					}
					rows = values
				case wiredProvisionPath:
					values := []wiredProvisionedDevice{{ID: "provisioned", DeviceID: "edge-id", SiteID: "building"}}
					switch mode {
					case "missing", "unassigned", "reprovision-missing":
						values = []wiredProvisionedDevice{}
					case "wrong-site":
						values[0].SiteID = "area"
					case "invalid-inventory":
						values[0].DeviceID = "foreign"
					}
					rows = values
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			plans, err := c.compileConfigReports([]byte(raw), []string{"inventory"})
			if err != nil || len(plans) != 1 || plans[0].count != 2 {
				t.Fatal(plans, err)
			}
			if plans[0].note == "" {
				t.Fatal("imperative Report caveat missing")
			}
			drift, err := plans[0].observe(context.Background())
			if mode == "replacement" || mode == "invalid-inventory" {
				if err == nil || drift != nil {
					t.Fatal("partial/stale report accepted", drift, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			count := 1
			if mode == "current" {
				count = 0
			}
			if mode == "unassigned" {
				count = 2
			}
			if len(drift) != count {
				t.Fatal(drift)
			}
			if count > 0 {
				last := drift[len(drift)-1]
				path := "/provisioning"
				change := configapi.NetworkControllerDriftChangeAdd
				if mode == "wrong-site" {
					path = "/provisioning/site"
					change = configapi.NetworkControllerDriftChangeUpdate
				}
				if mode == "reprovision" {
					path = "/reprovision"
					change = configapi.NetworkControllerDriftChangeUpdate
				}
				if last.Path != path || last.Change != change {
					t.Fatal(last)
				}
			}
		})
	}
}

func TestWiredInventoryReportReconciler(t *testing.T) {
	ctx := context.Background()
	e, _ := newWiredExecutorFixture(t)
	config := e.config
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	config.Spec.Source.Inline.Raw = []byte(wiredInventoryModel())
	if err := e.adapter.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := (&configReconciler{a: e.adapter}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
		t.Fatal(err)
	}
	if err := e.adapter.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 2 {
		t.Fatal(config.Status)
	}
	nc, err := e.adapter.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration != nil && (nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.Owners) != 0 || len(nc.Status.Configuration.ActionApplications) != 0) {
		t.Fatal("Report fabricated execution authority")
	}
}

func TestWiredInventoryReportKeepsDiscoveryNote(t *testing.T) {
	var discovery, model map[string]any
	if err := json.Unmarshal(discoveryModel(), &discovery); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wiredInventoryModel()), &model); err != nil {
		t.Fatal(err)
	}
	source := discovery["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
	target := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
	target["discovery"] = source["discovery"]
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := (&client{}).compileConfigReports(raw, []string{"inventory"})
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].count != 3 || plans[0].observationNote == nil || !strings.Contains(plans[0].observationNote(), "REPROVISION") || !strings.Contains(plans[0].observationNote(), (&discoveryOutcomeSummary{}).message()) {
		t.Fatal("lost combined observation explanation", plans[0])
	}
}
