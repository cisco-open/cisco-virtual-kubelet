// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestDeviceAssignmentRequiresExactSite(t *testing.T) {
	for _, observedSite := range []string{"site", "other", ""} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			if r.Method != "GET" || r.URL.Path != assignedDevicesPath || r.URL.Query().Get("siteId") != "site" {
				t.Error("incorrect assignment read")
				w.WriteHeader(400)
				return
			}
			fmt.Fprintf(w, `{"response":[{"deviceId":"device","siteId":%q}]}`, observedSite)
		})
		members, err := c.assignedDevices(context.Background(), "site")
		if observedSite == "site" {
			if err != nil || !members["device"] {
				t.Fatal("valid assignment rejected", err)
			}
		} else if err == nil {
			t.Fatal("foreign or missing site accepted")
		}
	}
}
