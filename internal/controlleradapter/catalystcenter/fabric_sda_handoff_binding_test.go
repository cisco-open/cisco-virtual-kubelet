// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestFabricSDAHandoffBinding(t *testing.T) {
	for _, mode := range []string{"valid", "bgp", "wrong-device", "wrong-role", "missing-transit", "wrong-type", "missing-settings", "missing-cp", "duplicate-cp", "invalid-cp", "mixed-settings", "multicast-disabled", "multicast-bgp", "bad-affinity"} {
		t.Run(mode, func(t *testing.T) {
			flag := true
			zero := int64(0)
			want := fabricSDAHandoffIntent{Device: "border", Transit: "SDA", Prime: &zero, Internet: &flag, Multicast: &flag}
			device := fabricDeviceConfiguration{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}}
			transit := fabricTransit{ID: "transit", Name: "SDA", Type: "SDA_LISP_PUB_SUB_TRANSIT", SDA: &sdaTransitSettings{Multicast: &flag, ControlPlanes: []string{"cp-b", "cp-a"}}}
			switch mode {
			case "bgp":
				transit.Type = "SDA_LISP_BGP_TRANSIT"
				want.Multicast = nil
				transit.SDA.Multicast = nil
			case "wrong-device":
				want.Device = "other"
			case "wrong-role":
				device.Roles = []string{"EDGE_NODE"}
				device.Border = nil
			case "missing-transit":
				transit.Name = "other"
			case "wrong-type":
				transit.Type = "IP_BASED_TRANSIT"
			case "missing-settings":
				transit.SDA = nil
			case "missing-cp":
				transit.SDA.ControlPlanes = nil
			case "duplicate-cp":
				transit.SDA.ControlPlanes = []string{"cp-a", "cp-a"}
			case "invalid-cp":
				transit.SDA.ControlPlanes = []string{""}
			case "mixed-settings":
				transit.IP = &ipTransitSettings{Protocol: "BGP", ASN: "65001"}
			case "multicast-disabled":
				f := false
				transit.SDA.Multicast = &f
			case "multicast-bgp":
				transit.Type = "SDA_LISP_BGP_TRANSIT"
			case "bad-affinity":
				zero = -1
			}
			got, err := bindFabricSDAHandoff(want, "border", device, map[string]fabricTransit{"SDA": transit})
			if mode != "valid" && mode != "bgp" {
				if err == nil || got.digest != "" {
					t.Fatal("invalid dependency accepted")
				}
				return
			}
			if err != nil || got.desired.DeviceID != "device" || got.desired.TransitID != "transit" || len(got.digest) != 64 {
				t.Fatal("missing binding", err)
			}
			if transit.SDA.ControlPlanes[0] != "cp-b" || !reflect.DeepEqual(got.transit.SDA.ControlPlanes, []string{"cp-a", "cp-b"}) {
				t.Fatal("canonicalization mutated input")
			}
			transit.SDA.ControlPlanes = []string{"cp-c"}
			changed, err := bindFabricSDAHandoff(want, "border", device, map[string]fabricTransit{"SDA": transit})
			if err != nil || changed.digest == got.digest {
				t.Fatal("control-plane replacement not detected", err)
			}
			zero = 42
			flag = false
			if *got.desired.Prime != 0 || !*got.desired.Internet {
				t.Fatal("projection aliased input")
			}
		})
	}
}

func TestFabricSDATransitSettingsRetained(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path != fabricTransitsPath || r.Method != "GET" {
			t.Error("unexpected request")
		}
		fmt.Fprint(w, `{"response":[{"id":"transit","name":"SDA","type":"SDA_LISP_PUB_SUB_TRANSIT","sdaTransitSettings":{"isMulticastOverTransitEnabled":false,"controlPlaneNetworkDeviceIds":["cp-a"]}}]}`)
	})
	rows, err := c.fabricTransits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := rows["SDA"].SDA
	if got == nil || got.Multicast == nil || *got.Multicast || !reflect.DeepEqual(got.ControlPlanes, []string{"cp-a"}) {
		t.Fatal("native SDA dependencies discarded")
	}
}
