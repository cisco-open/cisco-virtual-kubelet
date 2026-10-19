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

func TestRegularTemplateDependencies(t *testing.T) {
	for _, mode := range []string{"stable", "empty-selection-single", "empty-selection-multi", "unprovisioned", "unassigned", "unreachable", "missing-project", "missing-template", "draft-only", "composite", "unknown-kind", "wrong-version", "late-serial", "late-provision", "late-project", "late-template", "late-version", "late-content", "late-platform", "late-selection"} {
		t.Run(mode, func(t *testing.T) {
			late := false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					writes++
					w.WriteHeader(500)
					return
				}
				projectID, baseID, versionID := "project", "base", "committed"
				if late && mode == "late-project" {
					projectID = "new-project"
				}
				if late && mode == "late-template" {
					baseID = "new-base"
				}
				if late && mode == "late-version" {
					versionID = "new-version"
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					device := Device{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}
					if mode == "unreachable" {
						device.Reachability = "Unreachable"
					}
					if late && mode == "late-serial" {
						device.Serial = "FOC99999999"
					}
					rows = []Device{device}
				case devicesPath + "/edge-id":
					platform := templateDevicePlatform{ID: "edge-id", Serial: "FOC12345678", Family: "Switches and Hubs", SoftwareType: "IOS-XE", SoftwareVersion: "17.18.04", Reachability: "Reachable"}
					if late && mode == "late-platform" {
						platform.SoftwareVersion = "26.02.01"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": platform})
					return
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
					if mode == "unassigned" {
						rows = []map[string]string{}
					}
				case wiredProvisionPath:
					if r.URL.Query().Get("networkDeviceId") != "edge-id" {
						t.Error("provision lookup missing device binding")
					}
					id := "provisioned"
					if late && mode == "late-provision" {
						id = "replaced"
					}
					rows = []wiredProvisionedDevice{{ID: id, DeviceID: "edge-id", SiteID: "building"}}
					if mode == "unprovisioned" {
						rows = []wiredProvisionedDevice{}
					}
				case templateProjectsPath:
					projects := []templateProject{{ID: projectID, Name: "Lab"}}
					if mode == "missing-project" {
						projects = []templateProject{}
					}
					json.NewEncoder(w).Encode(projects)
					return
				case templatesPath:
					definitions := []templateSummary{{ID: baseID, ProjectID: projectID, Name: "Baseline"}}
					if mode == "missing-template" {
						definitions = []templateSummary{}
					}
					json.NewEncoder(w).Encode(definitions)
					return
				case templateVersionsPath + "/" + baseID:
					number := 2
					if mode == "draft-only" {
						number = 0
					}
					if late && mode == "late-version" {
						number = 3
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": baseID, "versionsInfo": []any{map[string]any{"id": versionID, "version": number}}}})
					return
				case templatesPath + "/" + versionID:
					number := 2
					if late && mode == "late-version" {
						number = 3
					}
					if mode == "wrong-version" {
						number = 1
					}
					body := map[string]any{"id": versionID, "projectId": projectID, "name": "Baseline", "version": number, "composite": false, "templateContent": "secret-value", "softwareType": "IOS-XE", "deviceTypes": []any{map[string]any{"productFamily": "Switches and Hubs"}}}
					if strings.HasPrefix(mode, "empty-selection-") || mode == "late-selection" {
						kind := "SINGLE_SELECT"
						if mode == "empty-selection-multi" {
							kind = "MULTI_SELECT"
						}
						choices := map[string]any{}
						if late && mode == "late-selection" {
							choices["added"] = "secret-choice"
						}
						body["templateParams"] = []any{map[string]any{"parameterName": "optional", "dataType": "STRING", "required": false, "defaultValue": "", "selection": map[string]any{"selectionType": kind, "selectionValues": choices}}}
					}
					if mode == "composite" {
						body["composite"] = true
					}
					if mode == "unknown-kind" {
						delete(body, "composite")
					}
					if late && mode == "late-content" {
						body["templateContent"] = "changed-secret-value"
					}
					json.NewEncoder(w).Encode(body)
					return
				default:
					t.Error("unexpected dependency request", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			want := inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}
			identity := nacmodel.TemplateIdentity{Project: "Lab", Name: "Baseline"}
			snapshot, err := c.regularTemplateDependencies(context.Background(), want, identity)
			valid := mode == "stable" || strings.HasPrefix(mode, "empty-selection-") || strings.HasPrefix(mode, "late-")
			if (err == nil) != valid {
				t.Fatal(mode, "unexpected preflight result", err)
			}
			if !valid {
				if snapshot != nil {
					t.Fatal("partial dependency evidence escaped")
				}
			} else {
				if snapshot.device.target.ID != "edge-id" || snapshot.base.ID != "base" || snapshot.version.ID != "committed" || snapshot.version.Number != 2 {
					t.Fatal("wrong binding")
				}
				target := regularTemplateTarget{template: identity, redeploy: "NEVER", copyingConfig: true, forcePush: true}
				payload, err := snapshot.payload(target)
				if err != nil || len(payload) == 0 {
					t.Fatal("resolved request was not built", err)
				}
				if strings.HasPrefix(mode, "empty-selection-") || mode == "late-selection" {
					target.parameters = map[string][]string{"optional": {"secret-choice"}}
					if body, err := snapshot.payload(target); err == nil || body != nil {
						t.Fatal("empty selection became an unrestricted request")
					}
				}
				target.parameters = map[string][]string{"undeclared": {"secret-value"}}
				if payload, err := snapshot.payload(target); err == nil || payload != nil {
					t.Fatal("unvalidated parameters reached request")
				}
				late = true
				err = c.revalidateRegularTemplateDependencies(context.Background(), want, identity, snapshot)
				if (err == nil) != (mode == "stable" || strings.HasPrefix(mode, "empty-selection-")) {
					t.Fatal(mode, "changed dependencies accepted", err)
				}
			}
			if writes != 0 {
				t.Fatal("preflight mutated appliance")
			}
			if err != nil && strings.Contains(err.Error(), "secret-value") {
				t.Fatal("content leaked")
			}
		})
	}
}
