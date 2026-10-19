// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWirelessProvisionExecutorPreparedRecovery(t *testing.T) {
	for _, scenario := range []string{"same baseline", "changed baseline", "accepted", "different source revision", "changed requirement", "changed dependencies", "other controller"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			stamp := "100"
			writes := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "wlc", Hostname: "WLC", Serial: "serial", ManagementIP: "198.51.100.10", Family: "Wireless Controller", Reachability: "Reachable"}}})
				case "/dna/intent/api/v1/wirelessControllers/wlc/provisionStatus":
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"networkDeviceId": "wlc", "deviceName": "WLC", "siteId": "site", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "NOT_STARTED", "outOfSync": false, "lastProvisionedTimeStamp": stamp}})
				default:
					writes++
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &wirelessProvisionExecutor{adapter: a, config: config, reference: configurationDeviceReference{Name: "WLC", Serial: "serial"}, state: "REPROVISION", siteID: "site"}
			e.dependencyDigest = strings.Repeat("c", 64)
			first, err := e.next(ctx)
			if err != nil || first == nil {
				t.Fatal("initial plan", err)
			}
			if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", first.resource, first.action, first.identity); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "changed baseline":
				stamp = "101"
			case "accepted":
				nc.Status.Configuration.Operation.Phase = "Accepted"
			case "different source revision":
				nc.Status.Configuration.Operation.SourceVersion = "other"
			case "other controller":
				e.reference.Name = "Other"
			case "changed dependencies":
				e.dependencyDigest = strings.Repeat("d", 64)
			case "changed requirement":
				e.modelConfigRequired = true
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			second, err := e.next(ctx)
			if scenario == "same baseline" {
				if err != nil || second == nil || second.resource != first.resource {
					t.Fatal("prepared baseline not recovered", err)
				}
			} else if scenario == "other controller" {
				if err != nil || second != nil {
					t.Fatal("unrelated executor blocked prepared owner", err)
				}
			} else if err == nil || second != nil {
				t.Fatal("changed/accepted operation replanned")
			}
			verification := nc.Status.Configuration.Operation.DeepCopy()
			verification.Phase = "Verifying"
			verification.ControllerGeneration++
			if done, err := e.verify(ctx, verification); err == nil || done {
				t.Fatal("verification accepted another controller revision")
			}
			if writes != 0 {
				t.Fatal("planning caused a mutation")
			}
		})
	}
}
