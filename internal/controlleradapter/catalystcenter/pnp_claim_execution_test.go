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

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func TestPnPSiteClaimRejectsIncompletePlatformInputs(t *testing.T) {
	for _, typ := range []string{"AccessPoint", "Sensor", "CatalystWLC", "MobilityExpress"} {
		t.Run(typ, func(t *testing.T) {
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			want.Type = typ
			// No transport is configured: these known-incomplete inputs must
			// be rejected before authentication or any controller request.
			if plan, err := (&client{}).planPnPSiteClaim(context.Background(), want, "owned-id", nil); err == nil || plan != nil {
				t.Fatal("incomplete platform request planned")
			}
		})
	}
}

func TestPnPSiteClaimSubmission(t *testing.T) {
	for _, mode := range []string{"observed", "frozen-input", "lost", "async", "auth", "conflict", "missing-response", "empty-response", "task-object", "late-state", "late-image", "late-version"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			index, err := nacmodel.IndexTemplates([]byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Lab","onboarding_templates":[{"name":"bootstrap"}]}]}}}`))
			if err != nil {
				t.Fatal(err)
			}
			writes, auths := 0, 0
			late := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					auths++
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == pnpSiteClaimPath {
					writes++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("claim payload")
					}
					config := body["configInfo"].(map[string]any)
					variable := config["configParameters"].([]any)[0].(map[string]any)
					if body["deviceId"] != "owned-id" || body["siteId"] != "building" || config["configId"] != "base-id" || variable["value"] != "fixture-secret" {
						t.Error("request identity/input changed")
					}
					switch mode {
					case "lost":
						fmt.Fprint(w, `{`)
					case "async":
						w.WriteHeader(202)
						fmt.Fprint(w, `{"response":"queued"}`)
					case "auth":
						w.WriteHeader(401)
					case "conflict":
						w.WriteHeader(409)
					case "missing-response":
						fmt.Fprint(w, `{}`)
					case "empty-response":
						fmt.Fprint(w, `{"response":""}`)
					case "task-object":
						fmt.Fprint(w, `{"response":{"taskId":"not-a-claim-receipt"}}`)
					default:
						fmt.Fprint(w, `{"response":"opaque acknowledgement fixture-secret"}`)
					}
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					row := pnpFixture("owned-id", "FOC12345678")
					info := row["deviceInfo"].(map[string]any)
					info["hostname"] = "edge"
					if late && mode == "late-state" {
						info["state"] = "changed"
					}
					json.NewEncoder(w).Encode([]any{row})
				case devicesPath:
					fmt.Fprint(w, `{"response":[]}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case imagesPath:
					id := "image-id"
					if late && mode == "late-image" {
						id = "new-image"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []Image{{ID: id, Name: "image.bin", Version: "17.18.04"}}})
				case templateProjectsPath:
					json.NewEncoder(w).Encode([]templateProject{{ID: "project-id", Name: "Lab"}})
				case templatesPath:
					json.NewEncoder(w).Encode([]templateSummary{{ID: "base-id", ProjectID: "project-id", Name: "bootstrap"}})
				case templateVersionsPath + "/base-id":
					id, version := "committed", 1
					if late && mode == "late-version" {
						id = "next"
						version = 2
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": "base-id", "versionsInfo": []any{map[string]any{"id": id, "version": version}}}})
				case templatesPath + "/committed", templatesPath + "/next":
					id, version := "committed", 1
					if strings.HasSuffix(r.URL.Path, "/next") {
						id = "next"
						version = 2
					}
					json.NewEncoder(w).Encode(map[string]any{"id": id, "name": "bootstrap", "projectId": "project-id", "version": version, "composite": false, "templateParams": []any{map[string]any{"parameterName": "password", "dataType": "STRING"}}})
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			plan, err := c.planPnPSiteClaim(ctx, want, "owned-id", index)
			if err != nil {
				t.Fatal(err)
			}
			if writes != 0 || !strings.HasPrefix(plan.resource, "pnp-claim:") || strings.Contains(plan.resource, "fixture-secret") {
				t.Fatal("planning mutated or leaked input")
			}
			late = true
			if mode == "frozen-input" {
				want.Template.Variables[0].Value = "changed-secret"
			}
			response, err := plan.submit(ctx)
			valid := mode == "observed" || mode == "frozen-input"
			if (err == nil) != valid {
				t.Fatal("submission result", err)
			}
			if valid && (response == nil || response.deviceID != "owned-id" || response.siteID != "building") {
				t.Fatal("response binding")
			}
			if strings.Contains(fmt.Sprintf("%v %#v", response, err), "fixture-secret") {
				t.Fatal("raw response leaked")
			}
			count := 1
			if strings.HasPrefix(mode, "late-") {
				count = 0
			}
			if writes != count || auths != 1 {
				t.Fatal("unexpected submission/retry count", writes, auths)
			}
			if _, err := plan.submit(ctx); err == nil || writes != count {
				t.Fatal("claim replayed")
			}
		})
	}
}
