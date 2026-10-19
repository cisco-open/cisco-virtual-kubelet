// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestTemplatePlatform(t *testing.T) {
	for _, mode := range []string{"family", "exact", "alternative", "wrong-family", "wrong-series", "wrong-type", "wrong-software", "wrong-version", "pattern", "variant", "bad-row", "bad-type", "empty"} {
		t.Run(mode, func(t *testing.T) {
			device := templateDevicePlatform{Family: "Switches and Hubs", Series: "Catalyst 9000", Type: "Catalyst 9300", SoftwareType: "IOS-XE", SoftwareVersion: "17.18.04"}
			row := map[string]any{"productFamily": device.Family}
			body := map[string]any{"softwareType": "IOS-XE", "deviceTypes": []any{row}}
			switch mode {
			case "exact":
				row["productSeries"] = device.Series
				row["productType"] = device.Type
				body["softwareVersion"] = device.SoftwareVersion
			case "alternative":
				body["deviceTypes"] = []any{map[string]any{"productFamily": "Routers"}, row}
			case "wrong-family":
				row["productFamily"] = "Routers"
			case "wrong-series":
				row["productSeries"] = "Other"
			case "wrong-type":
				row["productType"] = "Other"
			case "wrong-software":
				body["softwareType"] = "NX-OS"
			case "wrong-version":
				body["softwareVersion"] = "17.9.1"
			case "pattern":
				body["softwareVersion"] = "17.*"
			case "variant":
				body["softwareVariant"] = "variant"
			case "bad-row":
				body["deviceTypes"] = []any{row, "invalid"}
			case "bad-type":
				row["productSeries"] = true
			case "empty":
				body["deviceTypes"] = []any{}
			}
			err := validateTemplatePlatform(body, device)
			if (err == nil) != (mode == "family" || mode == "exact" || mode == "alternative") {
				t.Fatal(mode, err)
			}
		})
	}
}
func TestTemplateDevicePlatformBinding(t *testing.T) {
	for _, mode := range []string{"match", "serial", "id", "family", "unreachable", "missing-software", "missing-version", "version", "missing"} {
		t.Run(mode, func(t *testing.T) {
			want := Device{ID: "device", Serial: "FOC12345678", Family: "Switches and Hubs", SoftwareVersion: "17.18.04"}
			got := templateDevicePlatform{ID: want.ID, Serial: want.Serial, Family: want.Family, SoftwareType: "IOS-XE", SoftwareVersion: want.SoftwareVersion, Reachability: "Reachable"}
			switch mode {
			case "serial":
				got.Serial = "FOC99999999"
			case "id":
				got.ID = "other"
			case "family":
				got.Family = "Routers"
			case "unreachable":
				got.Reachability = "Unreachable"
			case "missing-software":
				got.SoftwareType = ""
			case "missing-version":
				got.SoftwareVersion = ""
			case "version":
				got.SoftwareVersion = "17.9.1"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != devicesPath+"/device" {
					t.Error("unexpected platform request")
					w.WriteHeader(500)
					return
				}
				if mode == "missing" {
					fmt.Fprint(w, `{}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": got})
			})
			result, err := c.templateDevicePlatform(context.Background(), want)
			if (err == nil) != (mode == "match") {
				t.Fatal(mode, err)
			}
			if err != nil && result.ID != "" {
				t.Fatal("partial binding escaped")
			}
		})
	}
}
