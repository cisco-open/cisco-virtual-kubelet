// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestFabricL2InventoryCompleteness(t *testing.T) {
	for _, mode := range []string{"paged", "repeat", "partial", "missing-id", "missing-fabric", "missing-wireless", "reserved-vlan", "same-vlan-id", "same-vlan-name", "wrong-type", "same-vlan-other-fabric"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != fabricL2NetworksPath || r.Method != "GET" {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				requests++
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				if limit != 50 || offset < 1 {
					t.Error("unbounded page request")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "repeat" {
					offset = 1
				}
				rows := []fabricL2Network{}
				wireless := false
				for i := offset; i < offset+limit && i <= 51; i++ {
					n := fabricL2Network{ID: fmt.Sprintf("vn-%d", i), FabricID: "fabric", VLANName: fmt.Sprintf("vlan-%d", i), VLAN: i + 1, Traffic: "DATA", Wireless: &wireless}
					if i == 1 {
						switch mode {
						case "missing-id":
							n.ID = ""
						case "missing-fabric":
							n.FabricID = ""
						case "missing-wireless":
							n.Wireless = nil
						case "reserved-vlan":
							n.VLAN = 2046
						case "wrong-type":
							n.Traffic = "UNKNOWN"
						}
					}
					if i == 51 {
						switch mode {
						case "same-vlan-id":
							n.VLAN = 2
						case "same-vlan-name":
							n.VLANName = "vlan-1"
						case "same-vlan-other-fabric":
							n.VLAN = 2
							n.VLANName = "vlan-1"
							n.FabricID = "zone"
						}
					}
					rows = append(rows, n)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.fabricL2Networks(context.Background())
			valid := mode == "paged" || mode == "same-vlan-other-fabric"
			if (err == nil) != valid {
				t.Fatal("unexpected inventory result", err)
			}
			if valid && (len(got) != 51 || requests != 2) {
				t.Fatal("incomplete pagination")
			}
			if !valid && got != nil {
				t.Fatal("returned partial inventory")
			}
		})
	}
}
