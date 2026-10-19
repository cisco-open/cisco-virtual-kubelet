// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPnPPreview(t *testing.T) {
	for _, mode := range []string{"complete", "pending", "failed", "lost", "auth", "async", "conflict", "empty", "missing-flags", "wrong-site", "bad-task", "late-identity", "late-workflow", "unclaimed"} {
		t.Run(mode, func(t *testing.T) {
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
			posts, reads := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					reads++
					row := pnpFixture("pnp-id", "FOC12345678")
					info := row["deviceInfo"].(map[string]any)
					info["hostname"], info["siteId"] = "edge", "building"
					if mode == "unclaimed" {
						delete(info, "siteId")
					}
					if reads > 1 && mode == "late-identity" {
						row["id"] = "replacement"
					}
					if reads > 1 && mode == "late-workflow" {
						row["workflow"].(map[string]any)["lastupdateOn"] = 1000
					}
					json.NewEncoder(w).Encode([]any{row})
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case devicesPath:
					fmt.Fprint(w, `{"response":[]}`)
				case pnpPreviewPath:
					posts++
					var body map[string]string
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 3 || body["deviceId"] != "pnp-id" || body["siteId"] != "building" || body["type"] != "Default" {
						t.Error("incorrect preview request")
					}
					switch mode {
					case "lost":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					case "auth":
						w.WriteHeader(401)
						return
					case "async":
						w.WriteHeader(202)
						return
					case "conflict":
						w.WriteHeader(409)
						return
					case "empty":
						fmt.Fprint(w, `{}`)
						return
					}
					response := map[string]any{"complete": mode != "pending", "error": mode == "failed", "siteId": "building", "taskId": "task-id", "config": "private-config", "errorMessage": "private-error"}
					if mode == "missing-flags" {
						delete(response, "complete")
					}
					if mode == "wrong-site" {
						response["siteId"] = "other-site"
					}
					if mode == "bad-task" {
						response["taskId"] = "bad/id"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": response})
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			plan, err := c.planPnPPreview(context.Background(), want, "pnp-id", "building")
			if mode == "unclaimed" {
				if err == nil || posts != 0 {
					t.Fatal("unclaimed preview accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := plan.submit(context.Background())
			valid := mode == "complete" || mode == "pending" || mode == "failed"
			if (err == nil) != valid {
				t.Fatal("preview result", err)
			}
			if valid {
				if got.Complete != (mode != "pending") || got.Failed != (mode == "failed") || got.SiteID != "building" || got.TaskID != "task-id" {
					t.Fatal("lost preview metadata")
				}
				encoded, _ := json.Marshal(got)
				if strings.Contains(string(encoded), "private-") {
					t.Fatal("preview exposed configuration")
				}
			}
			if _, err := plan.submit(context.Background()); err == nil {
				t.Fatal("reused plan")
			}
			if strings.HasPrefix(mode, "late-") {
				if posts != 0 {
					t.Fatal("submitted after native change")
				}
			} else if posts != 1 {
				t.Fatal("submission replayed", posts)
			}
		})
	}
}

func TestPnPPreviewTypes(t *testing.T) {
	for _, typ := range []string{"", "Default", "StackSwitch", "AccessPoint", "Sensor", "MobilityExpress", "CatalystWLC"} {
		t.Run(typ, func(t *testing.T) {
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P", Type: typ}
			if typ == "StackSwitch" {
				want.Serial += ",FOC87654321"
			}
			body, err := pnpPreviewPayload(want, "pnp-id", "building")
			if (err != nil) != (typ == "AccessPoint" || typ == "CatalystWLC") {
				t.Fatal(err)
			}
			if err != nil {
				return
			}
			var fields map[string]string
			if json.Unmarshal(body, &fields) != nil {
				t.Fatal("invalid payload")
			}
			expected := typ
			if expected == "" {
				expected = "Default"
			}
			if fields["type"] != expected {
				t.Fatal("wrong native type")
			}
		})
	}
}
