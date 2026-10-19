// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestAnycastInventoryBatchReadBound(t *testing.T) {
	ctx := context.Background()
	calls := map[string]int{}
	b := false
	reservations := fixtureReservations()
	bits := 25
	gateway := "192.0.2.1"
	reservations = append(reservations, ipReservation{ID: "extra", Name: "extra", Type: "LAN", SiteID: "area", V4: &poolAddressSpace{Subnet: "192.0.2.0", PrefixLength: &bits, Gateway: &gateway, GlobalPoolID: "pool"}})
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("unexpected mutation")
			w.WriteHeader(400)
			return
		}
		var response any
		switch r.URL.Path {
		case sitesPath:
			response = sampleSites()
		case fabricSitesPath:
			response = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &b}}
		case fabricL3NetworksPath:
			response = []fabricL3Network{{ID: "network", Name: "Corp", Fabrics: []string{"fabric"}}}
		case globalPoolsPath:
			response = fixturePools()
		case sitePoolsPath:
			response = reservations
		case fabricL2NetworksPath:
			response = []fabricL2Network{}
		case anycastGatewaysPath:
			response = []any{}
		default:
			t.Error("unexpected API", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": response})
	})
	desired := []fabricAnycastIntent{}
	for _, pool := range []string{"access", "extra"} {
		intent, err := parseAnycastGateway(map[string]any{"ip_pool_name": pool, "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true})
		if err != nil {
			t.Fatal(err)
		}
		desired = append(desired, fabricAnycastIntent{path: "Global/Lab", anycastGatewayIntent: intent})
	}
	snapshot, err := c.loadAnycastGatewayInventory(ctx, desired)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]int{authPath: 1, sitesPath: 2, fabricSitesPath: 2, fabricL3NetworksPath: 1, globalPoolsPath: 1, sitePoolsPath: 1, anycastGatewaysPath: 1, fabricL2NetworksPath: 1}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatal("inventory fetched per target", calls)
	}
	digests := []string{}
	for _, want := range desired {
		deps, err := snapshot.resolve(want)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, deps.digest)
	}
	for i := 0; i < 100; i++ {
		for _, want := range desired {
			if _, err := snapshot.resolve(want); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatal("local resolution reread inventory", calls)
	}
	if digests[0] == digests[1] {
		t.Fatal("distinct reservations share dependency identity")
	}
	reservations[0].ID = "replacement"
	fresh, err := c.anycastGatewayDependencies(ctx, desired[0])
	if err != nil {
		t.Fatal(err)
	}
	if fresh.digest == digests[0] || calls[sitePoolsPath] != 2 {
		t.Fatal("fresh submission/readback path reused old evidence")
	}
	old, err := snapshot.resolve(desired[0])
	if err != nil || old.digest != digests[0] {
		t.Fatal("external response mutation changed prior snapshot")
	}
}
