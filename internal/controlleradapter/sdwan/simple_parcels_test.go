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
		if r.Kind == "hierarchy-setting" {
			data := r.Body["data"].(map[string]any)
			if r.Type == "cflowd" {
				data["flowActiveTimeout"] = globalOption(601)
			} else {
				servers := data["highSpeedLogging"].([]any)
				servers[0].(map[string]any)["port"] = globalOption(2056)
			}
		} else if r.Kind == "device-template" {
			r.Body["templateDescription"] = "CVK updated isolated fixture"
		} else if r.Kind == "localized-policy" || r.Kind == "centralized-policy" || r.Kind == "security-policy" {
			r.Body["policyDescription"] = "CVK updated isolated fixture"
		} else if r.Kind == "" {
			r.Body["templateDescription"] = "CVK updated isolated fixture"
		} else {
			r.Body["description"] = "CVK updated isolated fixture"
		}
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

func TestSNMPFixture(t *testing.T) {
	if len(parcelFixtures(t, "snmp-feature")) != 2 {
		t.Fatal("expected one profile and one SNMP parcel")
	}
}
func TestLiveSNMPNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "snmp-feature"))
}
func TestLiveSNMPLifecycle(t *testing.T) { liveParcelLifecycle(t, parcelFixtures(t, "snmp-feature")) }

func TestAAAFixture(t *testing.T) {
	if len(parcelFixtures(t, "aaa-feature")) != 2 {
		t.Fatal("expected one profile and one AAA parcel")
	}
}
func TestLiveAAANamesAbsent(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, "aaa-feature")) }
func TestLiveAAALifecycle(t *testing.T)   { liveParcelLifecycle(t, parcelFixtures(t, "aaa-feature")) }

func TestServiceFeatureFixtures(t *testing.T) {
	resources := parcelFixtures(t, "service-features")
	if len(resources) != 9 {
		t.Fatal("expected service profile and eight parcels")
	}
}
func TestLiveServiceNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "service-features"))
}
func TestLiveServiceLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "service-features"))
}

func TestServiceSchemaCombinations(t *testing.T) {
	for _, input := range []map[string]any{
		{"type": "interface", "id": float64(10)},
		{"type": "route", "id": float64(11), "route_mask": "255.255.255.0", "vpn_id": float64(10)},
		{"type": "invalid", "id": float64(12)},
	} {
		if _, _, err := scalarParcelData("service_object_tracker", input); err == nil {
			t.Fatal("invalid object tracker accepted")
		}
	}
	for _, row := range []map[string]any{
		{"code": float64(66), "ascii": "a", "hex": "ab"},
		{"ascii": "a"},
		{"code": float64(66)},
	} {
		if _, _, err := scalarParcelData("service_dhcp_server", map[string]any{"options": []any{row}}); err == nil {
			t.Fatal("invalid DHCP option combination accepted")
		}
	}
	if _, _, err := scalarParcelData("service_dhcp_server", map[string]any{"static_leases": []any{map[string]any{"ip_address": "192.0.2.10"}}}); err == nil {
		t.Fatal("incomplete static lease accepted")
	}
}

func TestTransportCellularFixtures(t *testing.T) {
	if len(parcelFixtures(t, "transport-cellular")) != 4 {
		t.Fatal("expected four cellular fixture objects")
	}
	for _, input := range []map[string]any{
		{"profile_id": float64(2), "authentication_enable": "true"},
		{"profile_id": float64(2), "authentication_enable": false, "profile_username": "discarded"},
		{"profile_id": float64(2), "authentication_enable": true},
		{"profile_id": float64(2), "slice_differentiator": float64(10)},
		{"profile_id": float64(2), "no_overwrite": true},
	} {
		if _, _, err := scalarParcelData("transport_cellular_profile", input); err == nil {
			t.Fatal("invalid cellular combination accepted")
		}
	}
}
func TestLiveTransportCellularNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "transport-cellular"))
}
func TestLiveTransportCellularLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "transport-cellular"))
}
