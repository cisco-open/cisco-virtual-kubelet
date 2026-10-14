// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("CVK_SDWAN_LIVE") != "1" {
		t.Skip("set CVK_SDWAN_LIVE=1 for explicit lab qualification")
	}
	endpoint := os.Getenv("SDWAN_URL")
	user := os.Getenv("SDWAN_USERNAME")
	pass := os.Getenv("SDWAN_PASSWORD")
	if endpoint == "" || user == "" || pass == "" {
		t.Fatal("lab credentials required")
	}
	dir := t.TempDir()
	if os.WriteFile(filepath.Join(dir, "username"), []byte(user), 0600) != nil || os.WriteFile(filepath.Join(dir, "password"), []byte(pass), 0600) != nil {
		t.Fatal("credential projection")
	}
	c := &Client{Endpoint: endpoint, CredentialPath: dir, InsecureSkipVerify: os.Getenv("SDWAN_INSECURE") == "true", MaxSessionLifetime: time.Minute}
	t.Cleanup(c.Invalidate)
	return c
}
func liveResource(t *testing.T) Resource {
	t.Helper()
	b, e := os.ReadFile("../../../examples/configs/sdwan/banner.yaml")
	if e != nil {
		t.Fatal(e)
	}
	r, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	return r[0]
}
func TestLiveSDWANDiscovery(t *testing.T) {
	c := liveClient(t)
	r, e := c.Discover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("Manager=%s inventory=%d", r.Version, r.Count)
}
func TestLiveBannerOracle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_ORACLE") != "1" {
		t.Skip("requires upstream-deployed fixture")
	}
	c := liveClient(t)
	r := liveResource(t)
	o, e := c.Observe(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if o == nil {
		t.Fatal("oracle resource missing")
	}
	same, e := Equal(r, o.Body)
	if e != nil || !same {
		t.Fatal("native mapping differs from upstream deployed state", e)
	}
	t.Log("native mapping matches upstream module/provider live readback")
}
func TestLiveBannerLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires explicit fixture mutation opt-in")
	}
	c := liveClient(t)
	r := liveResource(t)
	ctx := context.Background()
	o, e := c.Observe(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	if o != nil {
		t.Fatal("fixture name already exists; refusing adoption")
	}
	id, e := c.Create(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := c.DeleteFixture(context.Background(), id); e != nil {
			t.Errorf("fixture cleanup: %v", e)
		}
	})
	o, e = c.Observe(ctx, r)
	if e != nil || o == nil {
		t.Fatal("readback", e)
	}
	same, e := Equal(r, o.Body)
	if e != nil || !same {
		t.Fatal("create verification", e)
	}
	// Re-observation must compare equal without issuing a second POST.
	again, e := c.Observe(ctx, r)
	if e != nil || again == nil || again.ID != id {
		t.Fatal("idempotent observation", e)
	}
	r.Body["templateDescription"] = "CVK fixture update qualification"
	if e = c.Update(ctx, r, o); e != nil {
		t.Fatal(e)
	}
	o, e = c.Observe(ctx, r)
	if e != nil || o == nil {
		t.Fatal("update readback", e)
	}
	same, e = Equal(r, o.Body)
	if e != nil || !same {
		t.Fatal("update verification", e)
	}
	t.Log("native create/read/update/read verified; cleanup registered")
}

func TestLiveProfileShape(t *testing.T) {
	if os.Getenv("CVK_SDWAN_PROFILE_DISCOVERY") != "1" {
		t.Skip("profile API discovery")
	}
	c := liveClient(t)
	var raw any
	if e := c.Request(context.Background(), "GET", "/dataservice/v1/feature-profile/sdwan/system", nil, &raw); e != nil {
		t.Fatal(e)
	}
	switch v := raw.(type) {
	case []any:
		t.Logf("profile list: array (%d entries)", len(v))
		if len(v) > 0 {
			m := v[0].(map[string]any)
			id := m["profileId"].(string)
			var parcels any
			if e := c.Request(context.Background(), "GET", "/dataservice/v1/feature-profile/sdwan/system/"+id+"/banner", nil, &parcels); e != nil {
				t.Fatal(e)
			}
			switch a := parcels.(type) {
			case []any:
				t.Logf("banner list array count=%d", len(a))
				if len(a) > 0 {
					for k := range a[0].(map[string]any) {
						t.Log("banner field", k)
					}
				}
			case map[string]any:
				for k := range a {
					t.Log("banner list key", k)
				}
			}
		}

		if len(v) > 0 {
			if m, ok := v[0].(map[string]any); ok {
				for k := range m {
					t.Log("profile field", k)
				}
			}
		}
	case map[string]any:
		for k := range v {
			t.Log("profile list field", k)
		}
	default:
		t.Fatal("unexpected profile shape")
	}
}

func TestLiveSystemProfileLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires explicit fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	b, e := os.ReadFile("../../../examples/configs/sdwan/system-profile.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range rs {
		o, e := c.Observe(ctx, r)
		if e != nil {
			t.Fatal(e)
		}
		if o != nil {
			t.Fatal("fixture name already exists")
		}
	}
	for _, r := range rs {
		id, e := c.Create(ctx, r)
		if e != nil {
			t.Fatal("create", r.Kind, e)
		}
		t.Cleanup(func() {
			if e := c.DeleteResourceFixture(context.Background(), r, id); e != nil {
				t.Error("cleanup", r.Kind, e)
			}
		})
		o, e := c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal("readback", r.Kind, e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal("readback mismatch", r.Kind, e)
		}
	}
	t.Log("native profile and dependent banner parcel verified; reverse-order cleanup registered")
}

func TestLiveSystemProfileOracle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_ORACLE") != "1" {
		t.Skip("requires upstream-deployed fixture")
	}
	c := liveClient(t)
	b, e := os.ReadFile("../../../examples/configs/sdwan/system-profile.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range rs {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o == nil {
			t.Fatal("oracle observation", r.Kind, e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal("native mapping differs from upstream", r.Kind, e)
		}
	}
	t.Log("native system profile and banner parcel match upstream live deployment")
}

func TestLiveFixtureNamesAbsent(t *testing.T) {
	c := liveClient(t)
	for _, file := range []string{"banner.yaml", "system-profile.yaml"} {
		b, e := os.ReadFile("../../../examples/configs/sdwan/" + file)
		if e != nil {
			t.Fatal(e)
		}
		rs, e := Compile(b)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range rs {
			o, e := c.Observe(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			if o != nil {
				t.Fatal("fixture resource already exists; refusing overwrite")
			}
		}
	}
}
