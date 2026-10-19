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

func TestLANActiveSessionEvidence(t *testing.T) {
	for _, mode := range []string{"idle", "active", "disabled", "missing-count", "null-ids", "duplicate", "count-mismatch", "over-capacity", "negative", "leading-zero", "numeric-count", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			response := map[string]any{"maxSupportedCount": "5", "activeSessions": "0", "activeSessionIds": []string{}}
			switch mode {
			case "active":
				response["activeSessions"] = "2"
				response["activeSessionIds"] = []string{"b", "a"}
			case "disabled":
				response["maxSupportedCount"] = "0"
			case "missing-count":
				delete(response, "activeSessions")
			case "null-ids":
				response["activeSessionIds"] = nil
			case "duplicate":
				response["activeSessions"] = "2"
				response["activeSessionIds"] = []string{"a", "a"}
			case "count-mismatch":
				response["activeSessions"] = "1"
			case "over-capacity":
				response["activeSessions"] = "6"
			case "negative":
				response["activeSessions"] = "-1"
			case "leading-zero":
				response["activeSessions"] = "00"
			case "numeric-count":
				response["activeSessions"] = 0
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != lanActiveSessionsPath || r.URL.RawQuery != "" {
					t.Error("unexpected request")
					w.WriteHeader(400)
					return
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "no-content" {
					w.WriteHeader(204)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			snapshot, err := c.lanActiveSessions(context.Background())
			valid := mode == "idle" || mode == "active" || mode == "disabled"
			if (err == nil) != valid {
				t.Fatal("unexpected activity validity", err)
			}
			if mode == "active" && err == nil && snapshot.Active[0] != "a" {
				t.Fatal("activity IDs not canonical")
			}
		})
	}
}

func lanUsageFixture() map[string]any {
	return map[string]any{"id": "reservation", "name": "access", "poolType": "LAN", "siteId": "area", "ipV4AddressSpace": map[string]any{"subnet": "192.0.2.128", "prefixLength": 25, "globalPoolId": "pool", "totalAddresses": "128", "unassignableAddresses": "2", "assignedAddresses": "2", "defaultAssignedAddresses": "1"}}
}

func TestLANPoolUsageIdentityAndExclusions(t *testing.T) {
	for _, mode := range []string{"valid", "exhausted", "missing-stat", "numeric-stat", "fraction", "leading-zero", "negative", "overflow", "impossible-sum", "oversized", "wrong-id", "wrong-site", "wrong-parent", "wrong-cidr", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			response := lanUsageFixture()
			space := response["ipV4AddressSpace"].(map[string]any)
			switch mode {
			case "exhausted":
				space["assignedAddresses"] = "125"
			case "missing-stat":
				delete(space, "defaultAssignedAddresses")
			case "numeric-stat":
				space["assignedAddresses"] = 2
			case "fraction":
				space["assignedAddresses"] = "1.5"
			case "leading-zero":
				space["assignedAddresses"] = "02"
			case "negative":
				space["assignedAddresses"] = "-1"
			case "overflow":
				space["assignedAddresses"] = "18446744073709551616"
			case "impossible-sum":
				space["assignedAddresses"] = "128"
			case "oversized":
				space["totalAddresses"] = "129"
			case "wrong-id":
				response["id"] = "other"
			case "wrong-site":
				response["siteId"] = "other"
			case "wrong-parent":
				space["globalPoolId"] = "other"
			case "wrong-cidr":
				space["prefixLength"] = 24
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != sitePoolsPath+"/reservation" || r.URL.RawQuery != "" {
					t.Error("unexpected request")
					w.WriteHeader(400)
					return
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "no-content" {
					w.WriteHeader(204)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			usage, err := c.lanPoolUsage(context.Background(), fixtureReservations()[0])
			valid := mode == "valid" || mode == "exhausted"
			if (err == nil) != valid {
				t.Fatal("unexpected capacity validity", err)
			}
			if mode == "valid" && err == nil && usage.remaining() != 123 {
				t.Fatal("excluded addresses counted as free")
			}
			if mode == "exhausted" && err == nil && usage.remaining() != 0 {
				t.Fatal("exhausted pool was not represented exactly")
			}
		})
	}
}
