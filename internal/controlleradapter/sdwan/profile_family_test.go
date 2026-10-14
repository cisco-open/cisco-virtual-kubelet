// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"os"
	"testing"
)

func familyFixtures(t *testing.T) []Resource {
	t.Helper()
	b, e := os.ReadFile("../../../examples/configs/sdwan/feature-profiles.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(b)
	if e != nil {
		t.Fatal(e)
	}
	return rs
}
func TestProfileFamilyFixtures(t *testing.T) {
	if len(familyFixtures(t)) != 9 {
		t.Fatal("profile coverage")
	}
}
func TestLiveFamilyNamesAbsent(t *testing.T) {
	c := liveClient(t)
	for _, r := range familyFixtures(t) {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o != nil {
			t.Fatal(r.Name, "must be absent", e)
		}
	}
}
func TestLiveFamilyLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	for _, r := range familyFixtures(t) {
		o, e := c.Observe(ctx, r)
		if e != nil || o != nil {
			t.Fatal(r.Name, "must be absent", e)
		}
		id, e := c.Create(ctx, r)
		if e != nil {
			t.Fatal(r.Name, "create", e)
		}
		t.Cleanup(func() {
			if e := c.DeleteResourceFixture(ctx, r, id); e != nil {
				t.Error(r.Name, "cleanup", e)
			}
		})
		o, e = c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal(r.Name, "readback", e)
		}
		same, e := Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Name, "create verification", e)
		}
		if r.Kind == "profile" {
			r.Body["description"] = "CVK updated profile fixture"
		} else {
			r.Body["data"].(map[string]any)["flowVisibility"] = map[string]any{"optionType": "global", "value": true}
		}
		if e = c.Update(ctx, r, o); e != nil {
			t.Fatal(r.Name, "update", e)
		}
		o, e = c.Observe(ctx, r)
		if e != nil || o == nil {
			t.Fatal(r.Name, "updated readback", e)
		}
		same, e = Equal(r, o.Body)
		if e != nil || !same {
			t.Fatal(r.Name, "update verification", e)
		}
	}
}
func TestLiveFamilyOracle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_ORACLE") != "1" {
		t.Skip("requires upstream fixture")
	}
	c := liveClient(t)
	for _, r := range familyFixtures(t) {
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
