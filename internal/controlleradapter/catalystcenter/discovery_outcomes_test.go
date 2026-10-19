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

func TestDiscoveryOutcomePaginationAndValidation(t *testing.T) {
	for _, mode := range []string{"valid", "repeat", "duplicate-address", "null", "missing", "invalid-ip", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if mode == "null" {
					fmt.Fprint(w, `{"response":null}`)
					return
				}
				if mode == "missing" {
					fmt.Fprint(w, `{}`)
					return
				}
				start, count := 0, 100
				if strings.HasSuffix(r.URL.Path, "/101/100") {
					start, count = 100, 1
				}
				if mode == "repeat" {
					start = 0
				}
				if mode == "oversize" {
					count = 101
				}
				rows := []discoveryDeviceOutcome{}
				for i := start; i < start+count; i++ {
					ip := fmt.Sprintf("192.0.2.%d", i+1)
					if mode == "invalid-ip" {
						ip = "invalid"
					}
					if mode == "duplicate-address" {
						ip = "192.0.2.1"
					}
					rows = append(rows, discoveryDeviceOutcome{ID: fmt.Sprint(i + 1), IP: ip})
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.discoveryDevices(context.Background(), "1")
			if mode == "valid" {
				if err != nil || len(rows) != 101 {
					t.Fatal(len(rows), err)
				}
			} else if err == nil {
				t.Fatal("accepted malformed or incomplete collection")
			}
		})
	}
}

func TestDiscoveryOutcomeReportSeparatesConfigurationAndCollection(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case globalCredentialsPath:
			fmt.Fprint(w, `{"response":{"cliCredential":[{"id":"credential-id","description":"lab-cli"}]}}`)
		case discoveryPath + "/1/100":
			json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{{ID: "42", Name: "fixture", Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: "SSH", CredentialIDs: []string{"credential-id"}, Condition: "Complete", Status: "Inactive"}}})
		case discoveryPath + "/42/network-device/1/100":
			fmt.Fprint(w, `{"response":[{"id":"device-1","managementIpAddress":"192.0.2.1","cliStatus":"FAILURE","snmpStatus":"SUCCESS","netconfStatus":"SECRET-MARKER","inventoryCollectionStatus":"Partial Collection Failure","errorDescription":"SECRET-MARKER"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	plans, err := c.compileConfigReports(discoveryModel(), []string{"inventory"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(context.Background())
	if err != nil || len(drift) != 0 {
		t.Fatal("operational failure must not trigger discovery replacement", drift, err)
	}
	note := plans[0].observationNote()
	if !strings.Contains(note, "CLI 0 successful/1 failed/0 unverified") || !strings.Contains(note, "NETCONF 0 successful/0 failed/1 unverified") || !strings.Contains(note, "0 Managed/Reachable, 1 other/unverified") || strings.Contains(note, "SECRET") {
		t.Fatal(note)
	}
	// Repeated observation must replace counts, not accumulate them.
	if _, err = plans[0].observe(context.Background()); err != nil || plans[0].observationNote() != note {
		t.Fatal("unstable repeated report", err)
	}
}

func TestDiscoveryEmptyOutcomesAreUnverified(t *testing.T) {
	s := discoveryOutcomeSummary{MissingJobs: 1}
	s.add(nil)
	if !strings.Contains(s.message(), "1 missing, 1 without device outcomes (unverified)") {
		t.Fatal(s.message())
	}
}
