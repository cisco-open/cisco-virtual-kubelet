// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestAccessPointProvisionTransport(t *testing.T) {
	for _, mode := range []string{"provision", "reprovision", "missing-task", "unauthorized", "server-error", "invalid-state", "invalid-site", "invalid-profile", "invalid-target"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				writes++
				if r.Method != "POST" || r.URL.Path != accessPointProvisionPath || r.Header.Get("X-Auth-Token") != "test" {
					t.Error("wrong authenticated request")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid JSON")
				}
				expected := map[string]any{"networkDevices": []any{map[string]any{"deviceId": "ap-id"}}, "siteId": "floor", "rfProfileName": "TYPICAL"}
				if !reflect.DeepEqual(body, expected) {
					t.Error("request invented fields or changed target")
				}
				switch mode {
				case "missing-task":
					fmt.Fprint(w, `{"response":{}}`)
				case "unauthorized":
					w.WriteHeader(401)
					fmt.Fprint(w, "private-controller-detail")
				case "server-error":
					w.WriteHeader(500)
					fmt.Fprint(w, "private-controller-detail")
				default:
					fmt.Fprint(w, `{"response":{"taskId":"ap-task","url":"https://untrusted.invalid/task"}}`)
				}
			})
			target := accessPointConfigurationTarget{device: Device{ID: "ap-id", Serial: "SERIAL"}, ethernetMAC: "02:00:00:00:00:01"}
			state, site, profile := "PROVISION", "floor", "TYPICAL"
			switch mode {
			case "reprovision":
				state = "REPROVISION"
			case "invalid-state":
				state = "ASSIGN"
			case "invalid-site":
				site = "../floor"
			case "invalid-profile":
				profile = ""
			case "invalid-target":
				target.device.Serial = ""
			}
			task, err := c.provisionAccessPoint(context.Background(), target, site, profile, state)
			valid := mode == "provision" || mode == "reprovision"
			if (err == nil) != valid || valid && task.ID != "ap-task" {
				t.Fatal("unexpected receipt", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-controller-detail") {
				t.Fatal("response leaked")
			}
			expectedWrites := 1
			if strings.HasPrefix(mode, "invalid-") {
				expectedWrites = 0
			}
			if writes != expectedWrites {
				t.Fatal("invalid request dispatched or mutation retried")
			}
		})
	}
}
