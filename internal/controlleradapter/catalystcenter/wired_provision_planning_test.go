// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWiredProvisionRevisionPlanning(t *testing.T) {
	for _, mode := range []string{"new", "applied", "new-revision", "late-operation", "late-source", "late-controller"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(fabricEdgeIntentModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			intents, err := parseFabricDeviceIntents([]byte(fabricEdgeIntentModel))
			if err != nil {
				t.Fatal(err)
			}
			want := intents[0].inventory
			armed := false
			mutations := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					mutations++
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					if armed && mode == "late-controller" {
						nc, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						nc.Generation++
						if err := a.statusWriter.Update(ctx, nc); err != nil {
							t.Error(err)
						}
						armed = false
					}
					if armed && mode == "late-source" {
						changed := config.DeepCopy()
						changed.Generation++
						if err := a.statusWriter.Update(ctx, changed); err != nil {
							t.Error(err)
						}
						armed = false
					}
					if armed && mode == "late-operation" {
						nc, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
						if nc.Status.Configuration == nil {
							nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
						}
						nc.Status.Configuration.Operation = &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), Phase: "Prepared"}
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Error(err)
						}
						armed = false
					}
					rows = []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}}
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
				case wiredProvisionPath:
					rows = []wiredProvisionedDevice{{ID: "provisioned", SiteID: "building", DeviceID: "edge-id"}}
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			initial, err := a.client.planWiredDeviceProvision(ctx, want, "REPROVISION")
			if err != nil || initial == nil {
				t.Fatal(initial, err)
			}
			if mode == "applied" || mode == "new-revision" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				submitted := metav1.NewTime(time.Now().Add(-time.Minute))
				verified := metav1.NewTime(time.Now().Add(-time.Second))
				// Fixture represents an already settled operation; shared receipt emission
				// itself is exercised by TestWiredProvisionRetainedRevision.
				operation := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: initial.resource, Action: initial.action, TargetID: initial.identity.target, ParentID: initial.identity.parent, TargetSerial: initial.identity.serial, Phase: "Verified", TaskID: "task", SubmittedAt: &submitted}
				nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
				// Reuse prepareConfiguration's owner shape without depending on its Go type.
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", initial.resource, initial.action, initial.identity); err != nil {
					t.Fatal(err)
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation = nil
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: initial.resource, Operation: operation, VerifiedAt: &verified}}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if mode == "new-revision" {
					config.Generation++
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
			}
			armed = true
			write, err := a.planWiredProvisionRevision(ctx, config, nil, want, "REPROVISION")
			if mode == "late-operation" || mode == "late-source" || mode == "late-controller" {
				if err == nil || write != nil {
					t.Fatal("late change accepted", write, err)
				}
			} else if err != nil || (write == nil) != (mode == "applied") {
				t.Fatal("wrong revision decision", write, err)
			}
			if mutations != 0 {
				t.Fatal("revision planning mutated controller")
			}
		})
	}
}
