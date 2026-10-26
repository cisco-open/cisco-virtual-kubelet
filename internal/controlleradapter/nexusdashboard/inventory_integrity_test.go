// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nexusdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// fabricsReply answers the fabric list with one fabric per name.
func fabricsReply(names ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		list := make([]map[string]any, 0, len(names))
		for _, n := range names {
			list = append(list, map[string]any{"name": n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"fabrics": list, "meta": map[string]any{"counts": map[string]int{"remaining": 0, "total": len(list)}},
		})
	}
}

// switchesBy serves each fabric's switches endpoint from a handler per fabric.
func switchesBy(f *fakeND, handlers map[string]http.HandlerFunc) {
	f.mux.HandleFunc("/api/v1/manage/fabrics/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/manage/fabrics/"), "/switches")
		if h, ok := handlers[name]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

func raw(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

const oneSwitch = `{"serialNumber":"S1","switchId":"S1","model":"N9K-C9300","fabricManagementIp":"10.0.0.1","additionalData":{"discoveryStatus":"ok"}}`

func TestListInventoryRejectsIncompleteResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		fabrics http.HandlerFunc
		b       http.HandlerFunc // fabric "b"; fabric "a" always returns one switch
		wantErr string
	}{
		"empty page that still reports items remaining": {
			fabrics: fabricsReply("a", "b"),
			b: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get(pageOffsetParam) == "0" {
					_, _ = w.Write([]byte(`{"switches":[` + oneSwitch + `],"meta":{"counts":{"remaining":2,"total":3}}}`))
					return
				}
				_, _ = w.Write([]byte(`{"switches":[],"meta":{"counts":{"remaining":2,"total":3}}}`))
			},
			wantErr: "empty but ND reports 2 items remaining",
		},
		"switch list missing without a zero total": {
			fabrics: fabricsReply("a", "b"),
			b:       raw(`{"meta":{"counts":{"remaining":0}}}`),
			wantErr: `no "switches" list`,
		},
		"switch list null": {
			fabrics: fabricsReply("a", "b"),
			b:       raw(`{"switches":null}`),
			wantErr: `no "switches" list`,
		},
		"switch list missing with a nonzero total": {
			fabrics: fabricsReply("a", "b"),
			b:       raw(`{"meta":{"counts":{"remaining":0,"total":4}}}`),
			wantErr: `no "switches" list`,
		},
		"fabric list missing": {
			fabrics: raw(`{"meta":{"counts":{"remaining":0}}}`),
			b:       raw(`{"switches":[]}`),
			wantErr: `no "fabrics" list`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeND(t, false)
			f.setFabrics(tc.fabrics)
			switchesBy(f, map[string]http.HandlerFunc{
				"a": raw(`{"switches":[` + oneSwitch + `],"meta":{"counts":{"remaining":0,"total":1}}}`),
				"b": tc.b,
			})
			c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
			items, err := c.ListInventory(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v (items=%d), want it to contain %q", err, len(items), tc.wantErr)
			}
			if items != nil {
				t.Fatal("an incomplete response must not return a partial inventory")
			}
		})
	}
}

func TestListInventoryAcceptsGenuinelyEmptyCollections(t *testing.T) {
	for name, bBody := range map[string]string{
		"empty list":                     `{"switches":[],"meta":{"counts":{"remaining":0,"total":0}}}`,
		"empty list without meta":        `{"switches":[]}`,
		"missing list but ND says empty": `{"meta":{"counts":{"remaining":0,"total":0}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeND(t, false)
			f.setFabrics(fabricsReply("a", "b"))
			switchesBy(f, map[string]http.HandlerFunc{
				"a": raw(`{"switches":[` + oneSwitch + `],"meta":{"counts":{"remaining":0,"total":1}}}`),
				"b": raw(bBody),
			})
			c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
			items, err := c.ListInventory(context.Background())
			if err != nil || len(items) != 1 {
				t.Fatalf("items=%d err=%v, an empty fabric is not an error", len(items), err)
			}
		})
	}
}

// Regression: a refresh that ND answers with a truncated paging sequence must
// fail as a whole, so the reconciler never sees a partial snapshot and never
// marks valid devices missing or prunes them.
func TestTruncatedPagingNeverMarksOrPrunesDevices(t *testing.T) {
	var truncate atomic.Bool
	f := newFakeND(t, false)
	f.setFabrics(fabricsReply("a"))
	sw := func(serial, ip string) string {
		return `{"serialNumber":"` + serial + `","switchId":"` + serial + `","hostname":"h-` + serial + `","model":"N9K-C9300","fabricManagementIp":"` + ip + `","additionalData":{"discoveryStatus":"ok"}}`
	}
	switchesBy(f, map[string]http.HandlerFunc{"a": func(w http.ResponseWriter, r *http.Request) {
		off, _ := strconv.Atoi(r.URL.Query().Get(pageOffsetParam))
		switch {
		case !truncate.Load():
			_, _ = w.Write([]byte(`{"switches":[` + sw("S1", "10.0.0.1") + `,` + sw("S2", "10.0.0.2") + `],"meta":{"counts":{"remaining":0,"total":2}}}`))
		case off == 0:
			_, _ = w.Write([]byte(`{"switches":[` + sw("S1", "10.0.0.1") + `],"meta":{"counts":{"remaining":1,"total":2}}}`))
		default: // the second page comes back empty although ND says one is left
			_, _ = w.Write([]byte(`{"switches":[],"meta":{"counts":{"remaining":1,"total":2}}}`))
		}
	}})
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)

	a, _, _ := newTestAdapter(t, func(context.Context) error { return nil })
	a.list = c.ListInventory
	clk := &testClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	a.now = clk.now
	spec := adoptionSpec()
	spec.Removal = &ciskov1.NetworkControllerDeviceRemoval{
		Policy: ciskov1.DeviceRemovalPrune, GracePeriod: &metav1.Duration{Duration: time.Hour},
	}
	a.adoption = spec
	a.devices = &deviceSyncer{client: a.statusWriter, namespace: "ns", uid: testUID, adoption: spec, now: clk.now}
	ctx := context.Background()

	a.syncInventory(ctx) // complete: both switches adopted
	if !deviceExists(t, a.statusWriter, "nd-s1") || !deviceExists(t, a.statusWriter, "nd-s2") {
		t.Fatal("setup: both devices should exist")
	}

	truncate.Store(true)
	for i := 0; i < 3; i++ { // well past the grace period, repeatedly
		clk.advance(2 * time.Hour)
		a.syncInventory(ctx)
	}
	for _, name := range []string{"nd-s1", "nd-s2"} {
		if !deviceExists(t, a.statusWriter, name) {
			t.Fatalf("%s was pruned from a partial snapshot", name)
		}
		if got := getDevice(t, a.statusWriter, name).Annotations[annotationMissingSince]; got != "" {
			t.Fatalf("%s was marked missing from a partial snapshot (%q)", name, got)
		}
	}
	if items, _ := a.inventory.Snapshot(); len(items) != 2 {
		t.Fatalf("the last complete snapshot must be kept, got %d items", len(items))
	}
}
