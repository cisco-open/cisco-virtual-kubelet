// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strings"
	"testing"
)

func TestFabricVLANSSIDReadback(t *testing.T) {
	for _, mode := range []string{"converged", "missing", "extra", "policy-changed", "late-policy", "replaced-fabric", "late-error", "malformed-resource", "wrong-phase", "no-task", "wrong-path"} {
		t.Run(mode, func(t *testing.T) {
			tag := "Guests"
			want := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest", SecurityGroup: &tag}, {Name: "Staff"}}}}
			resource, _ := fabricVLANSSIDResource("Global/Lab", &fabricVLANSSIDDependencies{siteID: "area", fabricID: "fabric", before: []fabricVLANSSID{}, payload: want})
			op := &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Phase: "Verifying", Action: "Update", TaskID: "task", ParentID: "area", TargetID: "fabric", Resource: resource}
			if mode == "wrong-phase" {
				op.Phase = "Accepted"
			}
			if mode == "no-task" {
				op.TaskID = ""
			}
			if mode == "malformed-resource" {
				op.Resource = resource + ":extra"
			}
			reads, calls := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					t.Error("readback mutated controller")
					w.WriteHeader(400)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					b := false
					id := "fabric"
					if mode == "replaced-fabric" {
						id = "replacement"
					}
					reply([]fabricSite{{ID: id, SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids", "/dna/intent/api/v1/sda/fabrics/replacement/vlanToSsids":
					reads++
					if mode == "late-error" && reads > 1 {
						w.WriteHeader(500)
						return
					}
					policy := "Guests"
					if mode == "policy-changed" || mode == "late-policy" && reads > 1 {
						policy = "Changed"
					}
					rows := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest", SecurityGroup: &policy}, {Name: "Staff"}}}}
					if mode == "missing" {
						rows[0].SSIDs = rows[0].SSIDs[:1]
					}
					if mode == "extra" {
						rows[0].SSIDs = append(rows[0].SSIDs, fabricSSIDBinding{Name: "Unrelated"})
					}
					reply(rows)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			path := "Global/Lab"
			if mode == "wrong-path" {
				path = "Global"
			}
			done, err := c.verifyFabricVLANSSIDs(context.Background(), path, op)
			if mode == "converged" {
				if err != nil || !done {
					t.Fatal("converged result rejected", err)
				}
				return
			}
			if done {
				t.Fatal("incorrect state accepted")
			}
			pending := mode == "missing" || mode == "extra" || mode == "policy-changed"
			if (err == nil) != pending {
				t.Fatal("unexpected verification outcome", err)
			}
			if (mode == "wrong-phase" || mode == "no-task" || mode == "malformed-resource") && calls != 0 {
				t.Fatal("invalid journal caused native reads")
			}
		})
	}
}

func TestFabricVLANSSIDResourceValidation(t *testing.T) {
	valid := "fabric-vlan-ssid-update:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64) + ":" + strings.Repeat("e", 64)
	if _, err := parseFabricVLANSSIDResource(valid); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{valid + ":extra", strings.Replace(valid, "update", "create", 1), strings.ToUpper(valid), valid[:len(valid)-1], strings.ReplaceAll(valid, "c", "z"), strings.Replace(valid, strings.Repeat("c", 64), strings.Repeat("b", 64), 1)} {
		if _, err := parseFabricVLANSSIDResource(bad); err == nil {
			t.Fatal("invalid resource accepted", bad)
		}
	}
}

func TestFabricVLANSSIDGroupResource(t *testing.T) {
	valid := "fabric-vlan-ssid-update:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64) + ":" + strings.Repeat("e", 64)
	for _, resource := range []string{valid, "fabric-vlan-ssid-update:pruned"} {
		if !isReservedConfigurationGroupResource("fabric", resource) {
			t.Fatal("group scope lost")
		}
		if validConfigurationGroupAction("fabric", resource, "Update") != (resource == valid) {
			t.Fatal("journal validation")
		}
		if validConfigurationGroupAction("fabric", resource, "Create") || validConfigurationGroupAction("wireless", resource, "Update") {
			t.Fatal("wrong operation admitted")
		}
	}
}
