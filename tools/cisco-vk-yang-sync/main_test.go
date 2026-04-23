// Copyright © 2026 Cisco Systems, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleFamilies = `
vlan:
  yang_paths: [/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list]
  shape: keyed_list
  key_fields: [id]
vrf:
  yang_paths: [/Cisco-IOS-XE-native:native/vrf/definition]
  shape: keyed_list
  key_fields: [name]
`

func writeTempIndex(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "families.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write tempfile: %v", err)
	}
	return p
}

func TestDryRunReportsLoadedFamilies(t *testing.T) {
	idx := writeTempIndex(t, sampleFamilies)

	var out, errBuf bytes.Buffer
	code := run([]string{"--family-index", idx}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr=%q)", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "2 families loaded") {
		t.Errorf("stdout missing family count:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "- vlan ->") {
		t.Errorf("stdout missing vlan entry:\n%s", out.String())
	}
}

func TestMissingFamilyIndex(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"--family-index", "/does/not/exist.yaml"}, &out, &errBuf)
	if code != exitBadInput {
		t.Fatalf("exit code = %d, want exitBadInput", code)
	}
	if !strings.Contains(errBuf.String(), "family index not found") {
		t.Errorf("stderr missing diagnostic:\n%s", errBuf.String())
	}
}

func TestNonDryRunRejected(t *testing.T) {
	idx := writeTempIndex(t, sampleFamilies)

	var out, errBuf bytes.Buffer
	code := run([]string{"--family-index", idx, "--dry-run=false"}, &out, &errBuf)
	if code != exitNotYet {
		t.Fatalf("exit code = %d, want exitNotYet", code)
	}
	if !strings.Contains(errBuf.String(), "Phase-1") {
		t.Errorf("stderr missing Phase-1 hint:\n%s", errBuf.String())
	}
}

func TestRunAgainstCommittedFamilyIndex(t *testing.T) {
	// Exercising the real checked-in index guards against accidental YAML
	// breakage: any future change that produces a parse error here fails
	// this test, not a runtime failure on a developer's first invocation.
	var out, errBuf bytes.Buffer
	code := run(
		[]string{"--family-index", "../../internal/drivers/iosxe/configdriver/schema/families.yaml"},
		&out, &errBuf,
	)
	if code != exitOK {
		t.Fatalf("exit code = %d (stderr=%q)", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "8 families loaded") {
		t.Errorf("expected 8 families loaded from real index, got:\n%s", out.String())
	}
}

// Sanity: parseFlags never writes to stdout; helps CI assert no flag-printed
// banners leak into the real output.
func TestParseFlagsSilentOnValidInput(t *testing.T) {
	var errBuf bytes.Buffer
	if _, err := parseFlags([]string{"--yang-version", "1791"}, &errBuf); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("parseFlags wrote to stderr: %q", errBuf.String())
	}
	_ = io.Discard
}
