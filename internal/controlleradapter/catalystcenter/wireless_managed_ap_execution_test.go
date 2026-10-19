// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestWirelessManagedAPExecution(t *testing.T) {
	for _, anchor := range []bool{false, true} {
		for _, scenario := range []string{"cycle", "site-replaced", "device-replaced", "membership-changed"} {
			t.Run(fmt.Sprint(anchor, "/", scenario), func(t *testing.T) {
				ctx := context.Background()
				device := Device{ID: "wlc", Hostname: "WLC", Serial: "serial", ManagementIP: "198.51.100.10", Family: "Wireless Controller", Reachability: "Reachable"}
				siteB := "b"
				members := map[string][]string{"primary": {"a"}, "secondary": {}, "anchor": {"a"}}
				writes := 0
				var pending map[string][]string
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == authPath:
						fmt.Fprint(w, `{"Token":"test"}`)
					case r.URL.Path == devicesPath:
						json.NewEncoder(w).Encode(map[string]any{"response": []Device{device}})
					case r.URL.Path == sitesPath:
						json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", Type: "global", NameHierarchy: "Global"}, {ID: "a", Name: "A", Type: "area", ParentID: "global", NameHierarchy: "Global/A"}, {ID: siteB, Name: "B", Type: "area", ParentID: "global", NameHierarchy: "Global/B"}}})
					case strings.HasSuffix(r.URL.Path, "/managedApLocations/count"):
						json.NewEncoder(w).Encode(map[string]any{"response": map[string]int{"primaryManagedApLocationsCount": len(members["primary"]), "secondaryManagedApLocationsCount": len(members["secondary"]), "anchorManagedApLocationsCount": len(members["anchor"])}})
					case r.Method == "GET":
						kind := ""
						for _, k := range []string{"primary", "secondary", "anchor"} {
							if strings.HasSuffix(r.URL.Path, "/"+k+"ManagedApLocations") {
								kind = k
							}
						}
						if kind == "" {
							t.Error("unexpected GET")
							w.WriteHeader(404)
							return
						}
						rows := []wirelessManagedAPLocation{}
						for _, id := range members[kind] {
							path := "Global/A"
							if id == "b" {
								path = "Global/B"
							}
							rows = append(rows, wirelessManagedAPLocation{SiteID: id, SitePath: path})
						}
						json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"managedApLocations": rows}}})
					case r.Method == "POST":
						endpoint := "assignManagedApLocations"
						if anchor {
							endpoint = "assignAnchorManagedApLocations"
						}
						if r.URL.Path != "/dna/intent/api/v1/wirelessControllers/wlc/"+endpoint {
							t.Error("wrong mutation endpoint")
						}
						if json.NewDecoder(r.Body).Decode(&pending) != nil {
							t.Error("invalid payload")
						}
						expected := map[string][]string{"primaryManagedAPLocationsSiteIds": {"a", "b"}, "secondaryManagedAPLocationsSiteIds": {}}
						if anchor {
							expected = map[string][]string{"anchorManagedAPLocationsSiteIds": {"a", "b"}}
						}
						if !reflect.DeepEqual(pending, expected) {
							t.Error("incorrect assignment payload", pending)
						}
						writes++
						fmt.Fprint(w, `{"response":{"taskId":"assignment-task"}}`)
					default:
						t.Error("unexpected request")
						w.WriteHeader(404)
					}
				})
				e := &wirelessManagedAPExecutor{client: c, reference: configurationDeviceReference{Name: "WLC", Serial: "serial"}, anchor: anchor, desired: wirelessManagedAPIntent{primary: []string{"Global/A", "Global/B"}, anchor: []string{"Global/A", "Global/B"}}}
				provision := &wirelessProvisionExecutor{reference: e.reference, apDependencies: []wirelessProvisionAPDependency{{prefix: "locations/", executor: e}}}
				if _, err := provision.requiredAPReceipts(ctx); err != errConfigurationBusy {
					t.Fatal("unconverged locations passed provisioning prerequisite", err)
				}
				write, err := e.next(ctx)
				if err != nil || write == nil {
					t.Fatal("plan", err)
				}
				op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, TargetSerial: write.identity.serial}
				switch scenario {
				case "site-replaced":
					siteB = "replacement"
				case "device-replaced":
					device.ID = "new-wlc"
				case "membership-changed":
					members["primary"] = []string{}
					members["anchor"] = []string{}
				}
				task, err := write.submit(ctx)
				if scenario != "cycle" {
					if err == nil || writes != 0 {
						t.Fatal("changed state submitted", err, writes)
					}
					return
				}
				if err != nil || task.ID != "assignment-task" || writes != 1 {
					t.Fatal("submit", task, err, writes)
				}
				if ok, err := e.verify(ctx, op); err != nil || ok {
					t.Fatal("acceptance treated as readback", ok, err)
				}
				for kind := range members {
					if ids, ok := pending[kind+"ManagedAPLocationsSiteIds"]; ok {
						members[kind] = ids
					}
				}
				restarted := *e
				if ok, err := restarted.verify(ctx, op); err != nil || !ok {
					t.Fatal("readback", ok, err)
				}
				if next, err := restarted.next(ctx); err != nil || next != nil || writes != 1 {
					t.Fatal("replayed assignment", err)
				}
				resources, err := provision.requiredAPReceipts(ctx)
				if err != nil || len(resources) != 1 || resources[0] != "locations/"+op.Resource {
					t.Fatal("assignment receipt identity lost", resources, err)
				}
				device.Serial = "replacement"
				if ok, err := restarted.verify(ctx, op); err == nil || ok {
					t.Fatal("replaced serial verified")
				}
			})
		}
	}
}
