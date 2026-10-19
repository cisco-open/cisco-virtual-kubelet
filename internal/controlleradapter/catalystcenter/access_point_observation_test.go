// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAccessPointKeyedObservation(t *testing.T) {
	fixture := `{"ethMac":"02-00-00-00-00-01","apName":"AP1","provisioningStatus":"unknown-native-value","adminStatus":"Enabled","radioDTOs":[{"ifType":2,"slotId":0,"powerlevel":3,"radioRoleAssignment":{"native":"value"}}]}`
	for _, mode := range []string{"valid", "missing-status", "wrong-mac", "base-mac-only", "missing-name", "array", "envelope", "null", "partial", "duplicate-slot", "missing-slot", "bad-type", "invalid-key"} {
		t.Run(mode, func(t *testing.T) {
			raw := fixture
			key := "02:00:00:00:00:01"
			switch mode {
			case "missing-status":
				raw = strings.Replace(raw, `"provisioningStatus":"unknown-native-value",`, "", 1)
			case "wrong-mac":
				raw = strings.Replace(raw, "02-00-00-00-00-01", "02-00-00-00-00-02", 1)
			case "base-mac-only":
				raw = strings.Replace(raw, "ethMac", "macAddress", 1)
			case "missing-name":
				raw = strings.Replace(raw, `"apName":"AP1",`, "", 1)
			case "array":
				raw = "[" + raw + "]"
			case "envelope":
				raw = `{"response":` + raw + `}`
			case "null":
				raw = "null"
			case "duplicate-slot":
				raw = strings.Replace(raw, `"radioDTOs":[`, `"radioDTOs":[{"ifType":1,"slotId":0},`, 1)
			case "missing-slot":
				raw = strings.Replace(raw, `"slotId":0,`, "", 1)
			case "bad-type":
				raw = strings.Replace(raw, `"ifType":2`, `"ifType":99`, 1)
			case "invalid-key":
				key = "invalid"
			}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != "GET" || r.URL.Path != accessPointSummaryPath || r.URL.Query().Get("key") != "02:00:00:00:00:01" || len(r.URL.Query()) != 1 {
					t.Error("unbounded or incorrect AP query")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				fmt.Fprint(w, raw)
			})
			got, err := c.accessPointConfiguration(context.Background(), key)
			valid := mode == "valid" || mode == "missing-status"
			if (err == nil) != valid {
				t.Fatalf("unexpected observation: %v", err)
			}
			if valid && (got.EthernetMAC != "02:00:00:00:00:01" || got.Name != "AP1" || len(got.Radios) != 1 || *got.Radios[0].PowerLevel != 3) {
				t.Fatal("observation lost fields")
			}
			if mode == "missing-status" && got.ProvisioningStatus != nil {
				t.Fatal("missing state fabricated")
			}
			if mode == "invalid-key" && reads != 0 {
				t.Fatal("invalid selector dispatched")
			}
		})
	}
}
