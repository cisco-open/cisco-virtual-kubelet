// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"testing"
)

func borderFixture() map[string]any {
	return map[string]any{"border_types": []any{"LAYER_3", "LAYER_2"}, "local_autonomous_system_number": 4294967295, "default_exit": false, "import_external_routes": true, "border_priority": 10, "prepend_autonomous_system_count": 0}
}
func TestFabricBorderSettingsModel(t *testing.T) {
	good := borderFixture()
	b, err := parseFabricBorderSettings(good)
	if err != nil {
		t.Fatal(err)
	}
	if b.Layer3.ASN != "4294967295" || *b.Layer3.DefaultExit || !*b.Layer3.ImportExternal || b.Layer3.Priority != nil || b.Layer3.Prepend != nil || !reflect.DeepEqual(b.Types, []string{"LAYER_2", "LAYER_3"}) {
		t.Fatal(b)
	}
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"border_types", []any{"LAYER_2", "LAYER_2"}}, {"border_types", []any{"BAD"}}, {"border_types", []any{}},
		{"local_autonomous_system_number", 0}, {"local_autonomous_system_number", 4294967296}, {"local_autonomous_system_number", 1.5},
		{"default_exit", nil}, {"default_exit", "false"}, {"import_external_routes", nil},
		{"border_priority", 0}, {"border_priority", 11}, {"border_priority", 1.5}, {"border_priority", "2"},
		{"prepend_autonomous_system_count", -1}, {"prepend_autonomous_system_count", 11}, {"networkDeviceId", "injected"},
		{"l2_handoffs", []any{}},
	} {
		t.Run(fmt.Sprint(tc.key, tc.value), func(t *testing.T) {
			m := borderFixture()
			m[tc.key] = tc.value
			if _, err := parseFabricBorderSettings(m); err == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
	l2, err := parseFabricBorderSettings(map[string]any{"border_types": []any{"LAYER_2"}})
	if err != nil || l2.Layer3 != nil {
		t.Fatal(l2, err)
	}
	bad := borderFixture()
	bad["border_types"] = []any{"LAYER_2"}
	if _, err := parseFabricBorderSettings(bad); err == nil {
		t.Fatal("discarded L3 fields")
	}
	good["border_priority"] = 1
	good["prepend_autonomous_system_count"] = 10
	b, err = parseFabricBorderSettings(good)
	if err != nil || *b.Layer3.Priority != 1 || *b.Layer3.Prepend != 10 {
		t.Fatal(b, err)
	}
}

func TestFabricDeviceNativeCreate(t *testing.T) {
	for _, mode := range []string{"create", "unchanged", "conflict", "late-target", "late-peer", "lost-receipt", "defaults", "wrong-readback", "reordered", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			border, err := parseFabricBorderSettings(borderFixture())
			if err != nil {
				t.Fatal(err)
			}
			want := fabricDeviceConfiguration{NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE", "CONTROL_PLANE_NODE"}, Border: border}
			observed := want
			observed.ID = "membership"
			rows := []fabricDeviceConfiguration{}
			if mode == "unchanged" || mode == "conflict" || mode == "defaults" {
				rows = append(rows, observed)
			}
			if mode == "conflict" {
				rows[0].Roles = []string{"BORDER_NODE"}
			}
			if mode == "defaults" {
				priority, prepend := 10, 0
				rows[0].Border.Layer3.Priority = &priority
				rows[0].Border.Layer3.Prepend = &prepend
				want.Border, _ = parseFabricBorderSettings(borderFixture())
			}
			gets, writes := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != fabricDevicesPath {
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				if r.Method == "GET" {
					gets++
					if r.URL.Query().Get("fabricId") != "fabric" {
						t.Error("unscoped inventory")
					}
					if gets == 2 && mode == "late-target" {
						rows = append(rows, observed)
					}
					if gets == 2 && mode == "late-peer" {
						rows = append(rows, fabricDeviceConfiguration{ID: "peer-row", NetworkDeviceID: "peer", FabricID: "fabric", Roles: []string{"EXTENDED_NODE"}})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				if r.Method != "POST" {
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(500)
					return
				}
				writes++
				var payload []fabricDeviceConfiguration
				if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 {
					t.Error("invalid payload")
					w.WriteHeader(500)
					return
				}
				if payload[0].ID != "" || payload[0].NetworkDeviceID != "device" || payload[0].FabricID != "fabric" || payload[0].Border.Layer3.Priority != nil || *payload[0].Border.Layer3.DefaultExit {
					t.Error("wrong native body", payload)
				}
				payload[0].ID = "membership"
				rows = payload
				if mode == "wrong-readback" {
					rows[0].Roles = []string{"BORDER_NODE"}
				}
				if mode == "reordered" {
					rows[0].Roles = []string{"CONTROL_PLANE_NODE", "BORDER_NODE"}
					rows[0].Border.Types = []string{"LAYER_3", "LAYER_2"}
				}
				if mode == "lost-receipt" {
					fmt.Fprint(w, `{"response":{}}`)
					return
				}
				fmt.Fprint(w, `{"response":{"taskId":"created"}}`)
			})
			write, err := c.planFabricDeviceCreate(ctx, want)
			if mode == "conflict" {
				if !errors.Is(err, errConfigurationTransition) || write != nil {
					t.Fatal(write, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unchanged" || mode == "defaults" {
				if write != nil || writes != 0 {
					t.Fatal(write, writes)
				}
				return
			}
			if write == nil {
				t.Fatal("missing plan")
			}
			if mode == "snapshot" {
				*want.Border.Layer3.DefaultExit = true
				want.Roles[0] = "EDGE_NODE"
				want.Border.Types[0] = "BAD"
			}
			task, err := write.submit(ctx)
			if mode == "late-target" || mode == "late-peer" {
				if !errors.Is(err, errConfigurationStale) || writes != 0 {
					t.Fatal(err, writes)
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal(err, writes)
				}
				return
			}
			if err != nil || task.ID != "created" || writes != 1 {
				t.Fatal(task, err, writes)
			}
			if mode == "snapshot" {
				return
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Resource: write.resource, TargetID: "device", ParentID: "fabric"}
			verified, err := c.verifyFabricDeviceCreate(ctx, want, op)
			if err != nil || verified == (mode == "wrong-readback") {
				t.Fatal(verified, err)
			}
			op.ParentID = "other"
			if _, err := c.verifyFabricDeviceCreate(ctx, want, op); err == nil {
				t.Fatal("wrong operation accepted")
			}
		})
	}
}

func TestFabricDeviceConfigurationInventory(t *testing.T) {
	for _, mode := range []string{"paged", "duplicate-device", "wrong-fabric", "missing-border", "missing-l3", "missing-bool", "invalid-asn", "border-on-edge", "unknown-role", "duplicate-role", "late-error"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != "GET" || r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("fabricId") != "fabric" {
					t.Error("wrong inventory request")
				}
				rows := []fabricDeviceConfiguration{}
				if reads == 1 {
					for i := 0; i < 500; i++ {
						rows = append(rows, fabricDeviceConfiguration{ID: fmt.Sprintf("row-%d", i), NetworkDeviceID: fmt.Sprintf("device-%d", i), FabricID: "fabric", Roles: []string{"EDGE_NODE"}})
					}
				} else {
					if r.URL.Query().Get("offset") != "501" {
						t.Error("wrong pagination")
					}
					if mode == "late-error" {
						w.WriteHeader(500)
						return
					}
					border, _ := parseFabricBorderSettings(borderFixture())
					last := fabricDeviceConfiguration{ID: "last", NetworkDeviceID: "last-device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: border}
					switch mode {
					case "duplicate-device":
						last.NetworkDeviceID = "device-0"
					case "wrong-fabric":
						last.FabricID = "other"
					case "missing-border":
						last.Border = nil
					case "missing-l3":
						last.Border.Layer3 = nil
					case "missing-bool":
						last.Border.Layer3.DefaultExit = nil
					case "invalid-asn":
						last.Border.Layer3.ASN = "4294967296"
					case "border-on-edge":
						last.Roles = []string{"EDGE_NODE"}
					case "unknown-role":
						last.Roles = []string{"EMBEDDED_WIRELESS_CONTROLLER_NODE"}
					case "duplicate-role":
						last.Roles = []string{"BORDER_NODE", "BORDER_NODE"}
					}
					rows = append(rows, last)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.fabricDeviceConfigurations(context.Background(), "fabric")
			if mode == "paged" {
				if err != nil || len(rows) != 501 || reads != 2 {
					t.Fatal(len(rows), reads, err)
				}
			} else if err == nil || rows != nil {
				t.Fatal("partial inventory accepted", err)
			}
		})
	}
}
