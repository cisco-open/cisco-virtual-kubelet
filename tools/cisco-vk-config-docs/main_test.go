// Copyright © 2026 Cisco Systems Inc.
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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunGeneratesEveryFamily verifies the generator walks the real
// checked-in families.yaml and produces one markdown per family plus
// a README.md. Renders to a temp dir so the repo docs tree stays
// untouched during tests.
func TestRunGeneratesEveryFamily(t *testing.T) {
	outDir := t.TempDir()
	var out, errBuf bytes.Buffer

	code := run([]string{"--out", outDir}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}

	// Every family in the committed index must produce a page.
	for _, fam := range []string{"vlan", "vrf", "interface_ethernet", "bgp", "username"} {
		p := filepath.Join(outDir, fam+".md")
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !strings.HasPrefix(string(body), "# "+fam+"\n") {
			t.Errorf("%s: missing H1 banner, got:\n%s", p, body)
		}
		if !strings.Contains(string(body), "## YANG paths") {
			t.Errorf("%s: missing YANG paths section", p)
		}
		if strings.HasSuffix(string(body), "\n\n") {
			t.Errorf("%s: generated page has a blank line at EOF", p)
		}
	}

	// Index page exists and lists the families.
	idx, err := os.ReadFile(filepath.Join(outDir, "README.md"))
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if strings.HasSuffix(string(idx), "\n\n") {
		t.Errorf("generated index has a blank line at EOF")
	}
	for _, fam := range []string{"vlan", "bgp"} {
		want := "[" + fam + "](" + fam + ".md)"
		if !strings.Contains(string(idx), want) {
			t.Errorf("index missing link for %q:\n%s", fam, idx)
		}
	}
}

// TestDryRunTouchesNothing verifies the --dry-run flag does not
// create files. Useful for CI preview runs.
func TestDryRunTouchesNothing(t *testing.T) {
	outDir := t.TempDir()
	var out, errBuf bytes.Buffer
	code := run([]string{"--out", outDir, "--dry-run"}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d", code)
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 0 {
		t.Fatalf("dry-run created files: %v", entries)
	}
	if !strings.Contains(out.String(), "would write") {
		t.Errorf("stdout missing dry-run notice:\n%s", out.String())
	}
}

func TestRunPortalDialectMirrorsNetascodeLayout(t *testing.T) {
	// Portal dialect must:
	//   - write each family under data_models/iosxe/<name>/index.md
	//   - emit MkDocs front matter on each page
	//   - cross-link family deps via "../<name>/" relative URLs
	//   - surface OpenConfig paths when the family has them
	outDir := t.TempDir()
	var out, errBuf bytes.Buffer
	code := run([]string{"--out", outDir, "--dialect", "portal"}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}
	body, err := os.ReadFile(filepath.Join(outDir, "data_models", "iosxe", "vlan", "index.md"))
	if err != nil {
		t.Fatalf("read vlan index: %v", err)
	}
	if !strings.Contains(string(body), "title: vlan") {
		t.Errorf("MkDocs front matter missing on vlan: %s", body)
	}
	if !strings.Contains(string(body), "OpenConfig") {
		t.Errorf("OpenConfig section missing on vlan (the family has openconfig_paths):\n%s", body)
	}

	idx, err := os.ReadFile(filepath.Join(outDir, "data_models", "iosxe", "index.md"))
	if err != nil {
		t.Fatalf("read portal index: %v", err)
	}
	if !strings.Contains(string(idx), "title: IOS-XE") {
		t.Errorf("portal index missing front matter:\n%s", idx)
	}
}

func TestRunPortalDialectInvalidValueRejected(t *testing.T) {
	outDir := t.TempDir()
	var out, errBuf bytes.Buffer
	code := run([]string{"--out", outDir, "--dialect", "weird"}, &out, &errBuf)
	if code != exitBadFlags {
		t.Fatalf("exit=%d, want exitBadFlags=%d. stderr=%s", code, exitBadFlags, errBuf.String())
	}
}

func TestSkeletonFamilyMarkedAsSuch(t *testing.T) {
	// There are no skeleton families in the committed tree at this
	// point (every family has a writer). So this test verifies that a
	// known real family lists status=implemented.
	outDir := t.TempDir()
	var out, errBuf bytes.Buffer
	if code := run([]string{"--out", outDir}, &out, &errBuf); code != exitOK {
		t.Fatalf("exit=%d", code)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "vlan.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "Status: **implemented**") {
		t.Errorf("vlan.md missing implemented status:\n%s", body)
	}
}
