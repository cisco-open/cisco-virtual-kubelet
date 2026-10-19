// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestPlannedAPReport(t *testing.T) {
	for _, mode := range []string{"match", "missing-floor", "missing-ap", "position", "radio", "type", "partial"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			sites := sampleSites()
			if mode != "missing-floor" {
				sites = append(sites, Site{ID: "floor", Name: "Floor", Type: "floor", ParentID: "building", NameHierarchy: "Global/Lab/Building/Floor"})
			}
			body := plannedAPObservedFixture
			switch mode {
			case "position":
				body = strings.Replace(body, `"x":4.5`, `"x":9`, 1)
			case "radio":
				body = strings.Replace(body, `"txPower":12`, `"txPower":18`, 1)
			case "type":
				body = strings.Replace(body, `"type":"C9130AXI"`, `"type":"other"`, 1)
			}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != http.MethodGet || r.URL.Path != plannedAPPositionsPath("floor") {
					t.Error("unexpected request")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "missing-ap" {
					fmt.Fprint(w, `{"response":[]}`)
				} else {
					fmt.Fprint(w, `{"response":[`+body+`]}`)
				}
			})
			plans, err := c.compileConfigReports([]byte(plannedAPFixture), []string{"sites"})
			if err != nil || len(plans) != 1 || plans[0].count != 2 {
				t.Fatal("report compilation failed", err)
			}
			drift, err := c.reportPlannedAPs(context.Background(), desired, sites)
			if mode == "partial" {
				if err == nil || drift != nil {
					t.Fatal("partial inventory yielded drift")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "match" {
				if len(drift) != 0 {
					t.Fatal("false drift")
				}
				return
			}
			expected := configapi.NetworkControllerDriftChangeUpdate
			if mode == "missing-floor" || mode == "missing-ap" {
				expected = configapi.NetworkControllerDriftChangeAdd
			}
			if len(drift) != 1 || drift[0].Change != expected || drift[0].Resource != plannedAPResource(desired[0]) {
				t.Fatal("wrong drift")
			}
			if mode == "missing-floor" && reads != 0 {
				t.Fatal("queried nonexistent floor")
			}
		})
	}
}
