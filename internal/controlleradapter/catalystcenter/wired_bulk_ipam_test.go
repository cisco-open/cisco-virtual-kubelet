// Copyright © 2026 Cisco Systems Inc.
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

type wiredBulkIPAMFixture struct {
	pools        []ipPool
	reservations []ipReservation
	sites        []Site
	reads        int
	beforeRead   func()
}

func installWiredBulkIPAMFixture(t *testing.T, f *wiredBulkFixture) *wiredBulkIPAMFixture {
	t.Helper()
	ipam := &wiredBulkIPAMFixture{pools: fixturePools(), reservations: fixtureReservations(), sites: sampleSites()}
	f.networkReply = func(w http.ResponseWriter, r *http.Request) bool {
		if ipam.reply(t, w, r) {
			return true
		}
		if !strings.HasSuffix(r.URL.Path, "/ntpSettings") {
			return false
		}
		if r.Method != "GET" {
			t.Error("IPAM qualification attempted settings mutation")
			w.WriteHeader(403)
			return true
		}
		if strings.Contains(r.URL.Path, "/root/") {
			fmt.Fprint(w, `{"response":{"ntp":{"servers":["192.0.2.1"]}}}`)
		} else {
			fmt.Fprint(w, `{"response":{"ntp":null}}`)
		}
		return true
	}
	return ipam
}

func (f *wiredBulkIPAMFixture) reply(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.URL.Path != globalPoolsPath && r.URL.Path != sitePoolsPath {
		return false
	}
	if r.Method != "GET" {
		t.Error("dependency observation attempted a mutation")
		w.WriteHeader(403)
		return true
	}
	f.reads++
	if f.beforeRead != nil {
		f.beforeRead()
	}
	var rows any = f.pools
	if r.URL.Path == sitePoolsPath {
		rows = f.reservations
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
		t.Error(err)
	}
	return true
}

func TestWiredBulkIPAMIdentitySnapshot(t *testing.T) {
	for _, mode := range []string{"matched", "missing-pool", "missing-reservation", "pool-id", "reservation-id", "changed-pool", "changed-gateway", "wrong-parent", "wrong-site", "duplicate-name", "changed-during-observation", "empty-observer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := &wiredBulkIPAMFixture{pools: fixturePools(), reservations: fixtureReservations(), sites: sampleSites()}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if f.reply(t, w, r) {
					return
				}
				if r.Method == "GET" && r.URL.Path == sitesPath {
					if err := json.NewEncoder(w).Encode(map[string]any{"response": f.sites}); err != nil {
						t.Error(err)
					}
					return
				}
				t.Error("unexpected dependency request")
				w.WriteHeader(403)
			})
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			before, err := c.observeWiredBulkIPAM(ctx, intent)
			if err != nil {
				t.Fatal("valid typed dependencies unavailable", err)
			}
			digest, err := before.digest()
			if err != nil || len(digest) != 64 {
				t.Fatal("missing typed dependency identity", err)
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, before), "192.0.2.") {
					t.Fatal("snapshot formatter leaked policy")
				}
			}
			switch mode {
			case "missing-pool":
				f.pools = []ipPool{}
			case "missing-reservation":
				f.reservations = []ipReservation{}
			case "pool-id":
				f.pools[0].ID = "replacement-pool"
				f.reservations[0].V4.GlobalPoolID = "replacement-pool"
			case "reservation-id":
				f.reservations[0].ID = "replacement-reservation"
			case "changed-pool":
				f.pools[0].Space.Subnet = "198.51.100.0"
			case "changed-gateway":
				value := "192.0.2.130"
				f.reservations[0].V4.Gateway = &value
			case "wrong-parent":
				f.reservations[0].V4.GlobalPoolID = "missing-parent"
			case "wrong-site":
				f.reservations[0].SiteID = "other-site"
			case "duplicate-name":
				other := f.reservations[0]
				other.ID = "other"
				other.SiteID = "other-site"
				f.reservations = append(f.reservations, other)
			}
			if mode == "changed-during-observation" {
				doc := wiredBulkDependencyDocument(t)
				raw, _ := json.Marshal(doc)
				observers, err := c.wiredBulkDependencies(&configSourceSnapshot{raw: raw})
				if err != nil || len(observers) != 1 {
					t.Fatal(err)
				}
				start := f.reads
				f.beforeRead = func() {
					if f.reads == start+3 {
						f.reservations[0].ID = "replaced-during-collection"
					}
				}
				if _, err := observers[0](ctx); !errors.Is(err, errConfigurationStale) {
					t.Fatal("hybrid dependency observation accepted", err)
				}
				return
			}
			if mode == "empty-observer" {
				raw := []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab","state":"PROVISION"}]}}}`)
				observers, err := c.wiredBulkDependencies(&configSourceSnapshot{raw: raw})
				if err != nil || len(observers) != 0 {
					t.Fatal("legacy model acquired an allocation dependency", err)
				}
				return
			}
			after, err := c.observeWiredBulkIPAM(ctx, intent)
			valid := mode == "matched" || mode == "pool-id" || mode == "reservation-id"
			if (err == nil) != valid {
				t.Fatal("invalid dependency graph accepted", err)
			}
			if !valid {
				return
			}
			current, err := after.digest()
			if err != nil {
				t.Fatal(err)
			}
			if (digest == current) != (mode == "matched") {
				t.Fatal("same-name native replacement reused original identity")
			}
		})
	}
}
