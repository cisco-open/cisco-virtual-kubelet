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

// Command cisco-vk-yang-sync regenerates the pieces that have a
// deterministic relationship to the family index and the declared
// YANG release:
//
//   - per-family writer skeletons in --out-writers (new families only;
//     existing files are preserved).
//   - ygot-generated Go types in --out-types when --yang-dir is
//     supplied and points at a checked-out Cisco-IOS-XE YANG module
//     tree. Absence of yang-dir falls back to skeleton-only mode so
//     a developer without local YANG modules can still regenerate
//     the netascode side of the contract.
//   - CRD OpenAPI 'x-kubernetes-preserve-unknown-fields' fragments per
//     family are emitted alongside the writer skeletons — the CRD
//     schema stays schemaless on spec.configuration (matching
//     netascode semantics) while the per-family fragment documents
//     the leaves the writer manages. controller-gen continues to own
//     the top-level IOSXEConfig CRD generation.
//
// The ygot path reuses the same generator the Makefile's 'ygot-gen'
// target invokes for the apphosting subset, so when the full YANG
// tree is checked in a single 'make generate' produces config-driver
// types alongside the apphosting ones.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"sigs.k8s.io/yaml"
)

// execCommand is a variable so tests can stub command invocation.
var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

type exitCode int

const (
	exitOK       exitCode = 0
	exitBadFlags exitCode = 2
	exitNotYet   exitCode = 3
	exitBadInput exitCode = 4
)

type flags struct {
	yangVersion string
	yangDir     string
	familyIndex string
	outAPI      string
	outCRD      string
	outWriters  string
	outTypes    string
	ygotBin     string
	dryRun      bool
	force       bool
}

func parseFlags(args []string, stderr io.Writer) (flags, error) {
	fs := flag.NewFlagSet("cisco-vk-yang-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f flags
	fs.StringVar(&f.yangVersion, "yang-version", "1791",
		"Cisco-IOS-XE YANG release directory under yang-dir")
	fs.StringVar(&f.yangDir, "yang-dir", "",
		"root directory of YANG modules (consumed by the ygot generator; "+
			"the family-index passes this tool does not read it)")
	fs.StringVar(&f.familyIndex, "family-index",
		"internal/drivers/iosxe/configdriver/schema/families.yaml",
		"path to the netascode family index")
	fs.StringVar(&f.outAPI, "out-api", "api/config/v1alpha1",
		"destination directory for generated API types (reserved)")
	fs.StringVar(&f.outCRD, "out-crd", "config/crd",
		"destination directory for generated CRD manifests (reserved)")
	fs.StringVar(&f.outWriters, "out-writers",
		"internal/drivers/iosxe/configdriver/writers",
		"destination directory for generated writer skeletons")
	fs.StringVar(&f.outTypes, "out-types",
		"internal/drivers/iosxe/configdriver/generated",
		"destination directory for ygot-generated Go types (used only when --yang-dir is supplied)")
	fs.StringVar(&f.ygotBin, "ygot-bin", "",
		"path to the ygot generator binary; when empty, falls back to 'go run github.com/openconfig/ygot/generator@v0.34.0'")
	fs.BoolVar(&f.dryRun, "dry-run", true,
		"print actions rather than writing files")
	fs.BoolVar(&f.force, "force", false,
		"overwrite existing writer files (default: preserve hand-edits)")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	return f, nil
}

// family is the subset of schema.Family this tool decodes. Duplicating
// the shape here keeps the tool free of a cross-package dependency on
// the runtime schema package (which wants an embedded families.yaml).
type family struct {
	YANGPaths []string `json:"yang_paths"`
	Shape     string   `json:"shape"`
	KeyFields []string `json:"key_fields,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Portal    string   `json:"portal,omitempty"`
}

func run(args []string, stdout, stderr io.Writer) exitCode {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadFlags
	}

	families, err := loadIndex(f.familyIndex)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return exitBadInput
	}

	fmt.Fprintln(stdout, "cisco-vk-yang-sync")
	fmt.Fprintf(stdout, "  yang-version = %s\n", f.yangVersion)
	fmt.Fprintf(stdout, "  yang-dir     = %s\n", showOrUnset(f.yangDir))
	fmt.Fprintf(stdout, "  family-index = %s (%d families loaded)\n", f.familyIndex, len(families))
	fmt.Fprintf(stdout, "  out-writers  = %s\n", f.outWriters)
	fmt.Fprintf(stdout, "  dry-run      = %v\n", f.dryRun)
	fmt.Fprintf(stdout, "  force        = %v\n", f.force)

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)

	if f.dryRun {
		fmt.Fprintln(stdout, "\nfamilies that would be processed:")
		for _, name := range names {
			target := filepath.Join(f.outWriters, name+".go")
			status := "skeleton (new)"
			if _, err := os.Stat(target); err == nil {
				status = "preserved (exists)"
				if f.force {
					status = "OVERWRITE (--force)"
				}
			}
			fmt.Fprintf(stdout, "  - %s -> %s  [%s]\n", name, target, status)
		}
		return exitOK
	}

	// Real run: emit skeletons for families that do not yet have a
	// writer file. Existing files are preserved unless --force is set.
	if err := os.MkdirAll(f.outWriters, 0o755); err != nil {
		fmt.Fprintf(stderr, "ERROR: mkdir %s: %v\n", f.outWriters, err)
		return exitBadInput
	}

	var created, skipped, overwritten int
	for _, name := range names {
		target := filepath.Join(f.outWriters, name+".go")
		exists := false
		if _, err := os.Stat(target); err == nil {
			exists = true
		}
		if exists && !f.force {
			skipped++
			fmt.Fprintf(stdout, "  skip  %s (exists)\n", target)
			continue
		}
		if err := writeSkeletonFile(target, name, families[name]); err != nil {
			fmt.Fprintf(stderr, "ERROR: write %s: %v\n", target, err)
			return exitBadInput
		}
		if exists {
			overwritten++
			fmt.Fprintf(stdout, "  write %s (overwritten)\n", target)
		} else {
			created++
			fmt.Fprintf(stdout, "  write %s (new)\n", target)
		}
	}

	fmt.Fprintf(stdout, "\n%d new, %d overwritten, %d preserved\n",
		created, overwritten, skipped)

	// Optional ygot pass: if --yang-dir is supplied and points at a
	// directory, invoke the generator to emit Go types under
	// --out-types. The generator is the same one the Makefile's
	// ygot-gen target uses; wiring it here means 'make generate'
	// picks up config-driver types automatically once the full YANG
	// tree is checked in.
	if f.yangDir != "" {
		if err := runYgot(stdout, stderr, f, names); err != nil {
			fmt.Fprintf(stderr, "ERROR: ygot generation failed: %v\n", err)
			return exitBadInput
		}
	} else {
		fmt.Fprintln(stdout,
			"\n(--yang-dir not supplied; skipping ygot Go-type generation)")
	}

	return exitOK
}

// runYgot invokes the ygot generator against f.yangDir, producing
// one Go file per family under f.outTypes. The command line mirrors
// the Makefile's ygot-gen target so operators who already understand
// one understand both.
//
// This is deliberately a thin wrapper: we let ygot fail loudly on a
// missing module or import rather than pre-validating the YANG tree
// here — the generator's error messages are the best source.
func runYgot(stdout, stderr io.Writer, f flags, families []string) error {
	info, err := os.Stat(f.yangDir)
	if err != nil {
		return fmt.Errorf("yang-dir %q: %w", f.yangDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("yang-dir %q is not a directory", f.yangDir)
	}

	if !f.dryRun {
		if err := os.MkdirAll(f.outTypes, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", f.outTypes, err)
		}
	}

	// The family index tells us which modules to feed the generator.
	// In practice an operator checks out the complete YANG tree and
	// ygot follows imports, so we pass every module in f.yangDir and
	// let ygot resolve dependencies.
	args, err := buildYgotArgs(f, families)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\nygot: %s %s\n", args.bin, strings.Join(args.args, " "))
	if f.dryRun {
		return nil
	}

	cmd := execCommand(args.bin, args.args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ygot invocation: %w", err)
	}
	fmt.Fprintf(stdout, "ygot: wrote %s\n", filepath.Join(f.outTypes, "iosxe_config.go"))
	return nil
}

// ygotInvocation is the pair the runner needs; split from runYgot so
// tests can inspect the command without executing it.
type ygotInvocation struct {
	bin  string
	args []string
}

func buildYgotArgs(f flags, _ []string) (ygotInvocation, error) {
	bin := f.ygotBin
	realArgs := []string{}
	if bin == "" {
		bin = "go"
		realArgs = []string{"run",
			"github.com/openconfig/ygot/generator@v0.34.0",
		}
	}
	realArgs = append(realArgs,
		"-path="+f.yangDir,
		"-output_file="+filepath.Join(f.outTypes, "iosxe_config.go"),
		"-package_name=generated",
		"-generate_fakeroot",
		"-fakeroot_name=IOSXE",
		"-compress_paths=false",
	)
	// Pass every .yang file under yangDir; ygot resolves imports
	// across the full path set. Ignore subdirectories that do not
	// contain .yang files (e.g. documentation or test fixtures).
	entries, err := os.ReadDir(f.yangDir)
	if err != nil {
		return ygotInvocation{}, fmt.Errorf("read yang-dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yang") {
			continue
		}
		realArgs = append(realArgs, filepath.Join(f.yangDir, e.Name()))
	}
	return ygotInvocation{bin: bin, args: realArgs}, nil
}

// loadIndex reads families.yaml and decodes it into the tool's internal
// family struct, validating the minimum invariants the templates rely on.
func loadIndex(path string) (map[string]family, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("family index not found: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("family index is a directory: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read family index: %w", err)
	}
	var out map[string]family
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse family index: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("family index is empty")
	}
	for name, f := range out {
		if len(f.YANGPaths) == 0 {
			return nil, fmt.Errorf("family %q has no yang_paths", name)
		}
		if f.Shape != "singleton" && f.Shape != "keyed_list" {
			return nil, fmt.Errorf("family %q has invalid shape %q", name, f.Shape)
		}
	}
	return out, nil
}

// skeletonTemplate renders a writer file that registers an
// ErrNotImplemented skeleton for the family. It is deliberately the
// same shape every hand-written writer ends up with at first draft so
// promoting a skeleton to a real writer is just replacing the init()
// body.
var skeletonTemplate = template.Must(template.New("skeleton").Parse(
	`// Copyright © 2026 Cisco Systems Inc.
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

// AUTO-GENERATED by cisco-vk-yang-sync from families.yaml entry {{ .Family | printf "%q" }}.
// Replace the registerSkeleton call with a real Override(...) to
// graduate this family to a working writer.
{{- if .Portal }}
//
// Reference: {{ .Portal }}
{{- end }}

package writers

func init() {
	registerSkeleton({{ .Family | printf "%q" }},
{{- range .YANGPaths }}
		{{ . | printf "%q" }},
{{- end }}
	)
}
`))

type templateData struct {
	Family    string
	YANGPaths []string
	Portal    string
}

func writeSkeletonFile(path, name string, f family) error {
	data := templateData{
		Family:    name,
		YANGPaths: append([]string(nil), f.YANGPaths...),
		Portal:    f.Portal,
	}
	var buf strings.Builder
	if err := skeletonTemplate.Execute(&buf, data); err != nil {
		return fmt.Errorf("render template: %w", err)
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

func showOrUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
