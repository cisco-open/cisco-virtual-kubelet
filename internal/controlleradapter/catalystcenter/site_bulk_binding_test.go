// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSiteBulkBindingRetainsExactSelectionThroughPartialResult(t *testing.T) {
	desired, err := parseSites([]byte(executionSiteModel))
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleSites()[:1]
	binding, err := newSiteBulkBinding(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := binding.resource(actual)
	if err != nil || len(resource) > 512 {
		t.Fatal("invalid binding", err)
	}
	for i := 0; i < 3; i++ {
		mutation, err := nextSiteMutation(desired, actual)
		if err != nil || mutation == nil {
			t.Fatal(err)
		}
		var created Site
		if err := json.Unmarshal(mutation.body, &created); err != nil {
			t.Fatal(err)
		}
		created.ID, created.NameHierarchy, created.Type = fmt.Sprintf("bulk-created-%d", i), mutation.desired.path, mutation.desired.kind
		actual = append(actual, created)
		// Reconstruct from only immutable source and retained resource, as a
		// worker restart must; do not infer a new batch from remaining drift.
		recovered, err := recoverSiteBulkBinding(desired, resource)
		if err != nil || !recovered.selected(0) || !recovered.selected(1) || !recovered.selected(2) {
			t.Fatal("partial result lost original selection", err)
		}
		got, err := recovered.resource(actual)
		if err != nil || got != resource {
			t.Fatal("newly observed identities changed baseline binding", err)
		}
		verified, err := verifySiteBulkBinding(desired, actual, resource)
		if err != nil || verified != (i == 2) {
			t.Fatalf("partial batch %d verified=%v error=%v", i, verified, err)
		}
	}
	actual[0].ID = "replaced-root"
	if verified, err := verifySiteBulkBinding(desired, actual, resource); err == nil || verified {
		t.Fatal("replaced anchor accepted")
	}
}

func TestSiteBulkBindingPinsExistingMembersAndRejectsMalformedSelection(t *testing.T) {
	desired, err := parseSites([]byte(`catalyst_center: {sites: {areas: [{name: Lab, parent_name: Global}, {name: New, parent_name: Global}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleSites()[:2]
	binding, err := newSiteBulkBinding(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := binding.resource(actual)
	if err != nil {
		t.Fatal(err)
	}
	if binding.selected(0) || !binding.selected(1) {
		t.Fatal("already existing member incorrectly selected")
	}
	for _, bad := range []string{
		strings.Replace(resource, ":2:", ":02:", 1),
		strings.Replace(resource, ":Ag:", ":AA:", 1),
		strings.Replace(resource, ":Ag:", ":gg:", 1),
		strings.Replace(resource, ":Ag:", ":Ag=:", 1),
		resource + ":extra",
		strings.ToUpper(resource),
	} {
		if _, err := recoverSiteBulkBinding(desired, bad); err == nil {
			t.Fatal("malformed descriptor accepted")
		}
	}
	actual[1].ID = "replacement"
	if current, err := binding.resource(actual); err == nil && current == resource {
		t.Fatal("preexisting member replacement did not alter binding")
	}
}

func TestSiteBulkBindingMaximumSourceFitsExistingJournal(t *testing.T) {
	desired := make([]desiredSite, 1000)
	for i := range desired {
		desired[i] = desiredSite{kind: "area", path: fmt.Sprintf("Global/Area-%04d", i)}
	}
	binding, err := newSiteBulkBinding(desired, sampleSites()[:1])
	if err != nil {
		t.Fatal(err)
	}
	resource, err := binding.resource(sampleSites()[:1])
	if err != nil || len(resource) > 512 {
		t.Fatal("maximum selection exceeds shared journal bound", err)
	}
	recovered, err := recoverSiteBulkBinding(desired, resource)
	if err != nil || !recovered.selected(999) {
		t.Fatal("maximum selection lost final member", err)
	}
	desired[0].path = "Global/Different"
	if verified, err := verifySiteBulkBinding(desired, sampleSites()[:1], resource); err == nil || verified {
		t.Fatal("changed source member accepted")
	}
}
