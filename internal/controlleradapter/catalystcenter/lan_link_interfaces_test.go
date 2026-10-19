// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func lanPortFixture(deviceID, name string) lanLinkInterface {
	return lanLinkInterface{ID: deviceID + "-port", DeviceID: deviceID, PortName: name, Type: "Physical", Index: "1", AdminStatus: "UP", Status: "Up", Mode: "routed"}
}

func TestLANLinkInterfaceInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "virtual", "unknown-status", "duplicate-id", "duplicate-name", "duplicate-index", "foreign-device", "missing-id", "missing-index", "invalid-index", "invalid-name", "null", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != "/dna/intent/api/v1/interface/network-device/seed" || r.URL.RawQuery != "" {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				rows := []lanLinkInterface{lanPortFixture("seed", "TenGigabitEthernet1/0/1")}
				switch mode {
				case "empty":
					rows = []lanLinkInterface{}
				case "virtual":
					rows[0].Type = "Virtual"
				case "unknown-status":
					rows[0].Status = "NEW-STATE"
				case "duplicate-id", "duplicate-name", "duplicate-index":
					other := rows[0]
					other.ID = "second"
					other.PortName = "TenGigabitEthernet1/0/2"
					other.Index = "2"
					if mode == "duplicate-id" {
						other.ID = rows[0].ID
					}
					if mode == "duplicate-name" {
						other.PortName = "tengigabitethernet1/0/1"
					}
					if mode == "duplicate-index" {
						other.Index = rows[0].Index
					}
					rows = append(rows, other)
				case "foreign-device":
					rows[0].DeviceID = "other"
				case "missing-id":
					rows[0].ID = ""
				case "missing-index":
					rows[0].Index = ""
				case "invalid-index":
					rows[0].Index = "1a"
				case "invalid-name":
					rows[0].PortName = "Gi1/0/1\n"
				case "null":
					fmt.Fprint(w, `{"response":null}`)
					return
				case "partial":
					w.WriteHeader(206)
				case "no-content":
					w.WriteHeader(204)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.lanLinkInterfaces(context.Background(), "seed")
			valid := mode == "valid" || mode == "empty" || mode == "virtual" || mode == "unknown-status"
			if (err == nil) != valid {
				t.Fatal(rows, err)
			}
			if err == nil {
				_, err = bindLANLinkInterface("seed", "TenGigabitEthernet1/0/1", rows)
				if (err == nil) != (mode == "valid" || mode == "unknown-status") {
					t.Fatal("wrong physical binding", err)
				}
			}
		})
	}
	row := lanPortFixture("seed", "TenGigabitEthernet1/0/1")
	if _, err := bindLANLinkInterface("seed", "Te1/0/1", []lanLinkInterface{row}); err == nil {
		t.Fatal("guessed abbreviation")
	}
	row.MappedID = "physical"
	if _, err := bindLANLinkInterface("seed", row.PortName, []lanLinkInterface{row}); err == nil {
		t.Fatal("accepted mapped interface")
	}
}

func TestLANLinkEndpointRevalidation(t *testing.T) {
	wants, err := parseLANLinks(lanLinkModel(t, lanLinkFixture()))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"unchanged", "port-replaced", "port-config", "device-replaced", "serial-alias", "disappeared"} {
		t.Run(mode, func(t *testing.T) {
			changed := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				if r.URL.Path == devicesPath {
					rows := []Device{{ID: "seed", ManagementIP: "192.0.2.10", Serial: "OLD-SEED"}, {ID: "peer", ManagementIP: "192.0.2.11", Serial: "OLD-PEER"}}
					if changed && mode == "device-replaced" {
						rows[1].Serial = "NEW-PEER"
					}
					if changed && mode == "serial-alias" {
						rows = append(rows, Device{ID: "alias", ManagementIP: "192.0.2.12", Serial: "old-peer"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				device, name := "seed", "TenGigabitEthernet1/0/1"
				if r.URL.Path == "/dna/intent/api/v1/interface/network-device/peer" {
					device, name = "peer", "TenGigabitEthernet1/0/2"
				} else if r.URL.Path != "/dna/intent/api/v1/interface/network-device/seed" {
					t.Error("unexpected request", r.URL)
					w.WriteHeader(404)
					return
				}
				rows := []lanLinkInterface{lanPortFixture(device, name)}
				if changed && device == "peer" {
					switch mode {
					case "port-replaced":
						rows[0].ID = "new-port"
					case "port-config":
						rows[0].IPv4 = "192.0.2.20"
					case "disappeared":
						rows = []lanLinkInterface{}
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			before, err := c.lanLinkEndpointSnapshot(context.Background(), wants[0])
			if err != nil {
				t.Fatal(err)
			}
			if before.primary.device.ID != "seed" || before.secondary.device.ID != "peer" {
				t.Fatal("source/destination reversed")
			}
			changed = true
			err = c.revalidateLANLinkEndpoints(context.Background(), wants[0], before)
			if (err == nil) != (mode == "unchanged") {
				t.Fatal("wrong revalidation outcome", err)
			}
		})
	}
}
