// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"os"
	"testing"
)

func extraFixtures(t *testing.T) []Resource {
	return parcelFixtures(t, "system-features")
}
func parcelFixtures(t *testing.T, name string) []Resource {
	t.Helper()
	b, e := os.ReadFile("../../../examples/configs/sdwan/" + name + ".yaml")
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
	if len(extraFixtures(t)) != 13 {
		t.Fatal("expected two profiles and eleven parcels")
	}
}
func TestLiveExtraNamesAbsent(t *testing.T) { liveParcelNamesAbsent(t, extraFixtures(t)) }
func liveParcelNamesAbsent(t *testing.T, resources []Resource) {
	c := liveClient(t)
	for _, r := range resources {
		o, e := c.Observe(context.Background(), r)
		if e != nil || o != nil {
			t.Fatal(r.Name, "must be absent", e)
		}
	}
}
func TestLiveExtraLifecycle(t *testing.T) { liveParcelLifecycle(t, extraFixtures(t)) }
func liveParcelLifecycle(t *testing.T, resources []Resource) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	c := liveClient(t)
	ctx := context.Background()
	for _, r := range resources {
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
func TestLiveExtraOracle(t *testing.T) { liveParcelOracle(t, extraFixtures(t)) }
func liveParcelOracle(t *testing.T, resources []Resource) {
	if os.Getenv("CVK_SDWAN_ORACLE") != "1" {
		t.Skip("requires upstream fixture")
	}
	c := liveClient(t)
	for _, r := range resources {
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

func TestPeripheralFixtures(t *testing.T) {
	if len(parcelFixtures(t, "peripheral-features")) != 4 {
		t.Fatal("expected two profiles and two parcels")
	}
}
func TestLivePeripheralNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "peripheral-features"))
}
func TestLivePeripheralLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "peripheral-features"))
}
func TestLivePeripheralOracle(t *testing.T) {
	liveParcelOracle(t, parcelFixtures(t, "peripheral-features"))
}

func TestUnqualifiedTrustSecFailsBeforeMutation(t *testing.T) {
	b, err := os.ReadFile("../../../examples/configs/sdwan/unqualified/trustsec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(b); err == nil {
		t.Fatal("unqualified TrustSec deployment accepted")
	}
}

func TestThousandEyesFixture(t *testing.T) {
	if len(parcelFixtures(t, "thousandeyes-feature")) != 2 {
		t.Fatal("expected one profile and one parcel")
	}
}
func TestLiveThousandEyesNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "thousandeyes-feature"))
}
func TestLiveThousandEyesLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "thousandeyes-feature"))
}
func TestLiveThousandEyesOracle(t *testing.T) {
	liveParcelOracle(t, parcelFixtures(t, "thousandeyes-feature"))
}
