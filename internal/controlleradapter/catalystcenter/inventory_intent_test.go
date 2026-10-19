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

const inventoryAssignModel = `{"catalyst_center":{"inventory":{"devices":[{"name":"switch","device_ip":"198.51.100.101","serial_number":"serial","state":"ASSIGN","site":"Global/Lab","tags":["lab"]}]}}}`

func TestInventoryAssignmentAndTagReport(t *testing.T) {
	assigned, member := false, false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("report mutated controller")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case devicesPath:
			fmt.Fprint(w, `{"response":[{"id":"device","hostname":"switch","managementIpAddress":"198.51.100.101","serialNumber":"serial"}]}`)
		case sitesPath:
			fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"},{"id":"site","name":"Lab","nameHierarchy":"Global/Lab","type":"area","parentId":"global"}]}`)
		case assignedDevicesPath:
			if r.URL.Query().Get("siteId") != "site" {
				t.Error("wrong target site")
			}
			if assigned {
				fmt.Fprint(w, `{"response":[{"deviceId":"device","siteId":"site"}]}`)
			} else {
				fmt.Fprint(w, `{"response":[]}`)
			}
		case tagsPath:
			fmt.Fprint(w, `{"response":[{"id":"tag","name":"lab","systemTag":false}]}`)
		case tagsPath + "/tag/member":
			if member {
				fmt.Fprint(w, `{"response":[{"instanceUuid":"device"}]}`)
			} else {
				fmt.Fprint(w, `{"response":[]}`)
			}
		default:
			t.Error("unexpected API")
			w.WriteHeader(404)
		}
	})
	plans, err := c.compileConfigReports([]byte(inventoryAssignModel), []string{"inventory"})
	if err != nil || len(plans) != 1 || plans[0].count != 2 {
		t.Fatal("report compile failed", err)
	}
	drift, err := plans[0].observe(context.Background())
	if err != nil || len(drift) != 2 || drift[0].Path != "/site_assignment" || drift[1].Path != "/tag_membership" {
		t.Fatal("missing inventory drift", err)
	}
	assigned, member = true, true
	drift, err = plans[0].observe(context.Background())
	if err != nil || len(drift) != 0 {
		t.Fatal("converged inventory report failed", err)
	}
	if _, err := c.compileConfigurationExecutor([]byte(inventoryAssignModel), "inventory"); err == nil {
		t.Fatal("device apply bypassed execution qualification")
	}
}

func TestInventoryIntentRejectsUnsupportedStateAndFields(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(inventoryAssignModel, `"ASSIGN"`, `"PROVISION"`, 1),
		strings.Replace(inventoryAssignModel, `"ASSIGN"`, `"REPROVISION"`, 1),
		strings.Replace(inventoryAssignModel, `"state":"ASSIGN",`, "", 1),
		strings.Replace(inventoryAssignModel, `"serial_number"`, `"clean_up_config"`, 1),
		strings.Replace(inventoryAssignModel, `"198.51.100.101"`, `"example.com"`, 1),
		strings.Replace(inventoryAssignModel, `"Global/Lab"`, `"Lab"`, 1),
	} {
		if _, err := parseInventoryIntent([]byte(raw)); err == nil {
			t.Fatal("unsupported inventory accepted")
		}
	}
}
