// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerExportCredentials(t *testing.T) {
	raw := `catalyst_center:
  network_settings:
    device_credentials:
      https_read_credentials:
        - name: PRIVATE_IDENTIFIER
          username: tester
          password_version: 1
`
	path := filepath.Join(t.TempDir(), "resolved.json")
	var out, errOut bytes.Buffer
	args := []string{"controller-export-credentials", "--model-version", "0.5.0", "--output", path}
	if code := run(args, strings.NewReader(raw), &out, &errOut); code != 0 {
		t.Fatal(code, errOut.String())
	}
	if strings.Contains(out.String(), "PRIVATE_IDENTIFIER") {
		t.Fatal("payload escaped to stdout")
	}
	var meta map[string]any
	if json.Unmarshal(out.Bytes(), &meta) != nil || meta["configurationParity"] != false || meta["executionSyntaxValidated"] != true {
		t.Fatal("wrong provenance")
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(b, []byte(`"port": 443`)) {
		t.Fatal("missing pinned port", err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("not private")
	}
	out.Reset()
	errOut.Reset()
	if run(args, strings.NewReader(raw), &out, &errOut) == 0 {
		t.Fatal("existing file overwritten")
	}
	for _, bad := range []string{strings.Replace(raw, "password_version: 1", "password: PRIVATE_SECRET", 1), strings.Replace(raw, "password_version: 1", "port: null\n          password_version: 1", 1)} {
		dest := filepath.Join(t.TempDir(), "refused.json")
		out.Reset()
		errOut.Reset()
		if run([]string{"controller-export-credentials", "--model-version", "0.5.0", "--output", dest}, strings.NewReader(bad), &out, &errOut) == 0 {
			t.Fatal("invalid input accepted")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) || out.Len() != 0 || strings.Contains(errOut.String(), "PRIVATE_SECRET") {
			t.Fatal("invalid output or secret escaped")
		}
	}
}

func TestControllerExportCredentialOverrides(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "defaults.yaml")
	if err := os.WriteFile(defaults, []byte("defaults:\n  catalyst_center:\n    network_settings:\n      device_credentials:\n        https_read_credentials:\n          port: 8443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := `{"catalyst_center":{"network_settings":{"device_credentials":{"https_read_credentials":[{"name":"PRIVATE_NAME","username":"tester","password_version":1}]}}}}`
	dest := filepath.Join(dir, "resolved.json")
	var out, stderr bytes.Buffer
	args := []string{"controller-export-credentials", "--model-version", "0.5.0", "--defaults", defaults, "--output", dest}
	if code := run(args, strings.NewReader(raw), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	b, _ := os.ReadFile(dest)
	if !bytes.Contains(b, []byte(`"port": 8443`)) || strings.Contains(out.String(), "PRIVATE_NAME") {
		t.Fatal("override output invalid")
	}
	var meta map[string]any
	if json.Unmarshal(out.Bytes(), &meta) != nil || meta["overridesSHA256"] == nil {
		t.Fatal("override provenance missing")
	}
	for _, file := range []string{"-", filepath.Join(dir, "absent"), filepath.Join(dir, "empty")} {
		if strings.HasSuffix(file, "empty") {
			os.WriteFile(file, nil, 0600)
		}
		out.Reset()
		stderr.Reset()
		if run([]string{"controller-export-credentials", "--model-version", "0.5.0", "--defaults", file, "--output", filepath.Join(dir, "refused.json")}, strings.NewReader(raw), &out, &stderr) == 0 {
			t.Fatal("unavailable override accepted")
		}
	}
}

func TestControllerExportCredentialSiteSelection(t *testing.T) {
	raw := `{"catalyst_center":{"network_settings":{},"sites":{"areas":[{"name":"Lab","parent_name":"Global","cli_credentials":"PRIVATE_LOOKUP"},{"name":"Other","parent_name":"Global","cli_credentials":"other"}]}}}`
	file := filepath.Join(t.TempDir(), "selected.json")
	var out, stderr bytes.Buffer
	args := []string{"controller-export-credentials", "--model-version", "0.5.0", "--managed-sites", `["Global/Lab"]`, "--manage-specific-sites-only", "--output", file}
	if code := run(args, strings.NewReader(raw), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	b, err := os.ReadFile(file)
	if err != nil || !bytes.Contains(b, []byte("PRIVATE_LOOKUP")) || bytes.Contains(b, []byte(`"Other"`)) || bytes.Contains(b, []byte("device_credentials")) || strings.Contains(out.String(), "PRIVATE_LOOKUP") {
		t.Fatal("wrong selected payload", err)
	}
	var meta struct {
		SiteSelection struct {
			SelectedDefinitions int  `json:"selectedDefinitions"`
			SelectedAssignments int  `json:"selectedAssignments"`
			PerSiteMode         bool `json:"perSiteMode"`
		} `json:"siteSelection"`
	}
	if json.Unmarshal(out.Bytes(), &meta) != nil || !meta.SiteSelection.PerSiteMode || meta.SiteSelection.SelectedDefinitions != 0 || meta.SiteSelection.SelectedAssignments != 1 {
		t.Fatal("wrong selection audit")
	}
	for _, v := range []string{`null`, `["Global/Missing"]`, `not-json`} {
		out.Reset()
		stderr.Reset()
		args[4] = v // replace the managed-sites JSON value
		if run(args, strings.NewReader(raw), &out, &stderr) == 0 {
			t.Fatal("invalid managed selection accepted")
		}
	}
}
