// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestMulticastNativeExecution(t *testing.T) {
	for _, mode := range []string{"create", "update", "noop", "stale-before", "retained-change-before", "lost-receipt", "wrong-readback", "missing-allocation", "changed-serial-after", "replaced-update-target", "retained-change-after", "guard-denied", "declared-peer-change-after"} {
		t.Run(mode, func(t *testing.T) {
			b := false
			writes := 0
			pools := fixturePools()
			reservations := fixtureReservations()
			devices := []Device{{ID: "a", Hostname: "border-a.lab", Serial: "SERIAL-A"}, {ID: "b", Hostname: "border-b.lab", Serial: "SERIAL-B"}}
			members := []fabricDeviceMembership{{ID: "member-a", NetworkDeviceID: "a", FabricID: "fabric", Roles: []string{"BORDER_NODE"}}, {ID: "member-b", NetworkDeviceID: "b", FabricID: "fabric", Roles: []string{"BORDER_NODE"}}}
			native := []map[string]any{multicastSiteRow(t, "Other", "access", "b").fields}
			existing := mode == "update" || mode == "noop" || mode == "replaced-update-target"
			if existing {
				native = append(native, multicastSiteRow(t, "Corp", "access", "a").fields)
			}
			model := map[string]any{"name": "Corp", "ip_pool_name": "access", "ipv4_ssm_ranges": []any{"232.0.0.0/8"}, "multicast_rps": []any{map[string]any{"rp_location": "FABRIC", "fabric_rps": []any{"border-a"}}}}
			if mode == "noop" {
				model["ipv4_ssm_ranges"] = []any{}
			}
			want, err := parseFabricMulticastVN(model)
			if err != nil {
				t.Fatal(err)
			}
			wants := []fabricMulticastVNIntent{want}
			if mode == "declared-peer-change-after" {
				peer, err := parseFabricMulticastVN(map[string]any{"name": "Other", "ip_pool_name": "access", "ipv4_ssm_ranges": []any{}, "multicast_rps": []any{map[string]any{"rp_location": "FABRIC", "fabric_rps": []any{"border-b"}}}})
				if err != nil {
					t.Fatal(err)
				}
				wants = append(wants, peer)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case fabricL3NetworksPath:
					rows := []fabricL3Network{{ID: "vn", Name: "Corp", Fabrics: []string{"fabric"}}}
					if mode == "declared-peer-change-after" {
						rows = append(rows, fabricL3Network{ID: "vn-other", Name: "Other", Fabrics: []string{"fabric"}})
					}
					reply(rows)
				case globalPoolsPath:
					reply(pools)
				case sitePoolsPath:
					reply(reservations)
				case devicesPath:
					reply(devices)
				case fabricDevicesPath:
					reply(members)
				case fabricMulticastVNsPath:
					if r.Method == "GET" {
						reply(native)
						return
					}
					expected := http.MethodPost
					if existing {
						expected = http.MethodPut
					}
					if r.Method != expected {
						t.Error("wrong write method", r.Method)
						w.WriteHeader(400)
						return
					}
					writes++
					var body []map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
						t.Error("bad payload")
						w.WriteHeader(400)
						return
					}
					row := body[0]
					if row["fabricId"] != "fabric" || row["virtualNetworkName"] != "Corp" || row["ipPoolName"] != "access" {
						t.Error("wrong identity")
					}
					rp := row["multicastRPs"].([]any)[0].(map[string]any)
					if existing {
						if row["id"] != "multicast-Corp" || rp["ipv4Address"] != "192.0.2.1" {
							t.Error("lost existing ID/allocation")
						}
					} else if row["id"] != nil || rp["ipv4Address"] != nil {
						t.Error("create contains native allocated fields")
					}
					row["id"] = "multicast-Corp"
					rp["ipv4Address"] = "192.0.2.1"
					if mode == "missing-allocation" {
						delete(rp, "ipv4Address")
					}
					if mode == "wrong-readback" {
						row["ipv4SsmRanges"] = []any{}
					}
					if mode == "replaced-update-target" {
						row["id"] = "replacement"
					}
					if existing {
						native[1] = row
					} else {
						native = append(native, row)
					}
					if mode == "retained-change-after" || mode == "declared-peer-change-after" {
						native[0]["ipv4SsmRanges"] = []any{"233.0.0.0/8"}
					}
					if mode == "changed-serial-after" {
						devices[0].Serial = "replacement"
					}
					if mode == "lost-receipt" {
						reply(map[string]string{})
						return
					}
					reply(map[string]string{"taskId": "task"})
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
				}
			})
			ctx := context.Background()
			write, err := c.planGuardedFabricMulticastVN(ctx, "Global/Lab", wants, "Corp", func(context.Context) error {
				if mode == "guard-denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			})
			if mode == "noop" {
				if err != nil || write != nil || writes != 0 {
					t.Fatal("noop", err)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("planning", err)
			}
			if mode == "stale-before" {
				reservations[0].ID = "replacement"
			}
			if mode == "retained-change-before" {
				native[0]["ipv4SsmRanges"] = []any{"233.0.0.0/8"}
			}
			task, err := write.submit(ctx)
			if _, again := write.submit(ctx); !errors.Is(again, errConfigurationTransition) {
				t.Fatal("one-use planner replayed", again)
			}
			if mode == "guard-denied" {
				if !errors.Is(err, errConfigurationDeviceAdmission) || writes != 0 {
					t.Fatal("guard bypassed", err, writes)
				}
				return
			}
			if mode == "stale-before" || mode == "retained-change-before" {
				if err == nil || writes != 0 {
					t.Fatal("stale snapshot submitted", err, writes)
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("ambiguous write accepted or retried")
				}
				return
			}
			if err != nil || task.ID != "task" || writes != 1 {
				t.Fatal("submission", err, writes)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
			verified, err := c.verifyFabricMulticastVN(ctx, "Global/Lab", wants, "Corp", op)
			valid := mode == "create" || mode == "update"
			if verified != valid || valid && err != nil {
				t.Fatal("verification", verified, err)
			}
			if valid {
				next, err := c.planGuardedFabricMulticastVN(ctx, "Global/Lab", wants, "Corp", func(context.Context) error {
					if mode == "guard-denied" {
						return errConfigurationDeviceAdmission
					}
					return nil
				})
				if err != nil || next != nil || writes != 1 {
					t.Fatal("replayed converged VN", err)
				}
			}
		})
	}
}

func TestMulticastNativeRequiresGuard(t *testing.T) {
	if _, err := (&client{}).planGuardedFabricMulticastVN(context.Background(), "Global/Lab", nil, "Corp", nil); !errors.Is(err, errConfigurationDeviceAdmission) {
		t.Fatal("missing authority accepted", err)
	}
}
