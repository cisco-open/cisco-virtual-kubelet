// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerReportSeparatesRecognitionFromExecution(t *testing.T) {
	input := `catalyst_center:
  sites:
    areas:
      - name: lab
        parent_name: Global
  inventory:
    devices: []
  authentication_templates: []
  feature_templates:
    wireless: {}
  system_settings:
    authentication_and_policy_servers:
      ise:
        password: NEVER-PRINT-THIS
`
	var stdout, stderr bytes.Buffer
	args := []string{"controller-report", "--model-version", "0.5.0"}
	if code := run(args, strings.NewReader(input), &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d: %s", code, &stderr)
	}
	if strings.Contains(stdout.String()+stderr.String(), "NEVER-PRINT-THIS") {
		t.Fatal("input value disclosed")
	}
	var report controllerParityReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ConfigurationParity || report.Validation != "section-boundary-only" || len(report.Sections) != 11 {
		t.Fatalf("unexpected parity: %+v", report)
	}
	count := 0
	for _, section := range report.Sections {
		if section.Present {
			count++
		}
		if section.ConfigurationRead || section.Plan || section.Apply || section.Prune {
			t.Fatalf("unimplemented configuration capability advertised: %+v", section)
		}
	}
	if count != 5 {
		t.Fatalf("present=%d", count)
	}
	stdout.Reset()
	if code := run(append(args, "--require-config-parity"), strings.NewReader(input), &stdout, &stderr); code != 3 {
		t.Fatalf("parity gate code=%d", code)
	}
}

func TestControllerReportRejectsAmbiguousInputsWithoutEchoingValues(t *testing.T) {
	cases := []string{
		"", "{}", "null", "[]", "catalyst_center: {}", "catalyst_center: null",
		"sites: {}", "catalyst_center: {sites: {}}\ndefaults: {}",
		"catalyst_center: {sites: null}", "catalyst_center: {sites: 12}",
		"catalyst_center: {sites: {}, sites: {}}",
		"catalyst_center: {sites: {name: first, name: NEVER-PRINT-THIS}}",
		"catalyst_center: {sites: &alias {}}", "catalyst_center: {sites: *alias}",
		"catalyst_center: {sites: {<<: {x: 1}}}",
		"catalyst_center: {sites: {12: abc}}",
		"catalyst_center: {sites: {}}\n---\ncatalyst_center: {fabric: {}}",
		"catalyst_center: {sites: {}}\n---\n",
		"catalyst_center: {sites: [NEVER-PRINT-THIS",
		"catalyst_center: {sites: " + strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70) + "}",
		strings.Repeat(" ", controllerInputLimit+1),
	}
	for i, input := range cases {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"controller-report", "--model-version", "0.5.0"}, strings.NewReader(input), &stdout, &stderr); code != 1 {
			t.Errorf("case %d: code=%d", i, code)
		}
		if stdout.Len() != 0 || strings.Contains(stderr.String(), "NEVER-PRINT-THIS") {
			t.Errorf("case %d: leaked input/partial report", i)
		}
	}
}

func TestControllerReportUnknownSectionsAreNotSilentlyDropped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"controller-report", "--model-version", "0.5.0"}, strings.NewReader(`{"catalyst_center":{"sites":{},"future_feature":{},"another_feature":[]}}`), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d stderr=%s", code, &stderr)
	}
	var report controllerParityReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ConfigurationParity || strings.Join(report.UnknownSections, ",") != "another_feature,future_feature" {
		t.Fatalf("unknowns: %+v", report)
	}
}

func TestControllerReportRequiresExactModelVersion(t *testing.T) {
	for _, args := range [][]string{
		{"controller-report"},
		{"controller-report", "--model-version", "latest"},
		{"controller-report", "--model-version", "0.4.6"},
		{"controller-report", "--model-version", "0.5.0", "a", "b"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, strings.NewReader("catalyst_center: {sites: {}}"), &stdout, &stderr); code != 2 {
			t.Fatalf("%v: code=%d", args, code)
		}
	}
}

func TestControllerReportTemplateReferenceCheck(t *testing.T) {
	input := `catalyst_center:
  templates:
    projects:
      - name: NEVER-PRINT-PROJECT
        dayn_templates:
          - name: regular
          - name: composite
            composite: true
            containing_templates: [regular]
`
	args := []string{"controller-report", "--model-version", "0.5.0", "--check-template-references", "--require-config-parity"}
	var out, errs bytes.Buffer
	if code := run(args, strings.NewReader(input), &out, &errs); code != 3 {
		t.Fatalf("reference validation falsely established parity: code=%d %s", code, &errs)
	}
	var report controllerParityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.TemplateReferencesChecked || report.ConfigurationParity {
		t.Fatal("wrong validation claim")
	}
	if strings.Contains(out.String()+errs.String(), "NEVER-PRINT") {
		t.Fatal("project name disclosed")
	}
	out.Reset()
	errs.Reset()
	bad := strings.Replace(input, "[regular]", "[NEVER-PRINT-MISSING]", 1)
	if code := run(args, strings.NewReader(bad), &out, &errs); code != 1 {
		t.Fatalf("invalid reference accepted: %d", code)
	}
	if strings.Contains(out.String()+errs.String(), "NEVER-PRINT") {
		t.Fatal("bad reference disclosed")
	}
}

func TestControllerReportTemplateAssets(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "regular.j2"), []byte("NEVER-PRINT-CONTENT\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := `catalyst_center: {templates: {projects: [{name: Test, dayn_templates: [{name: regular}]}]}}`
	args := []string{"controller-report", "--model-version", "0.5.0", "--template-directory", directory, "--require-config-parity"}
	var out, errs bytes.Buffer
	if code := run(args, strings.NewReader(input), &out, &errs); code != 3 {
		t.Fatalf("code=%d %s", code, &errs)
	}
	var report controllerParityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.TemplateReferencesChecked || !report.TemplateContentsChecked || report.ConfigurationParity {
		t.Fatal("incorrect content capability claim")
	}
	if strings.Contains(out.String()+errs.String(), "NEVER-PRINT") {
		t.Fatal("template content exposed")
	}
	if err := os.Symlink(filepath.Join(directory, "regular.j2"), filepath.Join(directory, "linked.vtl")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errs.Reset()
	if code := run(args, strings.NewReader(input), &out, &errs); code != 1 {
		t.Fatal("symlink input accepted")
	}
	if strings.Contains(out.String()+errs.String(), directory) {
		t.Fatal("filesystem path exposed")
	}
}
