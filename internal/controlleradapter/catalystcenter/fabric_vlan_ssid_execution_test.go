// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestFabricVLANSSIDGuardedExecution(t *testing.T) {
	for _, mode := range []string{"accepted", "noop", "stale", "read-error", "denied", "nil-guard", "empty-202", "missing-task", "unauthorized", "server-error"} {
		t.Run(mode, func(t *testing.T) {
			tag := "Guests"
			before := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest", SecurityGroup: &tag}}}}
			payload := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest", SecurityGroup: &tag}, {Name: "Staff"}}}}
			if mode == "noop" {
				payload = before
			}
			puts, guards, reads := 0, 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "PUT" || r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids" {
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				puts++
				var got []fabricVLANSSID
				if json.NewDecoder(r.Body).Decode(&got) != nil || !reflect.DeepEqual(got, payload) {
					t.Error("full replacement lost retained policy", got)
				}
				switch mode {
				case "empty-202":
					w.WriteHeader(202)
				case "missing-task":
					fmt.Fprint(w, `{"response":{}}`)
				case "unauthorized":
					w.WriteHeader(401)
				case "server-error":
					w.WriteHeader(500)
				default:
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				}
			})
			guard := func(context.Context) error {
				guards++
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			}
			if mode == "nil-guard" {
				guard = nil
			}
			collect := func(context.Context) (*fabricVLANSSIDDependencies, error) {
				reads++
				if reads > 1 && mode == "read-error" {
					return nil, errors.New("read failed")
				}
				digest := strings.Repeat("a", 64)
				if reads > 1 && mode == "stale" {
					digest = strings.Repeat("b", 64)
				}
				return &fabricVLANSSIDDependencies{siteID: "site", fabricID: "fabric", before: before, payload: payload, claim: &fabricVLANSSID{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}, digest: digest}, nil
			}
			ctx := context.Background()
			write, err := c.planFabricVLANSSIDWrite(ctx, "Global/Lab", collect, guard)
			if mode == "nil-guard" {
				if err == nil || write != nil || reads != 0 {
					t.Fatal("missing authority accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "noop" {
				if write != nil || puts != 0 || guards != 0 {
					t.Fatal("no-op submitted")
				}
				return
			}
			if write == nil || write.action != "Update" || write.identity.target != "fabric" || write.identity.parent != "site" {
				t.Fatal("missing plan")
			}
			task, err := write.submit(ctx)
			if (err == nil) != (mode == "accepted") {
				t.Fatal("submission outcome", task, err)
			}
			if mode == "accepted" && task.ID != "task" {
				t.Fatal("missing task")
			}
			expected := 1
			if mode == "stale" || mode == "read-error" || mode == "denied" {
				expected = 0
			}
			if puts != expected {
				t.Fatal("unexpected writes", puts)
			}
			if (mode == "stale" || mode == "read-error") && guards != 0 {
				t.Fatal("authority consumed before fresh evidence")
			}
			_, err = write.submit(ctx)
			if !errors.Is(err, errConfigurationTransition) || puts != expected || reads != 2 {
				t.Fatal("submission replayed", err, puts, reads)
			}
		})
	}
}

func TestFabricVLANSSIDOperationIdentity(t *testing.T) {
	deps := &fabricVLANSSIDDependencies{siteID: "site", fabricID: "fabric", before: []fabricVLANSSID{}, payload: []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}}}
	original, err := fabricVLANSSIDResource("Global/Lab", deps)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(original, ":")
	if len(parts) != 6 || parts[0] != "fabric-vlan-ssid-update" {
		t.Fatal("invalid identity")
	}
	for _, part := range parts[1:] {
		if len(part) != 64 {
			t.Fatal("invalid hash")
		}
	}
	deps.before = deps.payload
	converged, _ := fabricVLANSSIDResource("Global/Lab", deps)
	after := strings.Split(converged, ":")
	if after[1] != parts[1] || after[2] == parts[2] || after[3] != parts[3] {
		t.Fatal("baseline and result not independent")
	}
	deps.fabricID = "replacement"
	replaced, _ := fabricVLANSSIDResource("Global/Lab", deps)
	if strings.Split(replaced, ":")[1] == parts[1] {
		t.Fatal("fabric replacement lost")
	}
}
