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
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// registerInventory serves fabrics and per-fabric switches with real paging
// semantics (max/offset + meta.counts.remaining) and records page requests.
func registerInventory(f *fakeND, fabrics map[string][]map[string]any, pages *[]string) {
	page := func(w http.ResponseWriter, r *http.Request, key string, all []map[string]any) {
		if ck, err := r.Cookie(authCookie); err != nil || ck.Value != f.token.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		max, _ := strconv.Atoi(r.URL.Query().Get(pageSizeParam))
		off, _ := strconv.Atoi(r.URL.Query().Get(pageOffsetParam))
		if pages != nil {
			*pages = append(*pages, r.URL.Path+"?offset="+strconv.Itoa(off))
		}
		end := off + max
		if end > len(all) {
			end = len(all)
		}
		if off > len(all) {
			off = len(all)
		}
		items := all[off:end]
		if items == nil {
			items = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			key:    items,
			"meta": map[string]any{"counts": map[string]int{"remaining": len(all) - end, "total": len(all)}},
		})
	}
	f.setFabrics(func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		for name := range fabrics {
			list = append(list, map[string]any{"name": name})
		}
		page(w, r, "fabrics", list)
	})
	f.mux.HandleFunc("/api/v1/manage/fabric/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/manage/fabric/"), "/switches")
		sw, ok := fabrics[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if sw == nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		page(w, r, "switches", sw)
	})
}

func sw(serial, model, ip, status string) map[string]any {
	return map[string]any{
		"switchId": serial, "hostname": "h-" + serial, "model": model, "fabricManagementIp": ip,
		"softwareVersion": "10.4(2)", "additionalSwitchData": map[string]any{"discoveryStatus": status},
	}
}

func TestToInventoryItemFromSwaggerExample(t *testing.T) {
	raw, err := os.ReadFile("testdata/switches_page.json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Switches []ndSwitch `json:"switches"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Switches) != 1 {
		t.Fatalf("decode: %v", err)
	}
	got := toInventoryItem(env.Switches[0], "fab1")
	want := InventoryItem{
		Serial: "SAL1948TRHH", Hostname: "nx-leaf1", MgmtAddress: "10.23.244.72", Model: "N9K-C93180YC-FX3",
		Platform: PlatformNXOS, Fabric: "fab1", FabricType: "VXLAN", SoftwareVersion: "10.4(2)", Reachable: true,
	}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestToInventoryItemSkips(t *testing.T) {
	cases := map[string]ndSwitch{
		"non-NXOS":      {SwitchID: "S1", Model: "C9300-48P", FabricManagementIP: "10.0.0.1"},
		"no serial":     {Model: "N9K-C9300", FabricManagementIP: "10.0.0.1"},
		"no mgmt addr":  {SwitchID: "S1", Model: "N9K-C9300"},
		"unknown model": {SwitchID: "S1", FabricManagementIP: "10.0.0.1"},
	}
	for name, in := range cases {
		if got := toInventoryItem(in, "f"); got.SkipReason == "" {
			t.Errorf("%s: expected a skip reason, got %+v", name, got)
		}
	}
	oob := ndSwitch{SwitchID: "S1", Model: "N9K-X"}
	oob.Telemetry.OutOfBandIPv4 = "10.9.9.9"
	if got := toInventoryItem(oob, "f"); got.MgmtAddress != "10.9.9.9" || got.SkipReason != "" {
		t.Fatalf("OOB fallback failed: %+v", got)
	}
}

func TestListInventoryPaginatesAcrossFabrics(t *testing.T) {
	f := newFakeND(t, false)
	var many []map[string]any
	for i := 0; i < 250; i++ { // forces 3 pages at pageSize=100
		many = append(many, sw(fmt.Sprintf("SER%03d", i), "N9K-C9300", fmt.Sprintf("10.1.%d.%d", i/250, i%250+1), "ok"))
	}
	var pages []string
	registerInventory(f, map[string][]map[string]any{
		"big":   many,
		"small": {sw("X1", "C9300-24", "10.2.0.1", "ok"), sw("X2", "N9K-C9300", "10.2.0.2", "unreachable")},
		"empty": {},
	}, &pages)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)

	items, err := c.ListInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 252 {
		t.Fatalf("items = %d, want 252", len(items))
	}
	bigPages := 0
	for _, p := range pages {
		if strings.Contains(p, "/fabric/big/") {
			bigPages++
		}
	}
	if bigPages != 3 {
		t.Fatalf("big fabric pages = %d, want 3 (%v)", bigPages, pages)
	}
	byKey := map[string]InventoryItem{}
	for _, it := range items {
		byKey[it.Serial] = it
	}
	if byKey["X1"].SkipReason == "" || byKey["X2"].Reachable || byKey["X2"].SkipReason != "" {
		t.Fatalf("unexpected mapping: %+v %+v", byKey["X1"], byKey["X2"])
	}
	if got := summarize(items); got != "252 switches in 2 fabrics; 251 adoptable NX-OS, 1 skipped" {
		t.Fatalf("summary = %q", got)
	}
}

func TestListInventoryEmptyND(t *testing.T) {
	f := newFakeND(t, false)
	registerInventory(f, map[string][]map[string]any{}, nil)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	items, err := c.ListInventory(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestListInventoryFabricFailureFailsWholeCall(t *testing.T) {
	f := newFakeND(t, false)
	registerInventory(f, map[string][]map[string]any{
		"ok":  {sw("A1", "N9K-C9300", "10.0.0.1", "ok")},
		"bad": nil, // handler returns 500
	}, nil)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	if items, err := c.ListInventory(context.Background()); err == nil || items != nil {
		t.Fatalf("partial result must not be returned: items=%v err=%v", items, err)
	}
}

func TestPagingThatNeverDrainsIsBounded(t *testing.T) {
	f := newFakeND(t, false)
	f.setFabrics(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"fabrics":[{"name":"x"}],"meta":{"counts":{"remaining":5,"total":9999}}}`))
	})
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	if _, err := c.ListInventory(context.Background()); err == nil || !strings.Contains(err.Error(), "did not terminate") {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidFabricNameNeverReachesURL(t *testing.T) {
	f := newFakeND(t, false)
	var hit bool
	f.setFabrics(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"fabrics":[{"name":"../../infra"},{"name":"a b"}],"meta":{"counts":{"remaining":0,"total":2}}}`))
	})
	f.mux.HandleFunc("/api/v1/manage/fabric/", func(http.ResponseWriter, *http.Request) { hit = true })
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	items, err := c.ListInventory(context.Background())
	if err != nil || len(items) != 0 || hit {
		t.Fatalf("items=%v err=%v hit=%v", items, err, hit)
	}
}

func TestSyncInventoryKeepsLastGoodAndReportsCounts(t *testing.T) {
	var fail bool
	a, _, get := newTestAdapter(t, func(context.Context) error { return nil })
	a.list = func(context.Context) ([]InventoryItem, error) {
		if fail {
			return nil, fmt.Errorf("list fabrics: connection refused")
		}
		return []InventoryItem{
			{Serial: "S1", Fabric: "f1", Platform: PlatformNXOS},
			{Serial: "S2", Fabric: "f1", SkipReason: "platform other"},
		}, nil
	}
	a.syncInventory(context.Background())
	cap := get().Status.Capabilities
	if len(cap) != 1 || cap[0].Name != CapabilityInventory || !cap[0].Supported ||
		cap[0].Message != "2 switches in 1 fabrics; 1 adoptable NX-OS, 1 skipped" {
		t.Fatalf("capabilities = %+v", cap)
	}
	if strings.Contains(cap[0].Message, "S1") {
		t.Fatal("serials must not appear in status")
	}

	fail = true
	a.syncInventory(context.Background())
	cap = get().Status.Capabilities
	if cap[0].Supported || !strings.Contains(cap[0].Message, "keeping last good snapshot of 2 switches") {
		t.Fatalf("capabilities after failure = %+v", cap)
	}
	if items, at := a.inventory.Snapshot(); len(items) != 2 || at.IsZero() {
		t.Fatal("last good snapshot lost")
	}
}

func TestJitterBounds(t *testing.T) {
	d := 5 * time.Minute
	for i := 0; i < 1000; i++ {
		if j := jittered(d); j < time.Duration(0.9*float64(d)) || j > time.Duration(1.1*float64(d)) {
			t.Fatalf("jitter out of bounds: %v", j)
		}
	}
}
