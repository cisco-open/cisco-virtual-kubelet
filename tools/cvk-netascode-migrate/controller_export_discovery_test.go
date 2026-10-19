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

const exportDiscoveryInput = `{"catalyst_center":{"sites":{"areas":[{"name":"Lab","parent_name":"Global"}]},"inventory":{"discovery":[{"name":"PRIVATE_JOB_NAME","type":"SINGLE","ip_address_list":"192.0.2.1","protocol_order":"SSH","global_credential_list":["PRIVATE_CREDENTIAL_NAME"],"create_per_site":true},{"name":"GLOBAL_JOB","type":"SINGLE","ip_address_list":"192.0.2.2","protocol_order":"SSH","global_credential_list":["PRIVATE_CREDENTIAL_NAME"]}]}}}`

func TestControllerExportDiscovery(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "resolved.json")
	args := []string{"controller-export-discovery", "--model-version", "0.5.0", "--output", output, "--managed-sites", `["Global/Lab"]`}
	var stdout, stderr bytes.Buffer
	if run(args, strings.NewReader(exportDiscoveryInput), &stdout, &stderr) != 0 {
		t.Fatal(stderr.String())
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("output not private", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil || strings.Contains(string(raw), "GLOBAL_JOB") || strings.Contains(string(raw), "create_per_site") || !strings.Contains(string(raw), "PRIVATE_JOB_NAME") {
		t.Fatal("wrong selected model")
	}
	var meta map[string]any
	if json.Unmarshal(stdout.Bytes(), &meta) != nil || meta["selectedJobs"] != float64(1) || meta["perSiteMode"] != true || meta["configurationParity"] != false {
		t.Fatal("incorrect provenance report")
	}
	if strings.Contains(stdout.String(), "PRIVATE_JOB_NAME") || strings.Contains(stdout.String(), "PRIVATE_CREDENTIAL_NAME") {
		t.Fatal("stdout disclosed model values")
	}
	stdout.Reset()
	stderr.Reset()
	if run(args, strings.NewReader(exportDiscoveryInput), &stdout, &stderr) == 0 {
		t.Fatal("existing output overwritten")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(raw, after) {
		t.Fatal("existing output changed")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(output, link); err != nil {
		t.Fatal(err)
	}
	args[4] = link
	if run(args, strings.NewReader(exportDiscoveryInput), &stdout, &stderr) == 0 {
		t.Fatal("symlink output followed")
	}
}
func TestControllerExportDiscoveryRefusal(t *testing.T) {
	for _, flags := range [][]string{{}, {"--model-version", "latest", "--output", "-"}, {"--model-version", "0.5.0", "--output", "-"}, {"--model-version", "0.5.0", "--output", filepath.Join(t.TempDir(), "x"), "--managed-sites", "null"}, {"--model-version", "0.5.0", "--output", filepath.Join(t.TempDir(), "x"), "--managed-sites", `["Global/Missing"]`}} {
		var out, err bytes.Buffer
		if run(append([]string{"controller-export-discovery"}, flags...), strings.NewReader(exportDiscoveryInput), &out, &err) == 0 || out.Len() != 0 {
			t.Fatal("invalid command succeeded or printed source")
		}
	}
	path := filepath.Join(t.TempDir(), "refused.json")
	bad := strings.Replace(exportDiscoveryInput, `"GLOBAL_JOB"`, `"GLOBAL_JOB","password_list":["DO_NOT_PRINT"]`, 1)
	var out, err bytes.Buffer
	if run([]string{"controller-export-discovery", "--model-version", "0.5.0", "--output", path, "--managed-sites", `["Global/Lab"]`}, strings.NewReader(bad), &out, &err) == 0 || out.Len() != 0 || strings.Contains(err.String(), "DO_NOT_PRINT") {
		t.Fatal("excluded credential input accepted or leaked")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("invalid input created output")
	}
}

func TestControllerExportDiscoveryDefaults(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "defaults.yaml")
	output := filepath.Join(dir, "resolved.json")
	if err := os.WriteFile(defaults, []byte("defaults:\n  catalyst_center:\n    inventory:\n      discovery:\n        type: SINGLE\n        protocol_order: SSH\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input := strings.ReplaceAll(strings.ReplaceAll(exportDiscoveryInput, `"type":"SINGLE",`, ""), `"protocol_order":"SSH",`, "")
	args := []string{"controller-export-discovery", "--model-version", "0.5.0", "--output", output, "--defaults", defaults, "--managed-sites", `["Global/Lab"]`}
	var out, stderr bytes.Buffer
	if run(args, strings.NewReader(input), &out, &stderr) != 0 {
		t.Fatal(stderr.String())
	}
	raw, err := os.ReadFile(output)
	if err != nil || !strings.Contains(string(raw), `"protocol_order": "SSH"`) || strings.Contains(string(raw), "GLOBAL_JOB") {
		t.Fatal("wrong resolved default selection", err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("resolved defaults not private")
	}
	var meta map[string]any
	if json.Unmarshal(out.Bytes(), &meta) != nil || !strings.HasPrefix(meta["exporter"].(string), "cvk-discovery-defaults-v1:") || meta["appliedDefaultFields"] != float64(4) || meta["selectedJobs"] != float64(1) || meta["configurationParity"] != false || strings.Contains(out.String(), "PRIVATE_CREDENTIAL_NAME") {
		t.Fatal("wrong defaults provenance")
	}
	rejected := filepath.Join(dir, "refused.json")
	args[4] = rejected
	if err := os.WriteFile(defaults, []byte(`{"defaults":{"catalyst_center":{"inventory":{"discovery":{"password_list":["NO_PRINT"]}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if run(args, strings.NewReader(input), &out, &stderr) == 0 || out.Len() != 0 || strings.Contains(stderr.String(), "NO_PRINT") {
		t.Fatal("secret defaults accepted/disclosed")
	}
	if _, err := os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("rejected defaults created output")
	}
}
