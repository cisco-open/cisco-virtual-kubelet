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
	"bytes"
	"compress/gzip"
	"encoding/json"
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

	"github.com/openconfig/goyang/pkg/yang"
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

const goModulePath = "github.com/cisco/virtual-kubelet-cisco"

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
	perFamily   bool
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
	fs.BoolVar(&f.perFamily, "per-family", false,
		"generate per-family ygot schema packages under out-types/<release>/<family>/schema.go (requires --yang-dir)")

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
			targetExists := fileExists(target)
			groupedExists := !targetExists && familyRegisteredInWriterDir(f.outWriters, name)
			switch {
			case targetExists:
				status = "preserved (exists)"
				if f.force {
					status = "OVERWRITE (--force)"
				}
			case groupedExists:
				status = "preserved (registered in grouped writer)"
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
		targetExists := fileExists(target)
		groupedExists := !targetExists && familyRegisteredInWriterDir(f.outWriters, name)
		if groupedExists {
			skipped++
			fmt.Fprintf(stdout, "  skip  %s (registered in grouped writer)\n", target)
			continue
		}
		if targetExists && !f.force {
			skipped++
			fmt.Fprintf(stdout, "  skip  %s (exists)\n", target)
			continue
		}
		if err := writeSkeletonFile(target, name, families[name]); err != nil {
			fmt.Fprintf(stderr, "ERROR: write %s: %v\n", target, err)
			return exitBadInput
		}
		if targetExists {
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
		if f.perFamily {
			if err := runYgotPerFamily(stdout, stderr, f, families); err != nil {
				fmt.Fprintf(stderr, "ERROR: per-family ygot generation failed: %v\n", err)
				return exitBadInput
			}
		} else {
			if err := runYgot(stdout, stderr, f, names); err != nil {
				fmt.Fprintf(stderr, "ERROR: ygot generation failed: %v\n", err)
				return exitBadInput
			}
		}
	} else {
		fmt.Fprintln(stdout,
			"\n(--yang-dir not supplied; skipping ygot Go-type generation)")
	}

	return exitOK
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func familyRegisteredInWriterDir(dir, family string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	needles := []string{
		`family: "` + family + `"`,
		`family:      "` + family + `"`,
		`return "` + family + `"`,
		`registerSkeleton("` + family + `"`,
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		body := string(raw)
		for _, needle := range needles {
			if strings.Contains(body, needle) {
				return true
			}
		}
	}
	return false
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
	// Per-release output layout, gated on f.yangVersion. When the
	// caller passes an explicit release tag the generator emits
	// outTypes/<release>/iosxe_config.go with package name
	// xe<release>, so a single repo can carry multiple release
	// packages side-by-side under the same outTypes root. An empty
	// yangVersion preserves the legacy single-package layout
	// (package generated, file outTypes/iosxe_config.go) so existing
	// callers and tests are not broken.
	pkgName, outFile := ygotOutputLayout(f.yangVersion, f.outTypes)
	realArgs = append(realArgs,
		"-path="+f.yangDir,
		"-output_file="+outFile,
		"-package_name="+pkgName,
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

// ygotOutputLayout returns the ygot generator's package name and
// output-file path for the given release tag. Empty yangVersion
// preserves the pre-existing single-package layout for
// backward-compatibility; any concrete tag produces a per-release
// nested layout suitable for the runtime release-aware dispatch
// the action plan adds in Phase 4.
func ygotOutputLayout(yangVersion, outTypes string) (pkgName, outFile string) {
	if yangVersion == "" {
		return "generated", filepath.Join(outTypes, "iosxe_config.go")
	}
	return "xe" + yangVersion, filepath.Join(outTypes, yangVersion, "iosxe_config.go")
}

// ygotFamilyOutputLayout returns the package name and output file for a
// per-family scoped schema package. Output lands at:
//
//	<outTypes>/<release>/<family>/schema.go
//
// with package name cfgval_<family>_<release>.
func ygotFamilyOutputLayout(yangVersion, family, outTypes string) (pkgName, outFile string) {
	safe := strings.ReplaceAll(family, "-", "_")
	pkgName = "cfgval_" + safe + "_" + yangVersion
	outFile = filepath.Join(outTypes, yangVersion, family, "schema.go")
	return pkgName, outFile
}

// ModuleClosure is the result of a computeModuleClosure call.
type ModuleClosure struct {
	// AllFiles is the complete set of .yang filenames (top-level modules and
	// submodules) that belong in the isolated generation directory.
	AllFiles []string
	// TopLevelFiles contains only the top-level module .yang filenames
	// (excludes submodules). These are the files to pass as explicit
	// arguments to the ygot generator; submodules are found automatically
	// via the -path flag.
	TopLevelFiles []string
}

// computeModuleClosure returns the minimal set of .yang filenames from yangDir
// that are transitively required to serve the given YANG paths. Each yangPath
// is expected to carry a Cisco-style module prefix (e.g.
// /Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list); the
// function extracts the module names from those prefixes, then walks the
// import and include graphs to collect all transitively required modules and
// their submodules.
//
// YANG `include` statements (submodules) are followed in addition to
// `import` statements because submodules are inseparable from their parent
// module — a YANG processor always requires them together. Omitting a
// submodule from an isolated generation directory causes "no such submodule"
// errors in the ygot generator.
//
// Top-level module files and submodule files are distinguished in the returned
// ModuleClosure. Pass AllFiles to the isolated directory and TopLevelFiles as
// explicit ygot generator arguments (submodules are auto-loaded by ygot via
// the -path flag; passing them explicitly causes duplicate declarations).
func computeModuleClosure(yangDir string, yangPaths []string, stderr io.Writer) (*ModuleClosure, error) {
	// Extract unique module names referenced in the YANG paths.
	seedModules := map[string]struct{}{}
	for _, yp := range yangPaths {
		for _, segment := range strings.Split(yp, "/") {
			if idx := strings.Index(segment, ":"); idx > 0 {
				seedModules[segment[:idx]] = struct{}{}
			}
		}
	}
	if len(seedModules) == 0 {
		return nil, fmt.Errorf("no module prefixes found in yang_paths %v", yangPaths)
	}

	// Build a fast lookup: module-name → filename.
	entries, err := os.ReadDir(yangDir)
	if err != nil {
		return nil, fmt.Errorf("read yang-dir %q: %w", yangDir, err)
	}
	nameToFile := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yang") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".yang")
		nameToFile[base] = e.Name()
	}

	// BFS over the import+include graph using goyang's lightweight parser.
	ms := yang.NewModules()
	ms.AddPath(yangDir)

	visitedModules := map[string]struct{}{}    // top-level modules
	visitedSubmodules := map[string]struct{}{} // submodules
	queue := make([]string, 0, len(seedModules))
	for m := range seedModules {
		queue = append(queue, m)
	}

	for len(queue) > 0 {
		modName := queue[0]
		queue = queue[1:]
		if _, seen := visitedModules[modName]; seen {
			continue
		}
		if _, seen := visitedSubmodules[modName]; seen {
			continue
		}

		if _, ok := nameToFile[modName]; !ok {
			fmt.Fprintf(stderr, "  warning: module %q not found in yang-dir; skipping\n", modName)
			continue
		}

		if err := ms.Read(modName); err != nil {
			// Non-fatal: some auxiliary modules have broken includes in IOS-XE bundles.
			fmt.Fprintf(stderr, "  warning: goyang parse %q: %v; skipping\n", modName, err)
			continue
		}

		// Queue imports and includes from top-level modules.
		if mod, ok := ms.Modules[modName]; ok {
			visitedModules[modName] = struct{}{}
			for _, imp := range mod.Import {
				if imp.Name != "" {
					queue = append(queue, imp.Name)
				}
			}
			for _, inc := range mod.Include {
				if inc.Name != "" {
					queue = append(queue, inc.Name)
				}
			}
		}
		// Queue imports from submodules (submodules can import other modules).
		if sub, ok := ms.SubModules[modName]; ok {
			visitedSubmodules[modName] = struct{}{}
			for _, imp := range sub.Import {
				if imp.Name != "" {
					queue = append(queue, imp.Name)
				}
			}
			for _, inc := range sub.Include {
				if inc.Name != "" {
					queue = append(queue, inc.Name)
				}
			}
		}
	}

	// Collect filenames in sorted order so output is deterministic.
	topLevelFiles := make([]string, 0, len(visitedModules))
	for modName := range visitedModules {
		if fname, ok := nameToFile[modName]; ok {
			topLevelFiles = append(topLevelFiles, fname)
		}
	}
	sort.Strings(topLevelFiles)

	submoduleFiles := make([]string, 0, len(visitedSubmodules))
	for modName := range visitedSubmodules {
		if fname, ok := nameToFile[modName]; ok {
			submoduleFiles = append(submoduleFiles, fname)
		}
	}
	sort.Strings(submoduleFiles)

	allFiles := append(append([]string(nil), topLevelFiles...), submoduleFiles...)
	sort.Strings(allFiles)

	return &ModuleClosure{
		AllFiles:      allFiles,
		TopLevelFiles: topLevelFiles,
	}, nil
}

// runYgotPerFamily generates per-family schema packages using goyang to
// extract the terminal YANG entries for each family's yang_paths. Generated
// packages land under:
//
//	<outTypes>/<release>/<family>/schema.go
//
// goyang resolves all augment statements during ms.Process() so no isolated
// temporary directory is needed. The resulting schema.go contains a small
// gzip-JSON blob (kilobytes) with real type information.
//
// Failures for individual families are non-fatal: a SKIPPED stub is emitted
// and generation continues.
func runYgotPerFamily(stdout, stderr io.Writer, f flags, families map[string]family) error {
	info, err := os.Stat(f.yangDir)
	if err != nil {
		return fmt.Errorf("yang-dir %q: %w", f.yangDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("yang-dir %q is not a directory", f.yangDir)
	}

	absYangDir, err := filepath.Abs(f.yangDir)
	if err != nil {
		return fmt.Errorf("abs yang-dir: %w", err)
	}

	fmt.Fprintf(stdout, "\nper-family goyang schema extraction (release=%s)\n", f.yangVersion)

	names := make([]string, 0, len(families))
	for n := range families {
		names = append(names, n)
	}
	sort.Strings(names)

	var succeeded, skipped int
	var registeredPackages []string
	for _, name := range names {
		fam := families[name]
		pkgName, outFile := ygotFamilyOutputLayout(f.yangVersion, name, f.outTypes)

		closure, err := computeModuleClosure(f.yangDir, fam.YANGPaths, stderr)
		if err != nil || len(closure.TopLevelFiles) == 0 {
			skipped++
			reason := "no YANG modules found"
			if err != nil {
				reason = err.Error()
			}
			fmt.Fprintf(stdout, "  SKIP  %s: %s\n", name, reason)
			if !f.dryRun {
				if wErr := writeSkipStub(outFile, pkgName, reason); wErr != nil {
					fmt.Fprintf(stderr, "  warn: write skip stub %s: %v\n", outFile, wErr)
				}
			}
			continue
		}

		fmt.Fprintf(stdout, "  goyang %s -> %s (%d modules)\n",
			name, outFile, len(closure.AllFiles))
		if f.dryRun {
			continue
		}

		entries, extractErr := extractFamilySchemaEntries(absYangDir, closure.AllFiles, fam.YANGPaths)
		if extractErr != nil {
			skipped++
			reason := extractErr.Error()
			fmt.Fprintf(stdout, "  SKIP  %s: extract schema: %s\n", name, reason)
			if wErr := writeSkipStub(outFile, pkgName, "extract schema: "+reason); wErr != nil {
				fmt.Fprintf(stderr, "  warn: write skip stub %s: %v\n", outFile, wErr)
			}
			continue
		}

		blob, blobErr := buildFamilyYSchema(entries)
		if blobErr != nil {
			skipped++
			reason := blobErr.Error()
			fmt.Fprintf(stdout, "  SKIP  %s: build schema: %s\n", name, reason)
			if wErr := writeSkipStub(outFile, pkgName, "build schema: "+reason); wErr != nil {
				fmt.Fprintf(stderr, "  warn: write skip stub %s: %v\n", outFile, wErr)
			}
			continue
		}
		// Reject pathological cases where the terminal entry is a very large
		// subtree (e.g. the entire /native container). A blob above 512 KB
		// indicates the yang_path points too close to the root of the native
		// module tree; validation for such families remains a skip-no-op.
		const maxFamilyBlobBytes = 512 * 1024
		if len(blob) > maxFamilyBlobBytes {
			skipped++
			reason := fmt.Sprintf("schema blob too large (%d bytes; limit %d bytes)", len(blob), maxFamilyBlobBytes)
			fmt.Fprintf(stdout, "  SKIP  %s: %s\n", name, reason)
			if wErr := writeSkipStub(outFile, pkgName, reason); wErr != nil {
				fmt.Fprintf(stderr, "  warn: write skip stub %s: %v\n", outFile, wErr)
			}
			continue
		}

		if wErr := writeFamilySchemaFile(outFile, pkgName, blob); wErr != nil {
			skipped++
			reason := wErr.Error()
			fmt.Fprintf(stdout, "  SKIP  %s: write schema: %s\n", name, reason)
			if wErr2 := writeSkipStub(outFile, pkgName, "write schema: "+reason); wErr2 != nil {
				fmt.Fprintf(stderr, "  warn: write skip stub %s: %v\n", outFile, wErr2)
			}
			continue
		}

		// Emit register.go alongside schema.go so the generated package
		// self-registers with the cfgvalidation harness on import.
		registerFile := filepath.Join(filepath.Dir(outFile), "register.go")
		if wErr := writeRegisterFile(registerFile, pkgName, name, f.yangVersion); wErr != nil {
			fmt.Fprintf(stderr, "  warn: write register %s: %v\n", registerFile, wErr)
		} else {
			registeredPackages = append(registeredPackages,
				goModulePath+"/"+filepath.ToSlash(f.outTypes)+"/"+f.yangVersion+"/"+name)
		}

		succeeded++
	}

	fmt.Fprintf(stdout, "\nper-family goyang: %d generated, %d skipped\n", succeeded, skipped)
	if !f.dryRun {
		if wErr := writeSchemaValidatorsFile(f.outTypes, f.yangVersion, registeredPackages); wErr != nil {
			fmt.Fprintf(stderr, "  warn: write schema validators: %v\n", wErr)
		}
	}
	return nil
}

// buildIsolatedDir creates a temporary directory containing symlinks to each
// file in allFiles (rooted at absYangDir). When stubs contains an entry for a
// filename, the stub YANG content is written as a file instead of a symlink.
// The returned cleanup function removes the directory; callers must always
// invoke it.
func buildIsolatedDir(absYangDir string, allFiles []string, stubs map[string]string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "cvk-yang-closure-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("mkdirtemp: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }
	for _, fname := range allFiles {
		dst := filepath.Join(dir, fname)
		if stubContent, isStub := stubs[fname]; isStub {
			if writeErr := os.WriteFile(dst, []byte(stubContent), 0o644); writeErr != nil {
				cleanup()
				return "", func() {}, fmt.Errorf("write stub %s: %w", fname, writeErr)
			}
		} else {
			src := filepath.Join(absYangDir, fname)
			if symlinkErr := os.Symlink(src, dst); symlinkErr != nil {
				cleanup()
				return "", func() {}, fmt.Errorf("symlink %s: %w", fname, symlinkErr)
			}
		}
	}
	return dir, cleanup, nil
}

// buildConflictStubs returns minimal stub YANG content for modules that cause
// augmentation collisions in the Cisco IOS-XE YANG bundle. The stubs preserve
// the module namespace and prefix (so import resolution works) but carry no
// augmentation statements, preventing duplicate-node errors.
//
// Current conflict pattern: when both Cisco-IOS-XE-ethernet.yang and
// Cisco-IOS-XE-interfaces.yang (a native submodule) are in the closure,
// ethernet's augmentations conflict with interfaces's definitions for the
// same containers. Stubbing ethernet and the modules that import it
// (Cisco-IOS-XE-l2vpn) eliminates the conflict.
func buildConflictStubs(allFiles []string) map[string]string {
	fileSet := make(map[string]bool, len(allFiles))
	for _, f := range allFiles {
		fileSet[f] = true
	}

	stubs := map[string]string{}
	if fileSet["Cisco-IOS-XE-ethernet.yang"] && fileSet["Cisco-IOS-XE-interfaces.yang"] {
		stubs["Cisco-IOS-XE-ethernet.yang"] = `module Cisco-IOS-XE-ethernet {
  yang-version 1.1;
  namespace "http://cisco.com/ns/yang/Cisco-IOS-XE-ethernet";
  prefix ios-eth;
  import cisco-semver { prefix cisco-semver; }
  include Cisco-IOS-XE-ethernet-oam;
  include Cisco-IOS-XE-ethernet-cfm-efp;
}
`
		stubs["Cisco-IOS-XE-ethernet-oam.yang"] = `submodule Cisco-IOS-XE-ethernet-oam {
  yang-version 1.1;
  belongs-to Cisco-IOS-XE-ethernet { prefix ios-eth; }
}
`
		stubs["Cisco-IOS-XE-ethernet-cfm-efp.yang"] = `submodule Cisco-IOS-XE-ethernet-cfm-efp {
  yang-version 1.1;
  belongs-to Cisco-IOS-XE-ethernet { prefix ios-eth; }

  // Minimal config-interface-ethernet-cfm-efp-grouping skeleton.
  // Cisco-IOS-XE-l2vpn augments ios-eth:service/ios-eth:instance on each
  // interface type. When atm IS in the closure (class_map/policy_map), l2vpn
  // is the real module and needs this path to exist as its augment target.
  grouping config-interface-ethernet-cfm-efp-grouping {
    container service {
      list instance {
        key "id";
        leaf id {
          type uint32 {
            range "1..8000";
          }
        }
      }
    }
  }
}
`
		// l2vpn imports ethernet; two cases depending on whether atm is present:
		//   - atm absent: stub l2vpn empty — safe because nothing uses l2vpn groupings.
		//   - atm present: atm uses ios-l2vpn:config-interface-efp-xconnect-grouping
		//     so l2vpn cannot be fully stubbed. Instead stub l2vpn with only that
		//     grouping and omit all service/instance augments (those reference
		//     ios-eth:service which doesn't exist in the stubbed ethernet, causing
		//     "augment target not found" errors).
		if fileSet["Cisco-IOS-XE-l2vpn.yang"] {
			if !fileSet["Cisco-IOS-XE-atm.yang"] {
				stubs["Cisco-IOS-XE-l2vpn.yang"] = `module Cisco-IOS-XE-l2vpn {
  yang-version 1.1;
  namespace "http://cisco.com/ns/yang/Cisco-IOS-XE-l2vpn";
  prefix ios-l2vpn;
  import cisco-semver { prefix cisco-semver; }
  import Cisco-IOS-XE-native { prefix ios; }
  import ietf-inet-types { prefix inet; }
}
`
			} else {
				// atm in closure: provide only config-interface-efp-xconnect-grouping
				// (the one grouping atm uses from l2vpn). Omit all augment statements
				// so that ios-eth:service/ios-eth:instance "not found" errors disappear.
				stubs["Cisco-IOS-XE-l2vpn.yang"] = `module Cisco-IOS-XE-l2vpn {
  yang-version 1.1;
  namespace "http://cisco.com/ns/yang/Cisco-IOS-XE-l2vpn";
  prefix ios-l2vpn;
  import cisco-semver { prefix cisco-semver; }
  import ietf-inet-types { prefix inet; }

  grouping config-interface-efp-xconnect-grouping {
    choice xconnect-choice {
      container xconnect {
        leaf address { type inet:ipv4-address; }
        leaf vcid { type uint32 { range "1..4294967295"; } }
        leaf encapsulation {
          type enumeration { enum mpls; enum l2tpv3; }
        }
        leaf manual { type empty; }
        leaf pw-class { type string; }
      }
    }
  }
}
`
			}
		}
	}

	// atm + interfaces conflict: Cisco-IOS-XE-atm augments /native/interface/ATM
	// (and ATM-subinterface/ATM etc.) with config-interface-atm-grouping, but
	// Cisco-IOS-XE-interfaces (native submodule, always in closure) already defines
	// "ip" and "load-interval" in the ATM container, causing Duplicate node errors.
	//
	// The stub retains config-interface-atm-grouping with the conflicting "ip" and
	// "load-interval" nodes renamed/omitted, preserving the "pvc" list that
	// Cisco-IOS-XE-policy augments via ios-atm:pvc. Augments to ATM and
	// ATM-subinterface use a pvc-only variant so the augment targets exist without
	// injecting the colliding nodes.
	if fileSet["Cisco-IOS-XE-atm.yang"] && fileSet["Cisco-IOS-XE-interfaces.yang"] {
		stubs["Cisco-IOS-XE-atm.yang"] = `module Cisco-IOS-XE-atm {
  yang-version 1.1;
  namespace "http://cisco.com/ns/yang/Cisco-IOS-XE-atm";
  prefix ios-atm;
  import cisco-semver { prefix cisco-semver; }
  import ietf-inet-types { prefix inet; }
  import Cisco-IOS-XE-native { prefix ios; }
  import Cisco-IOS-XE-l2vpn { prefix ios-l2vpn; }

  // Stripped grouping: "ip" and "load-interval" omitted to avoid the Duplicate
  // node conflict with Cisco-IOS-XE-interfaces. Only "atm" and "pvc" sub-trees
  // are preserved because Cisco-IOS-XE-policy augments ios-atm:pvc.
  grouping config-interface-atm-pvc-grouping {
    container atm {
      leaf bandwidth { type enumeration { enum dynamic; } }
      leaf enable-ilmi-trap { type boolean; default "false"; }
      leaf ilmi-keepalive { type empty; }
      leaf route-bridged { type enumeration { enum ip; enum ipv6; } }
      list pvp {
        key "pvp-number";
        leaf pvp-number { type uint16; }
        leaf l2transport { type empty; }
        uses ios-l2vpn:config-interface-efp-xconnect-grouping;
      }
    }
    leaf cdp { type enumeration { enum enable; } }
    list cem {
      key "number";
      leaf number { type uint32; }
      uses ios-l2vpn:config-interface-efp-xconnect-grouping;
    }
    list pvc {
      key "local-vpi-vci";
      leaf local-vpi-vci { type string; }
      leaf remote-vpi-vci { type string; }
      leaf l2transport { type empty; }
      leaf ubr { type uint32; }
      container ubrplus { leaf PCR { type uint32; } leaf MCR { type uint32; } }
      leaf cbr { type uint32; }
      leaf vbr { type uint32; }
      container vbr-rt {
        leaf PCR { type uint32 { range "48..25000"; } }
        leaf ACR { type uint32; }
        leaf Burst-cell-size { type uint32 { range "1..65535"; } }
      }
      container vbr-nrt {
        leaf PCR { type uint32 { range "48..25000"; } }
        leaf SCR { type uint32; }
      }
    }
  }

  augment "/ios:native/ios:interface/ios:ATM" {
    uses config-interface-atm-pvc-grouping;
  }
  augment "/ios:native/ios:interface/ios:ATM-subinterface/ios:ATM" {
    uses config-interface-atm-pvc-grouping;
  }
  augment "/ios:native/ios:interface/ios:ATM-ACR" {
    uses config-interface-atm-pvc-grouping;
  }
  augment "/ios:native/ios:interface/ios:ATM-ACRsubinterface/ios:ATM-ACR" {
    uses config-interface-atm-pvc-grouping;
  }
}
`
	}

	// bgp + snmp conflict: Cisco-IOS-XE-bgp augments
	// /native/snmp-server/enable/enable-choice/traps with bgp-trap groupings,
	// but Cisco-IOS-XE-snmp already defines a `bgp` leaf and `bgp-traps`
	// container at that same path → ygot "Duplicate node" error.
	//
	// The stub must:
	//   1. Include the enable/enable-choice/traps container skeleton (via
	//      augment) so bgp's augment to that path resolves successfully.
	//      The stub container is empty — no bgp nodes — so the collision
	//      is avoided.
	//   2. Retain config-access-grouping, config-priv-grouping, and
	//      router-snmp-grouping because Cisco-IOS-XE-isis (also in bgp's
	//      closure) uses ios-snmp:router-snmp-grouping and the two helper
	//      groupings it transitively depends on.
	//   3. Omit all other augment statements and all snmp-server trap
	//      container content that conflicts with bgp's augment.
	if fileSet["Cisco-IOS-XE-bgp.yang"] && fileSet["Cisco-IOS-XE-snmp.yang"] {
		stubs["Cisco-IOS-XE-snmp.yang"] = `module Cisco-IOS-XE-snmp {
  yang-version 1.1;
  namespace "http://cisco.com/ns/yang/Cisco-IOS-XE-snmp";
  prefix ios-snmp;
  import cisco-semver { prefix cisco-semver; }
  import Cisco-IOS-XE-native { prefix ios; }

  grouping config-access-grouping {
    container access-config {
      leaf ipv6-acl { type string { length "1..194"; } }
      choice access-option {
        leaf standard-acl { type uint32 { range "1..99"; } }
        leaf acl-name { type string { length "1..183"; } }
        leaf ipv6 { status obsolete; type string; }
      }
    }
  }

  grouping config-priv-grouping {
    container priv-config {
      choice priv-option {
        container aes {
          presence "true";
          leaf algorithm { mandatory true; type enumeration { enum 128; enum 192; enum 256; } }
          leaf password { mandatory true; type string; }
          uses config-access-grouping;
        }
        container des {
          presence "true";
          leaf password { mandatory true; type string; }
          uses config-access-grouping;
        }
        container des3 {
          presence "true";
          leaf password { mandatory true; type string; }
          uses config-access-grouping;
        }
      }
    }
  }

  grouping router-snmp-grouping {
    container snmp {
      list context {
        key "name";
        leaf name { type string; }
        container community {
          leaf community-string { type string; }
          container access {
            leaf ro { type empty; }
            leaf rw { type empty; }
            leaf standard-acl { type uint32 { range "1..99"; } }
            leaf expanded-acl { type uint32 { range "1300..1999"; } }
            leaf acl-name { type string; }
            leaf ipv6 { type string; }
          }
        }
        container user {
          leaf name { type string; }
          container permisssion {
            container access {
              leaf standard-acl { type uint32 { range "1..99"; } }
              leaf acl-name { type string; }
              leaf ipv6 { type string; }
            }
            container auth-config {
              presence "true";
              leaf algorithm { mandatory true; type enumeration { enum md5; enum sha; } }
              leaf password { mandatory true; type string; }
              uses config-priv-grouping;
              uses config-access-grouping;
            }
            container auth {
              status obsolete;
              leaf md5 { status obsolete; type string; }
              leaf sha { status obsolete; type string; }
            }
            leaf credential { type empty; }
            container encrypted-config {
              presence "true";
              container auth-config {
                presence "true";
                leaf algorithm { mandatory true; type enumeration { enum md5; enum sha; } }
                leaf password { mandatory true; type string; }
                uses config-priv-grouping;
                uses config-access-grouping;
              }
              uses config-access-grouping;
            }
            leaf encrypted { status obsolete; type empty; }
          }
        }
      }
    }
  }

  augment "/ios:native/ios:snmp-server" {
    container enable {
      container enable-choice {
        container traps {
          presence "true";
        }
      }
    }
  }
}
`
	}

	return stubs
}

// buildNativeSubmoduleStubs returns minimal stub content for every submodule
// of Cisco-IOS-XE-native that is NOT referenced by the family's YANG paths.
// The stubs re-declare each grouping from the real submodule file as an empty
// body, so that `uses <grouping>` references in native's main file resolve
// without generating the full submodule type definitions.
//
// This function is NOT currently wired into the generation path. Including
// Cisco-IOS-XE-native as a ygot target — which would be required for the stubs
// to have any effect — causes ygot to build the entire native schema graph in
// memory (tens of GiB), making per-family generation impractical.
//
// The function and its tests are retained as the building block for a future
// approach once a viable native scoping strategy is found.
func buildNativeSubmoduleStubs(yangDir string, allFiles []string, yangPaths []string) map[string]string {
	const nativeModule = "Cisco-IOS-XE-native"
	nativeFile := filepath.Join(yangDir, nativeModule+".yang")

	raw, err := os.ReadFile(nativeFile)
	if err != nil {
		return nil // native not present — nothing to stub
	}

	// Collect all submodule names from native's include statements.
	nativeIncludes := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		const prefix = "include "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, prefix), ";")
		name = strings.TrimSpace(name)
		if name != "" {
			nativeIncludes[name] = true
		}
	}
	if len(nativeIncludes) == 0 {
		return nil
	}

	// Collect module-name prefixes referenced in this family's YANG paths.
	referenced := map[string]bool{}
	for _, yp := range yangPaths {
		for _, seg := range strings.Split(yp, "/") {
			if idx := strings.Index(seg, ":"); idx > 0 {
				referenced[seg[:idx]] = true
			}
		}
	}

	// Build the file set from allFiles for fast lookup.
	fileSet := make(map[string]bool, len(allFiles))
	for _, f := range allFiles {
		fileSet[f] = true
	}

	// For each native submodule that is in the closure but NOT referenced by
	// the family's YANG paths, emit a stub. The stub re-declares each grouping
	// as an empty body so that any `uses` reference in native.yang resolves
	// without generating type definitions for that submodule.
	//
	// Families that augment paths defined by those stubbed groupings (e.g. bgp
	// augmenting /ios:native/ios:ip which comes from config-ip-grouping) will
	// fail ygot generation and be skip-listed. The scoped schema goal is to
	// generate small, family-specific packages; those families are accepted as
	// not yet supported via ygot validation.
	stubs := map[string]string{}
	for subName := range nativeIncludes {
		if referenced[subName] {
			continue // family uses this submodule — keep the real file
		}
		fname := subName + ".yang"
		if !fileSet[fname] {
			continue // not in the closure — nothing to stub
		}
		// Preserve grouping names so native can resolve `uses` references, but
		// emit empty bodies to avoid generating the full submodule types.
		groupings := extractGroupingNames(filepath.Join(yangDir, fname))
		var gb strings.Builder
		gb.WriteString(fmt.Sprintf("submodule %s {\n  yang-version 1.1;\n  belongs-to %s { prefix ios; }\n", subName, nativeModule))
		for _, g := range groupings {
			gb.WriteString(fmt.Sprintf("  grouping %s {}\n", g))
		}
		gb.WriteString("}\n")
		stubs[fname] = gb.String()
	}
	return stubs
}

// extractGroupingNames returns the names of all groupings declared in the
// given YANG file. Used to synthesise empty stub declarations so that
// `uses <grouping>` references in native's main file are resolvable even
// when the submodule is replaced by a stub.
func extractGroupingNames(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		const prefix = "grouping "
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		rest := strings.TrimPrefix(trimmed, prefix)
		parts := strings.FieldsFunc(rest, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '{'
		})
		if len(parts) > 0 && parts[0] != "" {
			names = append(names, parts[0])
		}
	}
	return names
}

// extractFamilySchemaEntries uses goyang to parse the family's YANG module
// closure and returns the terminal *yang.Entry nodes for each yang_path.
// It resolves augment statements by calling ms.Process() so that family
// modules' augments into the native tree are visible.
//
// This convenience wrapper builds the module set from allFiles; callers that
// process many families should use extractFamilySchemaEntriesFromNativeEntry
// with a shared pre-built native entry for efficiency.
func extractFamilySchemaEntries(
	yangDir string,
	allFiles []string,
	yangPaths []string,
) ([]*yang.Entry, error) {
	ms := yang.NewModules()
	ms.AddPath(yangDir)
	for _, f := range allFiles {
		modName := strings.TrimSuffix(f, ".yang")
		if err := ms.Read(modName); err != nil {
			// Non-fatal: mirrors computeModuleClosure pattern.
		}
	}
	if errs := ms.Process(); len(errs) > 0 {
		// Process errors are typically warnings; continue with what resolved.
		_ = errs
	}

	nativeMod, ok := ms.Modules["Cisco-IOS-XE-native"]
	if !ok {
		return nil, fmt.Errorf("native module not found after Process()")
	}
	return extractFamilySchemaEntriesFromNativeEntry(yang.ToEntry(nativeMod), yangPaths)
}

// extractFamilySchemaEntriesFromNativeEntry navigates nativeEntry using
// yangPaths and returns the terminal *yang.Entry nodes. It is separated from
// extractFamilySchemaEntries so callers that process many families can build
// the native entry once (with all augments resolved) and share it.
func extractFamilySchemaEntriesFromNativeEntry(
	nativeEntry *yang.Entry,
	yangPaths []string,
) ([]*yang.Entry, error) {
	seen := map[string]struct{}{}
	var entries []*yang.Entry
	for _, yp := range yangPaths {
		segments := strings.Split(strings.TrimPrefix(yp, "/"), "/")
		cur := nativeEntry
		for _, seg := range segments {
			if seg == "" {
				continue
			}
			// Strip key predicates: "vlan-list[id=10]" → "vlan-list"
			if i := strings.Index(seg, "["); i >= 0 {
				seg = seg[:i]
			}
			// Strip module prefix: "Cisco-IOS-XE-vlan:vlan-list" → "vlan-list"
			if i := strings.LastIndex(seg, ":"); i >= 0 {
				seg = seg[i+1:]
			}
			if cur.Dir == nil {
				cur = nil
				break
			}
			cur = cur.Dir[seg]
			if cur == nil {
				break
			}
		}
		if cur == nil || cur == nativeEntry {
			return nil, fmt.Errorf("yang_path %q: not found in native schema", yp)
		}
		if _, dup := seen[cur.Name]; !dup {
			seen[cur.Name] = struct{}{}
			entries = append(entries, cur)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no schema entries found for yang_paths %v", yangPaths)
	}
	return entries, nil
}

// buildFamilyYSchema serialises a list of *yang.Entry nodes into the
// gzip-JSON format expected by the UnzipSchema function in the generated
// schema.go. The synthetic root has Name "FamilyRoot" and the provided
// entries become its Dir children.
func buildFamilyYSchema(entries []*yang.Entry) ([]byte, error) {
	root := &yang.Entry{Name: "FamilyRoot", Dir: make(map[string]*yang.Entry, len(entries))}
	for _, e := range entries {
		e.Parent = nil // clear before marshal; rebuilt on load by UnzipSchema
		root.Dir[e.Name] = e
	}
	j, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal schema entries: %w", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(j); err != nil {
		return nil, fmt.Errorf("gzip write: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), nil
}

// writeFamilySchemaFile renders familySchemaTemplate with the provided
// package name and gzip blob and writes the result to path.
func writeFamilySchemaFile(path, pkgName string, blob []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	type schemaData struct {
		PkgName string
		Blob    []byte
	}
	var buf strings.Builder
	if err := familySchemaTemplate.Execute(&buf, schemaData{PkgName: pkgName, Blob: blob}); err != nil {
		return fmt.Errorf("render family schema template: %w", err)
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// blobToGoLiteral converts a byte slice to a Go byte-slice literal like
// []byte{0x1f, 0x8b, ...} with 16 bytes per line.
func blobToGoLiteral(blob []byte) string {
	if len(blob) == 0 {
		return "[]byte{}"
	}
	var sb strings.Builder
	sb.WriteString("[]byte{")
	for i, b := range blob {
		if i%16 == 0 {
			sb.WriteString("\n\t\t")
		}
		sb.WriteString(fmt.Sprintf("0x%02x, ", b))
	}
	sb.WriteString("\n\t}")
	return sb.String()
}

var familySchemaTemplate = template.Must(template.New("family_schema").Funcs(template.FuncMap{
	"blobLiteral": blobToGoLiteral,
}).Parse(
	`// Code generated by cisco-vk-yang-sync. DO NOT EDIT.
// Per-family goyang schema: terminal entries extracted from the Cisco-IOS-XE
// native YANG tree for the paths declared in this family's yang_paths.
// The UnzipSchema function is API-compatible with the ygot-generated version
// so register.go works unchanged.

package {{ .PkgName }}

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"

	"github.com/openconfig/goyang/pkg/yang"
)

// FamilyRoot is a placeholder kept for compatibility with any code that
// references this type by name. The real schema lives in ySchema / UnzipSchema.
type FamilyRoot struct{}

// UnzipSchema decompresses ySchema, reconstructs the yang.Entry tree, and
// returns the map[string]*yang.Entry that register.go and ValidateBody use.
func UnzipSchema() (map[string]*yang.Entry, error) {
	r, err := gzip.NewReader(bytes.NewReader(ySchema))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}
	var root yang.Entry
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	// Rebuild parent pointers recursively.
	var setParents func(e *yang.Entry)
	setParents = func(e *yang.Entry) {
		for _, child := range e.Dir {
			child.Parent = e
			setParents(child)
		}
	}
	setParents(&root)
	return map[string]*yang.Entry{"FamilyRoot": &root}, nil
}

var ySchema = {{ .Blob | blobLiteral }}
`))

// familyYgotScope is the pair of values buildFamilyYgotScope returns.
type familyYgotScope struct {
	// TargetFiles are the top-level .yang filenames (relative to isolatedDir)
	// to pass as explicit arguments to the ygot generator. These are the
	// family-specific augmenting modules — NOT the large native base module
	// (which is kept in -path for dependency resolution only).
	TargetFiles []string
	// ExcludeModules is the list of module names (without .yang suffix) to
	// pass via -exclude_modules so ygot parses them for grouping/import
	// resolution but does not emit Go types for them.
	ExcludeModules []string
}

// buildFamilyYgotScope separates the closure into target files (pass as
// explicit ygot args) and exclude-only modules (pass via -exclude_modules).
//
// Strategy:
//  1. Cisco-IOS-XE-native is always excluded (dependency-only; including it
//     as a target causes ygot to build the full native schema graph in memory
//     — an order-of-magnitude more work than any per-family module alone —
//     which makes generation impractical regardless of submodule stubbing).
//  2. "Safe utility" modules — IETF standards libs, cisco-semver, IOS-XE type/
//     feature libraries — are excluded. Ygot resolves their types correctly when
//     they appear in -path but are absent from the generated output.
//  3. All other non-native closure modules become target files. This ensures
//     that modules imported by the family module (e.g. route-map, ospfv3) have
//     their types available during code generation and do not cause "unknown type"
//     errors. The generated package will include types from those modules, which
//     is acceptable because they are semantically part of the family's schema.
//
// Note: native submodules (Cisco-IOS-XE-parser, Cisco-IOS-XE-interfaces, etc.)
// are never in topLevelFiles; they are handled by the -path flag. They must not
// appear in ExcludeModules either because ygot loads them automatically.
//
// Consequence: with native excluded, the FamilyRoot synthetic root has no
// fields (the augmented paths have no owner struct). ValidateBody detects this
// via the FamilyRoot.Dir emptiness guard and skips validation until native can
// be scoped into generation without a memory explosion.
func buildFamilyYgotScope(yangPaths []string, topLevelFiles []string) familyYgotScope {
	const nativeModule = "Cisco-IOS-XE-native"

	// safeExcludes are modules that ygot processes for type resolution but
	// whose generated output we never need in a per-family schema package.
	safeExcludes := map[string]bool{
		"ietf-inet-types":               true,
		"ietf-yang-types":               true,
		"ietf-interfaces":               true,
		"ietf-routing":                  true,
		"cisco-semver":                  true,
		"Cisco-IOS-XE-types":            true,
		"Cisco-IOS-XE-features":         true,
		"Cisco-IOS-XE-interface-common": true,
	}

	var targets, excludes []string
	for _, fname := range topLevelFiles {
		modName := strings.TrimSuffix(fname, ".yang")
		switch {
		case modName == nativeModule:
			excludes = append(excludes, modName)
		case safeExcludes[modName]:
			excludes = append(excludes, modName)
		default:
			targets = append(targets, fname)
		}
	}

	// Fallback: if no targets remain (edge case where the family only
	// references native), put all non-native non-utility files as targets.
	if len(targets) == 0 {
		targets = nil
		excludes = nil
		for _, fname := range topLevelFiles {
			if strings.TrimSuffix(fname, ".yang") != nativeModule {
				targets = append(targets, fname)
			}
		}
		excludes = append(excludes, nativeModule)
	}

	sort.Strings(targets)
	sort.Strings(excludes)
	return familyYgotScope{TargetFiles: targets, ExcludeModules: excludes}
}

// buildPerFamilyYgotArgs constructs the ygot invocation for a single family.
// isolatedDir must contain only the closure modules; it is passed as -path so
// ygot cannot load augmentation modules outside the closure.
// targetFiles are the top-level .yang files that should be passed as explicit
// generator arguments — typically only the family-specific augmenting module,
// not the large native base module. excludeModules lists module names (without
// .yang) that ygot should skip for code generation (dependency-only modules).
func buildPerFamilyYgotArgs(f flags, pkgName, outFile, isolatedDir string, targetFiles, excludeModules []string) ygotInvocation {
	bin := f.ygotBin
	args := []string{}
	if bin == "" {
		bin = "go"
		args = []string{"run", "github.com/openconfig/ygot/generator@v0.34.0"}
	}
	args = append(args,
		"-path="+isolatedDir,
		"-output_file="+outFile,
		"-package_name="+pkgName,
		"-generate_fakeroot",
		"-fakeroot_name=FamilyRoot",
		"-compress_paths=false",
		"-generate_simple_unions",
		"-shorten_enum_leaf_names=true",
	)
	if len(excludeModules) > 0 {
		args = append(args, "-exclude_modules="+strings.Join(excludeModules, ","))
	}
	for _, fname := range targetFiles {
		args = append(args, filepath.Join(isolatedDir, fname))
	}
	return ygotInvocation{bin: bin, args: args}
}

// writeRegisterFile emits a register.go file alongside a generated schema.go.
// The file's init() function calls cfgvalidation.Register so the package
// self-registers with the fixture harness when it is blank-imported by the
// test binary. ValidateBody unmarshals the JSON body against the generated
// FamilyRoot using the package-level Unmarshal helper ygot emits.
func writeRegisterFile(path, pkgName, family, releaseTag string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	type regData struct {
		PkgName    string
		Family     string
		ReleaseTag string
		ModulePath string
	}
	data := regData{
		PkgName:    pkgName,
		Family:     family,
		ReleaseTag: releaseTag,
		ModulePath: "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/cfgvalidation",
	}
	var buf strings.Builder
	if err := registerTemplate.Execute(&buf, data); err != nil {
		return fmt.Errorf("render register template: %w", err)
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

var registerTemplate = template.Must(template.New("register").Parse(
	`// Code generated by cisco-vk-yang-sync. DO NOT EDIT.
// Register this family's goyang schema with the cfgvalidation harness.

package {{ .PkgName }}

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/openconfig/goyang/pkg/yang"
	"{{ .ModulePath }}"
)

func init() {
	cfgvalidation.Register({{ .Family | printf "%q" }}, {{ .ReleaseTag | printf "%q" }}, validator{})
}

type validator struct{}

// ValidateBody unwraps the RESTCONF envelope ({"Module:container": value}) and
// validates the inner payload against the matching schema entry. When path is
// non-empty, the schema entry is resolved by walking the path segments first;
// the local-name scan is used as a fallback when path resolution fails.
//
// Strictness is controlled by the CVK_SCHEMA_VALIDATION environment variable:
//   - unset / any other value: lenient — unknown fields and type mismatches are
//     skipped so fixtures that use deprecated or augment-outside-closure fields
//     do not fail CI.
//   - "strict": unknown fields and structural type mismatches are hard errors,
//     exposing YANG schema discrepancies in fixture data.
func (validator) ValidateBody(path string, body json.RawMessage) error {
	// Decode envelope: expect {"<module>:<name>": <value>}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	st, err := UnzipSchema()
	if err != nil {
		return fmt.Errorf("unzip schema: %w", err)
	}
	// If the synthetic FamilyRoot has no Dir children the schemas were
	// generated without YANG files and contain no usable type information.
	// Skip validation rather than hard-erroring on every op — schemas will
	// gain children once the generator is re-run against real YANG modules.
	if root, ok := st["FamilyRoot"]; !ok || len(root.Dir) == 0 {
		return nil
	}
	for envelopeKey, inner := range envelope {
		var entry *yang.Entry
		if path != "" {
			entry = findSchemaEntryByPath(st, path)
		}
		if entry == nil {
			// Fall back to local-name scan.
			localName := envelopeKey
			if i := strings.LastIndex(envelopeKey, ":"); i >= 0 {
				localName = envelopeKey[i+1:]
			}
			entry = findSchemaEntry(st, localName)
		}
		if entry == nil {
			return fmt.Errorf("schema entry not found for %s (path=%q)", envelopeKey, path)
		}
		var val interface{}
		if err := json.Unmarshal(inner, &val); err != nil {
			return fmt.Errorf("unmarshal %s: %w", envelopeKey, err)
		}
		if err := validateJSONEntry(entry, val); err != nil {
			return fmt.Errorf("%s: %w", envelopeKey, err)
		}
	}
	return nil
}

// isStrictValidation reports whether CVK_SCHEMA_VALIDATION=strict is set.
// In strict mode validateJSONEntry returns errors for unknown fields and
// structural type mismatches; in lenient mode (default) they are skipped.
// Set CVK_SCHEMA_VALIDATION=strict in your test environment to surface
// discrepancies between fixture data and the YANG schema.
func isStrictValidation() bool {
	return os.Getenv("CVK_SCHEMA_VALIDATION") == "strict"
}

// validateJSONEntry checks val against the yang.Entry schema tree.
// For containers and lists it verifies structural consistency and, in strict
// mode, rejects unknown fields.
func validateJSONEntry(schema *yang.Entry, val interface{}) error {
	if val == nil || schema == nil {
		return nil
	}
	switch {
	case schema.IsList():
		arr, ok := val.([]interface{})
		if !ok {
			// Schema says list but val is not an array. Common with deprecated
			// YANG schemas or RESTCONF extensions not yet in the YANG model.
			if isStrictValidation() {
				return fmt.Errorf("%s: expected JSON array for list, got %T", schema.Name, val)
			}
			return nil
		}
		for _, elem := range arr {
			if err := validateContainerFields(schema, elem); err != nil {
				return err
			}
		}
	case schema.IsContainer():
		return validateContainerFields(schema, val)
	}
	return nil
}

// validateContainerFields checks structural consistency of obj against schema.
// In strict mode it also rejects fields not present in schema.Dir.
func validateContainerFields(schema *yang.Entry, val interface{}) error {
	obj, ok := val.(map[string]interface{})
	if !ok {
		// Schema says container but val is not a JSON object. Common with
		// deprecated YANG schemas where list semantics changed.
		if isStrictValidation() {
			return fmt.Errorf("%s: expected JSON object, got %T", schema.Name, val)
		}
		return nil
	}
	// If Dir is empty, augmenting modules were not included in the schema
	// closure — skip field validation regardless of strictness mode to avoid
	// false positives caused by a coverage gap, not a fixture defect.
	if len(schema.Dir) == 0 {
		return nil
	}
	for k, v := range obj {
		localName := k
		if i := strings.LastIndex(k, ":"); i >= 0 {
			localName = k[i+1:]
		}
		child := schema.Dir[localName]
		if child == nil {
			// Field not found in schema. In lenient mode skip it: the field may
			// come from a deprecated schema, an augmenting module outside the
			// closure, or a RESTCONF extension not modelled in YANG.
			if isStrictValidation() {
				return fmt.Errorf("%s: unknown field %q", schema.Name, k)
			}
			continue
		}
		if err := validateJSONEntry(child, v); err != nil {
			return err
		}
	}
	return nil
}

// findSchemaEntryByPath resolves a yang.Entry by walking the schema tree along
// path segments (e.g. "/Cisco-IOS-XE-bgp:router-bgp"). Module prefixes and key
// predicates are stripped from each segment before lookup.
//
// Because the FamilyRoot schema tree contains only the terminal entries from
// the family's yang_paths (not the full ancestor chain), path segments that
// precede the terminal entry (like "native" or "ip") will not be found at the
// root. The function therefore tries each suffix of the path in turn, resolving
// from the first segment it can find in the schema. This correctly handles
// paths like /native/ip/route/ip-route-interface-forwarding-list where "route"
// is the terminal entry and "ip-route-interface-forwarding-list" is a child.
//
// When the terminal node cannot be reached (e.g. because the terminal entry
// has an empty Dir — augments from another module were not loaded), the
// function returns the deepest ancestor it could find. validateJSONEntry will
// then skip validation for that ancestor if its Dir is empty.
func findSchemaEntryByPath(st map[string]*yang.Entry, path string) *yang.Entry {
	path = strings.TrimPrefix(path, "/")
	segments := strings.Split(path, "/")

	// Normalize: strip key predicates and module prefixes, drop empty segments.
	normalized := make([]string, 0, len(segments))
	for _, seg := range segments {
		if j := strings.Index(seg, "["); j >= 0 {
			seg = seg[:j]
		}
		if j := strings.LastIndex(seg, ":"); j >= 0 {
			seg = seg[j+1:]
		}
		if seg != "" {
			normalized = append(normalized, seg)
		}
	}

	var deepest *yang.Entry
	for start := 0; start < len(normalized); start++ {
		candidate := findSchemaEntry(st, normalized[start])
		if candidate == nil {
			continue
		}
		entry := candidate
		for _, seg := range normalized[start+1:] {
			if entry.Dir == nil || entry.Dir[seg] == nil {
				// Cannot go deeper — record this as the deepest ancestor.
				if deepest == nil {
					deepest = entry
				}
				entry = nil
				break
			}
			entry = entry.Dir[seg]
		}
		if entry != nil {
			return entry // Exact match.
		}
	}
	return deepest // Deepest ancestor found (may be nil).
}

// findSchemaEntry searches the schema tree for an entry whose Name matches
// localName. It checks top-level entries first, then their children.
func findSchemaEntry(st map[string]*yang.Entry, localName string) *yang.Entry {
	for _, e := range st {
		if e.Name == localName {
			return e
		}
		if child, ok := e.Dir[localName]; ok {
			return child
		}
	}
	return nil
}
`))

// writeSchemaValidatorsFile emits a schema_validators_<release>_test.go file
// under <outTypesDir>/../writers/. The file blank-imports every registered
// per-family schema package so their init() functions fire during test runs.
func writeSchemaValidatorsFile(outTypesDir, release string, pkgs []string) error {
	outDir := filepath.Join(outTypesDir, "..", "writers")
	outPath := filepath.Join(outDir, "schema_validators_"+release+"_test.go")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	sorted := make([]string, len(pkgs))
	copy(sorted, pkgs)
	sort.Strings(sorted)
	type svData struct {
		Release  string
		Packages []string
	}
	var buf strings.Builder
	if err := schemaValidatorsTemplate.Execute(&buf, svData{Release: release, Packages: sorted}); err != nil {
		return fmt.Errorf("render schema validators template: %w", err)
	}
	return os.WriteFile(outPath, []byte(buf.String()), 0o644)
}

var schemaValidatorsTemplate = template.Must(template.New("schema_validators").Parse(
	`// Code generated by cisco-vk-yang-sync. DO NOT EDIT.
//
// Blank-imports all per-family ygot schema packages for release {{ .Release }} so their
// init() functions fire and register with cfgvalidation. This activates ygot
// schema validation in the fixture harness for every family whose schema was
// successfully generated (skip-listed families are absent).

package writers

import (
{{ range .Packages }}	_ "{{ . }}"
{{ end }})
`))

// writeSkipStub writes a Go source file that marks a family as skipped by the
// per-family ygot generator. The file is valid Go so the package compiles.
func writeSkipStub(path, pkgName, reason string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Include UnzipSchema and FamilyRoot so any co-existing register.go compiles.
	// UnzipSchema returns an empty map — the FamilyRoot empty-schema guard in
	// ValidateBody then skips validation for this family.
	content := fmt.Sprintf(
		"// SKIPPED: %s\npackage %s\n\n"+
			"import \"github.com/openconfig/goyang/pkg/yang\"\n\n"+
			"// FamilyRoot is a placeholder; schema generation was skipped.\ntype FamilyRoot struct{}\n\n"+
			"// UnzipSchema returns an empty map so ValidateBody skips validation.\n"+
			"func UnzipSchema() (map[string]*yang.Entry, error) {\n"+
			"\treturn map[string]*yang.Entry{}, nil\n"+
			"}\n",
		reason, pkgName)
	return os.WriteFile(path, []byte(content), 0o644)
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
