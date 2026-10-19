// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const extranetModel = `{"catalyst_center":{"fabric":{"extranet_policies":[{"name":"Shared","provider_virtual_network":"Services","subscriber_virtual_networks":["Corp","Guest"],"fabric_sites":["Global/Lab"]}]}}}`

func TestFabricExtranetReport(t *testing.T) {
	for _, mode := range []string{"matching", "missing", "subscribers", "provider", "scope", "zone", "unscoped", "missing-vn", "missing-scope", "replaced-vn", "replaced-fabric", "duplicate", "malformed", "ambiguous-scope"} {
		t.Run(mode, func(t *testing.T) {
			calls, reads := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					t.Errorf("unexpected mutation: %s", r.Method)
					w.WriteHeader(500)
					return
				}
				var response any
				switch r.URL.Path {
				case fabricL3NetworksPath:
					reads++
					rows := []fabricL3Network{{ID: "services", Name: "Services"}, {ID: "corp", Name: "Corp"}, {ID: "guest", Name: "Guest"}}
					if mode == "missing-vn" {
						rows = rows[:2]
					}
					if mode == "replaced-vn" && reads > 1 {
						rows[0].ID = "replacement"
					}
					response = rows
				case sitesPath:
					response = sampleSites()
				case fabricSitesPath:
					yes := true
					rows := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &yes}}
					if mode == "missing-scope" || mode == "zone" {
						rows = []fabricSite{}
					}
					if mode == "replaced-fabric" && reads > 1 {
						rows[0].ID = "replacement"
					}
					response = rows
				case fabricZonesPath:
					rows := []fabricZone{}
					if mode == "zone" || mode == "ambiguous-scope" {
						rows = append(rows, fabricZone{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication"})
					}
					response = rows
				case fabricExtranetPath:
					p := fabricExtranetPolicy{ID: "policy", Name: "Shared", Provider: "Services", Subscribers: []string{"Guest", "Corp"}, Fabrics: []string{"fabric"}}
					switch mode {
					case "provider":
						p.Provider = "Other"
					case "subscribers":
						p.Subscribers = []string{"Corp"}
					case "scope", "unscoped":
						p.Fabrics = []string{"other"}
					case "malformed":
						p.Subscribers = nil
					}
					rows := []fabricExtranetPolicy{p}
					if mode == "missing" {
						rows = []fabricExtranetPolicy{}
					}
					if mode == "duplicate" {
						p.ID = "duplicate"
						rows = append(rows, p)
					}
					response = rows
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			raw := extranetModel
			if mode == "unscoped" {
				raw = strings.Replace(raw, `,"fabric_sites":["Global/Lab"]`, "", 1)
			}
			plan, err := c.compileFabricConfiguration([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if plan.count() != 1 {
				t.Fatal(plan.count())
			}
			if write, err := plan.next(context.Background()); write != nil || !errors.Is(err, errConfigurationDeviceAdmission) || calls != 0 {
				t.Fatal("ungated", write, err, calls)
			}
			if ok, err := plan.verify(context.Background(), nil); ok || !errors.Is(err, errConfigurationDeviceAdmission) || calls != 0 {
				t.Fatal("ungated verification", ok, err, calls)
			}
			drift, err := plan.report(context.Background())
			switch mode {
			case "missing-vn", "missing-scope":
				if !errors.Is(err, errConfigurationBusy) || len(drift) != 0 {
					t.Fatal(drift, err)
				}
			case "replaced-vn", "replaced-fabric":
				if !errors.Is(err, errConfigurationStale) || len(drift) != 0 {
					t.Fatal(drift, err)
				}
			case "duplicate", "malformed", "ambiguous-scope":
				if err == nil || len(drift) != 0 {
					t.Fatal(drift, err)
				}
			case "matching", "zone", "unscoped":
				if err != nil || len(drift) != 0 {
					t.Fatal(drift, err)
				}
			default:
				expected := "Update"
				if mode == "missing" {
					expected = "Add"
				}
				if err != nil || len(drift) != 1 || string(drift[0].Change) != expected || drift[0].Resource != "extranet-policy:Shared" {
					t.Fatal(drift, err)
				}
			}
		})
	}
}
func TestFabricExtranetModelValidation(t *testing.T) {
	for _, edit := range [][2]string{
		{`"name":"Shared"`, `"name":""`},
		{`"name":"Shared"`, `"name":"Shared","unknown":true`},
		{`"subscriber_virtual_networks":["Corp","Guest"]`, `"subscriber_virtual_networks":[]`},
		{`"subscriber_virtual_networks":["Corp","Guest"]`, `"subscriber_virtual_networks":["Services"]`},
		{`"subscriber_virtual_networks":["Corp","Guest"]`, `"subscriber_virtual_networks":["Corp","Corp"]`},
		{`"fabric_sites":["Global/Lab"]`, `"fabric_sites":["fabric-id"]`},
		{`"fabric_sites":["Global/Lab"]`, `"fabric_sites":["Global/Lab","Global/Lab"]`},
		{`"fabric_sites":["Global/Lab"]`, `"fabric_sites":["Global"]`},
	} {
		raw := strings.Replace(extranetModel, edit[0], edit[1], 1)
		if _, err := (&client{}).compileFabricConfiguration([]byte(raw)); err == nil {
			t.Fatal("accepted invalid model", raw)
		}
	}
}
func TestFabricExtranetCompositionGate(t *testing.T) {
	// Even a missing prerequisite definition must not be created ahead of the
	// final policy's device admission gate.
	raw := strings.Replace(extranetModel, `"fabric":{`, `"fabric":{"l3_virtual_networks":[{"name":"Services"}],`, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected network call before gate")
		w.WriteHeader(500)
	})
	plan, err := c.compileFabricConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if plan.count() != 2 {
		t.Fatal(plan.count())
	}
	if write, err := plan.next(context.Background()); write != nil || !errors.Is(err, errConfigurationDeviceAdmission) {
		t.Fatal(write, err)
	}
	// Exercise combined IPAM/fabric wrapper, which must gate before IPAM planning.
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal([]byte(poolModel), &base); err != nil {
		t.Fatal(err)
	}
	base["catalyst_center"].(map[string]any)["fabric"] = root["catalyst_center"].(map[string]any)["fabric"]
	encoded, _ := json.Marshal(base)
	combined, err := c.compileFabricIPAMApply(encoded, []string{"network_settings", "fabric"})
	if err != nil {
		t.Fatal(err)
	}
	if write, err := combined.executor.next(context.Background()); write != nil || !errors.Is(err, errConfigurationDeviceAdmission) {
		t.Fatal(write, err)
	}
}

func TestFabricExtranetPagination(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			pages := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != fabricExtranetPath {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				pages++
				rows := []fabricExtranetPolicy{}
				switch r.URL.Query().Get("offset") {
				case "1":
					for i := 0; i < 500; i++ {
						rows = append(rows, fabricExtranetPolicy{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("Policy%d", i), Provider: "Services", Subscribers: []string{"Corp"}})
					}
				case "501":
					id := "last"
					if broken {
						id = "id-0"
					}
					rows = append(rows, fabricExtranetPolicy{ID: id, Name: "Last", Provider: "Services", Subscribers: []string{"Corp"}})
				default:
					t.Fatal("unexpected pagination offset", r.URL.Query())
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			policies, err := c.fabricExtranets(context.Background())
			if pages != 2 {
				t.Fatal("incomplete paging", pages)
			}
			if broken {
				if err == nil || policies != nil {
					t.Fatal("duplicate identity accepted", err)
				}
				return
			}
			if err != nil || len(policies) != 501 || policies["Last"].ID != "last" {
				t.Fatal("incomplete policies", len(policies), err)
			}
		})
	}
}
