// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"os"
	"testing"
)

func extraFixtures(t *testing.T) []Resource {
	t.Helper()
	b, e := os.ReadFile("../../../examples/configs/sdwan/system-features.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	return rs
}
func TestSimpleParcelFixtures(t *testing.T) {
	if len(extraFixtures(t)) != 12 {
		t.Fatal("expected two profiles and ten parcels")
	}
}
func TestLiveExtraNamesAbsent(t *testing.T) {
	c := liveClient(t)
	for _, r := range extraFixtures(t) {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o != nil {
			t.Fatal(r.Name, "must be absent", e)
		}
	}
}
func TestLiveExtraLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	for _, r := range extraFixtures(t) {
		o, e := c.Observe(ctx, r)
		if e != nil || o != nil {
			t.Fatal(r.Type, "must be absent", e)
		}
		id, e := c.Create(ctx, r)
		if e != nil {
			t.Fatal(r.Type, "create", e)
		}
		t.Cleanup(func() {
			if e := c.DeleteResourceFixture(ctx, r, id); e != nil {
				t.Error(r.Type, "cleanup", e)
			}
		})
		o, e = c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal(r.Type, "readback", e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Type, "create verification", e)
		}
		r.Body["description"] = "CVK updated isolated fixture"
		if e = c.Update(ctx, r, o); e != nil {
			t.Fatal(r.Type, "update", e)
		}
		o, e = c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal(r.Type, "updated readback", e)
		}
		same, e = Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Type, "update verification", e)
		}
	}
}
func TestLiveExtraOracle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_ORACLE") != "1" {
		t.Skip("requires upstream fixture")
	}
	c := liveClient(t)
	for _, r := range extraFixtures(t) {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o == nil {
			t.Fatal(r.Type, "readback", e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Type, "oracle mismatch", e)
		}
	}
}
