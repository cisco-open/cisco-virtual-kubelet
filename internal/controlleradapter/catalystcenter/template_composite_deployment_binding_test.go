// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"testing"
)

func TestCompositeDeploymentVersionBindings(t *testing.T) {
	fixture := compositeDeploymentFixture()
	content := &compositeDeploymentContent{parent: &regularTemplateContent{base: fixture.base, version: fixture.version, body: map[string]any{"secret": "sensitive-content"}}}
	for _, m := range fixture.members {
		content.members = append(content.members, &regularTemplateContent{base: m.base, version: m.version})
	}
	bindings, err := content.versionBindings()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(bindings)
	if strings.Contains(string(raw), "sensitive") || strings.Contains(string(raw), "body") {
		t.Fatal("content leaked")
	}
	var restored cvk.NetworkControllerConfigurationDependencyVersions
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	graph, err := parseCompositeDeploymentBindings(&restored)
	if err != nil || graph.parent.baseID != "composite" || len(graph.members) != 2 || graph.members[1].version.ID != "telemetry-committed" {
		t.Fatal("lost ordered version identities", err)
	}
	for _, mode := range []string{"kind", "empty", "over-limit", "duplicate", "cross-id", "draft", "same-id", "invalid-id"} {
		t.Run(mode, func(t *testing.T) {
			value := bindings.DeepCopy()
			switch mode {
			case "kind":
				value.Kind = "unknown"
			case "empty":
				value.Entries = value.Entries[:1]
			case "over-limit":
				value.Entries = make([]cvk.NetworkControllerConfigurationDependencyVersion, 1002)
			case "duplicate":
				value.Entries[2] = value.Entries[1]
			case "cross-id":
				value.Entries[2].ID = value.Entries[1].VersionID
			case "draft":
				value.Entries[1].Version = 0
			case "same-id":
				value.Entries[1].VersionID = value.Entries[1].ID
			case "invalid-id":
				value.Entries[1].ID = "../member"
			}
			if got, err := parseCompositeDeploymentBindings(value); err == nil || got != nil {
				t.Fatal("invalid durable binding accepted")
			}
		})
	}
}
