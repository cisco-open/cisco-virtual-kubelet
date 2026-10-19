// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package nacmodel

import (
	"strings"
	"testing"
)

const referenceModel = `catalyst_center:
  templates:
    projects:
      - name: One
        dayn_templates:
          - name: shared
          - name: unique
          - name: combined
            composite: true
            containing_templates: [shared, unique]
      - name: Two
        dayn_templates:
          - name: shared
`

func TestTemplateReferenceGraph(t *testing.T) {
	index, err := IndexTemplates([]byte(referenceModel))
	if err != nil {
		t.Fatal(err)
	}
	composite := index.Templates["One#combined"]
	if len(composite.Members) != 2 || composite.Members[0] != "One#shared" || composite.Members[1] != "One#unique" {
		t.Fatal("member resolution differs from upstream")
	}
	for _, tc := range []struct {
		ref, key        string
		external, valid bool
	}{
		{"unique", "One#unique", false, true}, {"shared", "", false, false}, {"Two#shared", "Two#shared", false, true}, {"External#Existing", "External#Existing", true, true}, {"missing", "", false, false}, {"One#two#three", "", false, false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			got, external, err := index.ResolveReference(tc.ref)
			if (err == nil) != tc.valid {
				t.Fatal("unexpected validity", err)
			}
			if tc.valid && (got.Key() != tc.key || external != tc.external) {
				t.Fatal("wrong target or ownership")
			}
		})
	}
}
func TestTemplateReferenceRejectsInvalidGraphs(t *testing.T) {
	for _, model := range []string{
		strings.Replace(referenceModel, "[shared, unique]", "[combined]", 1),
		strings.Replace(referenceModel, "[shared, unique]", "[shared, shared]", 1),
		strings.Replace(referenceModel, "[shared, unique]", "[absent]", 1),
		strings.Replace(referenceModel, "name: unique", "name: shared", 1),
		strings.Replace(referenceModel, "composite: true", "composite: invalid", 1),
		strings.Replace(referenceModel, "dayn_templates:", "onboarding_templates:", 1),
		strings.Replace(referenceModel, "[shared, unique]", "[Two#shared]", 1),
	} {
		if _, err := IndexTemplates([]byte(model)); err == nil {
			t.Fatal("invalid template graph accepted")
		}
	}
}
