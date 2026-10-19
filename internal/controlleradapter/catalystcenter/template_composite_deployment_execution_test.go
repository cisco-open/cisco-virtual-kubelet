// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestCompositeResourceBindsProjectsWithoutContent(t *testing.T) {
	fixture := compositeDeploymentFixture()
	content := &compositeDeploymentContent{parent: &regularTemplateContent{base: fixture.base, version: fixture.version}}
	for _, m := range fixture.members {
		content.members = append(content.members, &regularTemplateContent{base: m.base, version: m.version})
	}
	snapshot := &compositeTemplateDependencies{device: &wiredProvisionSnapshot{}, content: content}
	target := compositeTemplateTarget{copyingConfig: true, forcePush: true}
	before, err := compositeTemplateDeploymentResource(snapshot, target)
	if err != nil {
		t.Fatal(err)
	}
	target.parameters = map[string]map[string][]string{"Baseline": {"password": {"sensitive-value"}}}
	content.parent.body = map[string]any{"templateContent": "sensitive-content"}
	same, err := compositeTemplateDeploymentResource(snapshot, target)
	if err != nil || before != same {
		t.Fatal("content entered resource identity", err)
	}
	content.members[1].base.ProjectID = "replacement-project"
	changed, err := compositeTemplateDeploymentResource(snapshot, target)
	if err != nil || changed == before {
		t.Fatal("member project substitution retained resource identity", err)
	}
	content.members[1].base.ProjectID = fixture.members[1].base.ProjectID
	content.parent.base.ProjectID = "replacement-parent-project"
	changed, err = compositeTemplateDeploymentResource(snapshot, target)
	if err != nil || changed == before {
		t.Fatal("parent project substitution retained resource identity", err)
	}
}
