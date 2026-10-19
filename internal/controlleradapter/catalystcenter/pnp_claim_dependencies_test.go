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

func TestPnPClaimDependencies(t *testing.T) {
	for _, mode := range []string{"stable", "rf", "missing-rf", "late-rf", "ambiguous-template", "image-name-alias", "explicit-image", "external-template", "no-template", "missing-site", "missing-image", "ambiguous-image", "missing-project", "missing-template", "draft-only", "composite", "missing-parameter", "unknown-parameter", "late-site", "late-image", "late-template", "late-version", "late-content"} {
		t.Run(mode, func(t *testing.T) {
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			index, err := nacmodel.IndexTemplates([]byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Lab","onboarding_templates":[{"name":"bootstrap"}]}]}}}`))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "explicit-image" {
				want.ImageID = "image-id"
				want.ImageName = "ignored-name"
			}
			if mode == "external-template" {
				want.Template.Name = "Lab#bootstrap"
				index, err = nacmodel.IndexTemplates([]byte(pnpIntentFixture()))
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "no-template" {
				want.Template = nil
			}
			if mode == "missing-parameter" {
				want.Template.Variables = nil
			}
			if mode == "unknown-parameter" {
				want.Template.Variables[0].Name = "unknown"
			}
			if mode == "rf" || mode == "missing-rf" || mode == "late-rf" {
				want.RFProfile = "LabRF"
			}
			if mode == "ambiguous-template" {
				index, err = nacmodel.IndexTemplates([]byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Lab","onboarding_templates":[{"name":"bootstrap"}]},{"name":"Other","onboarding_templates":[{"name":"bootstrap"}]}]}}}`))
				if err != nil {
					t.Fatal(err)
				}
			}
			late := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("dependency lookup mutated controller")
					w.WriteHeader(400)
					return
				}
				baseID, versionID, version := "base-id", "version-id", 2
				if late && mode == "late-template" {
					baseID = "new-base"
				}
				if late && mode == "late-version" {
					versionID = "new-version"
					version = 3
				}
				var response any
				switch r.URL.Path {
				case sitesPath:
					sites := sampleSites()
					if mode == "missing-site" {
						sites = sites[:2]
					}
					if late && mode == "late-site" {
						for i := range sites {
							if sites[i].ID == "building" {
								sites[i].ID = "new-building"
							}
							if sites[i].ParentID == "building" {
								sites[i].ParentID = "new-building"
							}
						}
					}
					response = sites
				case imagesPath:
					images := []Image{{ID: "image-id", Name: "image.bin", Version: "17.18.04"}}
					if mode == "image-name-alias" {
						images[0].ImageName = images[0].Name
						images[0].Name = ""
					}
					if mode == "missing-image" {
						images = nil
					}
					if mode == "ambiguous-image" {
						images = append(images, Image{ID: "other-image", Name: "image.bin"})
					}
					if late && mode == "late-image" {
						images[0].ID = "new-image"
					}
					response = images
				case wirelessRFPath:
					id := "rf-id"
					if late && mode == "late-rf" {
						id = "new-rf"
					}
					rows := []any{map[string]any{"id": id, "rfProfileName": "LabRF", "defaultRfProfile": false, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false}}
					if mode == "missing-rf" {
						rows = []any{}
					}
					response = rows
				case templateProjectsPath:
					projects := []templateProject{{ID: "project-id", Name: "Lab"}}
					if mode == "missing-project" {
						projects = []templateProject{}
					}
					json.NewEncoder(w).Encode(projects)
					return
				case templatesPath:
					rows := []templateSummary{{ID: baseID, ProjectID: "project-id", Name: "bootstrap"}}
					if mode == "missing-template" {
						rows = []templateSummary{}
					}
					json.NewEncoder(w).Encode(rows)
					return
				case templateVersionsPath + "/" + baseID:
					if mode == "draft-only" {
						json.NewEncoder(w).Encode([]any{})
						return
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": baseID, "versionsInfo": []any{map[string]any{"id": versionID, "version": version}}}})
					return
				case templatesPath + "/" + versionID:
					content := "sensitive-content"
					if late && mode == "late-content" {
						content = "changed-content"
					}
					json.NewEncoder(w).Encode(map[string]any{"id": versionID, "projectId": "project-id", "name": "bootstrap", "version": version, "composite": mode == "composite", "templateContent": content, "templateParams": []any{map[string]any{"parameterName": "password", "dataType": "STRING", "required": true}}})
					return
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			got, err := c.pnpClaimDependencies(context.Background(), want, "owned-pnp-id", index)
			valid := mode == "rf" || mode == "image-name-alias" || mode == "stable" || mode == "explicit-image" || mode == "external-template" || mode == "no-template" || strings.HasPrefix(mode, "late-")
			if (err == nil) != valid {
				t.Fatal("dependency result", err)
			}
			if !valid {
				return
			}
			if got.refs.deviceID != "owned-pnp-id" || got.refs.siteID != "building" || got.refs.imageID != "image-id" || want.Template != nil && got.refs.templateID != "base-id" {
				t.Fatal("incorrect reference binding")
			}
			if strings.Contains(fmt.Sprintf("%v %#v", got, got), "sensitive-content") {
				t.Fatal("template content leaked")
			}
			late = true
			err = c.revalidatePnPClaimDependencies(context.Background(), want, "owned-pnp-id", index, got)
			if (err != nil) != strings.HasPrefix(mode, "late-") {
				t.Fatal("dependency revalidation", err)
			}
		})
	}
}
