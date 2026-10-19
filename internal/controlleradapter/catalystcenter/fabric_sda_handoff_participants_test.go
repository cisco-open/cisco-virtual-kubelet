// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestFabricSDAParticipants(t *testing.T) {
	for _, mode := range []string{"valid", "missing-border", "failed-scan", "changed-peer", "changed-link", "missing-local"} {
		t.Run(mode, func(t *testing.T) {
			flag := true
			changed := false
			border := &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}
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
				var rows any
				switch r.URL.Path {
				case fabricSitesPath:
					sites := []fabricSite{}
					for _, id := range []string{"local", "remote", "unrelated"} {
						if id == "local" && mode == "missing-local" {
							continue
						}
						sites = append(sites, fabricSite{ID: id, SiteID: "site-" + id, Authentication: "Closed Authentication", PubSub: &flag})
					}
					rows = sites
				case fabricSDAHandoffsPath:
					site := r.URL.Query().Get("fabricId")
					if r.URL.Query().Get("networkDeviceId") != "" {
						t.Error("peer discovery filtered devices")
					}
					if site == "unrelated" && mode == "failed-scan" {
						w.WriteHeader(400)
						return
					}
					handoffs := []fabricSDAHandoff{}
					if site == "remote" {
						transit := "transit"
						if changed && mode == "changed-link" {
							transit = "other"
						}
						handoffs = append(handoffs, fabricSDAHandoff{DeviceID: "remote-border", FabricID: site, TransitID: transit})
					}
					rows = handoffs
				case fabricDevicesPath:
					site := r.URL.Query().Get("fabricId")
					if site == "unrelated" {
						t.Error("unrelated membership requested")
					}
					members := []fabricDeviceConfiguration{{ID: site + "-member", NetworkDeviceID: site + "-border", FabricID: site, Roles: []string{"BORDER_NODE"}, Border: border}}
					if site == "remote" {
						if mode == "missing-border" {
							members = nil
						}
						id := "remote-edge"
						if changed && mode == "changed-peer" {
							id = "replacement-edge"
						}
						members = append(members, fabricDeviceConfiguration{ID: id + "-member", NetworkDeviceID: id, FabricID: site, Roles: []string{"EDGE_NODE"}})
					}
					rows = members
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			ids, digest, err := c.fabricSDAParticipants(context.Background(), "local", "transit")
			if mode == "missing-border" || mode == "failed-scan" || mode == "missing-local" {
				if err == nil || ids != nil || digest != "" {
					t.Fatal("partial participants escaped")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(ids, []string{"local-border", "remote-border", "remote-edge"}) {
				t.Fatal("incomplete participants", ids, err)
			}
			changed = true
			_, next, err := c.fabricSDAParticipants(context.Background(), "local", "transit")
			if err != nil || (digest == next) != (mode == "valid") {
				t.Fatal("peer change not detected", err)
			}
		})
	}
}
