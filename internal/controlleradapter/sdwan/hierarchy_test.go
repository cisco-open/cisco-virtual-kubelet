// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"os"
	"testing"
)

func hierarchyFixtures(t *testing.T) []Resource {
	t.Helper()
	b, e := os.ReadFile("../../../examples/configs/sdwan/network-hierarchy.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	return rs
}
func TestHierarchyFixtures(t *testing.T) {
	rs := hierarchyFixtures(t)
	if len(rs) != 7 {
		t.Fatal(len(rs))
	}
	seen := map[string]bool{"Global": true}
	for _, r := range rs {
		if !seen[r.ProfileRefs["parent"]] {
			t.Fatal("invalid dependency order")
		}
		seen[r.Name] = true
	}
}
func TestLiveHierarchyNamesAbsent(t *testing.T) {
	c := liveClient(t)
	for _, r := range hierarchyFixtures(t) {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o != nil {
			t.Fatal("hierarchy fixture must be absent", e)
		}
	}
}
func createHierarchyFixtures(t *testing.T, c *Client) {
	t.Helper()
	ctx := context.Background()
	rs := hierarchyFixtures(t)
	for _, r := range rs {
		o, e := c.Observe(ctx, r)
		if e != nil || o != nil {
			t.Fatal("hierarchy fixture must be absent", e)
		}
		id, e := c.Create(ctx, r)
		if e != nil {
			t.Fatal(r.Name, "create", e)
		}
		t.Cleanup(func() {
			if e := c.DeleteResourceFixture(context.Background(), r, id); e != nil {
				t.Error(r.Name, "cleanup", e)
			}
		})
		o, e = c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal(r.Name, "readback", e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Name, "verification", e)
		}
	}
}
func TestLiveHierarchyLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	createHierarchyFixtures(t, c)
}
func TestLiveHierarchyOracle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_ORACLE") != "1" {
		t.Skip("requires upstream fixture")
	}
	c := liveClient(t)
	for _, r := range hierarchyFixtures(t) {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o == nil {
			t.Fatal(r.Name, "readback", e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Name, "oracle mismatch", e)
		}
	}
}
