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

package writers

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/validation"
)

// ──────────────────────────────────────────────────────────────────
// Per-release fixture harness
//
// Layout: fixtures/<release-tag>/<family>/<case>/{desired.yaml,
// observed.json, expected_ops.json}
//
// For each case the harness sets the device version that maps to
// release-tag, looks up the writer for family, and asserts that
// writer.Diff(desired, observed) produces the expected ops slice.
//
// Op-comparison is semantic: bodies are normalised via JSON
// round-trip so map key ordering does not cause spurious failures.
//
// Adding a fixture is a no-code change: drop three files in a new
// case directory and the test picks it up automatically.
// ──────────────────────────────────────────────────────────────────

//go:embed fixtures
var fixturesFS embed.FS

// fixtureOp is the on-disk shape of one transport.Op entry in
// expected_ops.json. Verb and Path are strings; Body is a structured
// value that JSON-encodes to the same bytes the writer emits.
type fixtureOp struct {
	Verb string `json:"verb"`
	Path string `json:"path"`
	Body any    `json:"body"`
}

func TestFixturesAgainstRegisteredWriters(t *testing.T) {
	// Each case uses a per-device writer resolver built from the
	// release-tag exemplar device version.
	cases := collectFixtureCases(t)
	if len(cases) == 0 {
		t.Fatal("no fixtures discovered under fixtures/")
	}

	for _, fc := range cases {
		t.Run(fc.subtestName(), func(t *testing.T) {
			runFixtureCase(t, fc)
		})
	}
}

type fixtureCase struct {
	releaseTag string // "1716"
	family     string // "snmp_server"
	caseName   string // "basic"
	dir        string // "fixtures/1716/snmp_server/basic"
}

func (fc fixtureCase) subtestName() string {
	return fc.releaseTag + "/" + fc.family + "/" + fc.caseName
}

func collectFixtureCases(t *testing.T) []fixtureCase {
	t.Helper()
	var out []fixtureCase
	err := fs.WalkDir(fixturesFS, "fixtures", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		// We want directories that contain a desired.yaml.
		desired := path.Join(p, "desired.yaml")
		if _, err := fs.Stat(fixturesFS, desired); err != nil {
			return nil
		}
		// Path looks like "fixtures/<release>/<family>/<case>".
		parts := strings.Split(p, "/")
		if len(parts) != 4 || parts[0] != "fixtures" {
			return nil
		}
		out = append(out, fixtureCase{
			releaseTag: parts[1],
			family:     parts[2],
			caseName:   parts[3],
			dir:        p,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixtures: %v", err)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].subtestName() < out[j].subtestName()
	})
	return out
}

func runFixtureCase(t *testing.T, fc fixtureCase) {
	t.Helper()

	// Resolve the device version for this release tag.
	dev, ok := ExemplarDeviceVersionForReleaseTag(fc.releaseTag)
	if !ok {
		t.Skipf("no exemplar device version for release tag %q; add it to deviceVersionToReleaseTag", fc.releaseTag)
	}

	// Look up the writer.
	w := GetForRelease(fc.family, dev)
	if w == nil {
		t.Fatalf("no writer registered for family %q", fc.family)
	}

	desired := mustLoadYAML(t, path.Join(fc.dir, "desired.yaml"))
	observed := mustLoadJSON(t, path.Join(fc.dir, "observed.json"))
	expected := mustLoadExpectedOps(t, path.Join(fc.dir, "expected_ops.json"))

	gotOps, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("writer.Diff: %v", err)
	}
	validator := validation.NewStructuralValidator()
	vctx := validation.Context{
		Family:        fc.family,
		DeviceVersion: dev,
		ReleaseTag:    fc.releaseTag,
		AllowedPaths:  w.YANGPaths(),
	}
	for i, op := range gotOps {
		if err := validator.ValidateOperation(vctx, op); err != nil {
			t.Fatalf("generated op[%d] failed YANG validation: %v", i, err)
		}
	}

	if len(gotOps) != len(expected) {
		t.Fatalf("op count: got %d, want %d\n  got: %s\n  want: %s",
			len(gotOps), len(expected), dumpOps(gotOps), dumpFixtureOps(expected))
	}
	for i, want := range expected {
		got := gotOps[i]
		if string(got.Verb) != want.Verb {
			t.Errorf("op[%d].Verb: got %q, want %q", i, got.Verb, want.Verb)
		}
		if got.Path != want.Path {
			t.Errorf("op[%d].Path: got %q, want %q", i, got.Path, want.Path)
		}
		// Semantic body comparison: round-trip both sides through
		// JSON normalisation. Map key ordering doesn't matter; field
		// presence + values do.
		gotBody := normaliseJSON(t, got.Body)
		wantBody, err := json.Marshal(want.Body)
		if err != nil {
			t.Fatalf("op[%d]: marshal expected body: %v", i, err)
		}
		wantBodyNorm := normaliseJSON(t, wantBody)
		if !reflect.DeepEqual(gotBody, wantBodyNorm) {
			t.Errorf("op[%d].Body mismatch\n  got:  %s\n  want: %s",
				i, marshalIndent(gotBody), marshalIndent(wantBodyNorm))
		}
	}
}

// mustLoadYAML loads an embedded YAML file as any (map[string]any
// for objects, []any for arrays, scalars otherwise).
func mustLoadYAML(t *testing.T, p string) any {
	t.Helper()
	raw, err := fixturesFS.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	var out any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", p, err)
	}
	return out
}

// mustLoadJSON loads an embedded JSON file as any.
func mustLoadJSON(t *testing.T, p string) any {
	t.Helper()
	raw, err := fixturesFS.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", p, err)
	}
	return out
}

func mustLoadExpectedOps(t *testing.T, p string) []fixtureOp {
	t.Helper()
	raw, err := fixturesFS.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	var out []fixtureOp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", p, err)
	}
	return out
}

// normaliseJSON re-marshals input into a canonical JSON form. Input
// can be []byte (already JSON) or a Go value. Output is the byte
// representation suitable for reflect.DeepEqual after re-decoding.
func normaliseJSON(t *testing.T, v any) any {
	t.Helper()
	var raw []byte
	switch tv := v.(type) {
	case []byte:
		raw = tv
	case json.RawMessage:
		raw = tv
	default:
		var err error
		raw, err = json.Marshal(tv)
		if err != nil {
			t.Fatalf("marshal for normalise: %v", err)
		}
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal for normalise: %v", err)
	}
	return out
}

func marshalIndent(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func dumpOps(ops []transport.Op) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		parts[i] = fmt.Sprintf("{Verb:%s Path:%s Body:%s}", o.Verb, o.Path, string(o.Body))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func dumpFixtureOps(ops []fixtureOp) string {
	b, _ := json.Marshal(ops)
	return string(b)
}
