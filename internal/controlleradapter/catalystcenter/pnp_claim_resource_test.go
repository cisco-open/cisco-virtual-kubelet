// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestPnPClaimVersionBinding(t *testing.T) {
	for _, mode := range []string{"stable", "image-version", "image-id", "template-version-id", "template-version-number", "project-id", "secrets-excluded", "missing-image-version", "missing-template-version", "mismatched-image", "extra-template"} {
		t.Run(mode, func(t *testing.T) {
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			dependencies := &pnpClaimDependencies{
				refs: pnpClaimReferences{deviceID: "device-id", siteID: "site-id", imageID: "image-id", templateID: "template-id"},
				site: Site{ID: "site-id", NameHierarchy: want.Site}, image: &Image{ID: "image-id", Version: "17.18.04"},
				template: &regularTemplateContent{project: templateProject{ID: "project-id"}, base: templateSummary{ID: "template-id", ProjectID: "project-id"}, version: templateVersion{ID: "committed-id", Number: 1}, body: map[string]any{"templateContent": "private-content"}},
			}
			before, err := pnpClaimResource(want, dependencies)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "image-version":
				dependencies.image.Version = "26.02.01"
			case "image-id":
				dependencies.refs.imageID = "new-image"
				dependencies.image.ID = "new-image"
			case "template-version-id":
				dependencies.template.version.ID = "new-version"
			case "template-version-number":
				dependencies.template.version.Number = 2
			case "project-id":
				dependencies.template.project.ID = "new-project"
				dependencies.template.base.ProjectID = "new-project"
			case "secrets-excluded":
				dependencies.template.body["templateContent"] = "different-private-content"
				dependencies.template.version.Comment = "secret-comment"
				want.Template.Variables[0].Value = "different-private-variable"
			case "missing-image-version":
				dependencies.image.Version = ""
			case "missing-template-version":
				dependencies.template.version.ID = ""
			case "mismatched-image":
				dependencies.image.ID = "mismatch"
			case "extra-template":
				want.Template = nil
				dependencies.refs.templateID = ""
			}
			after, err := pnpClaimResource(want, dependencies)
			invalid := strings.HasPrefix(mode, "missing-") || mode == "mismatched-image" || mode == "extra-template"
			if (err != nil) != invalid {
				t.Fatal("binding validation", err)
			}
			if invalid {
				return
			}
			unchanged := mode == "stable" || mode == "secrets-excluded"
			if (before == after) != unchanged {
				t.Fatal("wrong version/secret binding")
			}
		})
	}
}
