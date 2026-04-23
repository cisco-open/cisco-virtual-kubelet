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

// Command cisco-vk-yang-sync regenerates Go types, CRD OpenAPI fragments,
// and per-family writer skeletons from Cisco-IOS-XE YANG modules and the
// netascode family index.
//
// Phase-0 scaffold: this binary validates the inputs and reports what it
// would generate. The real code-generation phase lands in a follow-up PR,
// by which point callers already depend on the stable CLI flag surface
// introduced here.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"
)

// exitCode is returned by run() rather than called directly so tests can
// exercise the CLI without terminating the test process.
type exitCode int

const (
	exitOK       exitCode = 0
	exitBadFlags exitCode = 2
	exitNotYet   exitCode = 3 // non-dry-run requested but not implemented
	exitBadInput exitCode = 4
)

// flags collects the CLI surface in one struct so tests and the real main()
// share it. Every flag has a default so a minimal invocation works.
type flags struct {
	yangVersion  string
	yangDir      string
	familyIndex  string
	outAPI       string
	outCRD       string
	outWriters   string
	dryRun       bool
}

func parseFlags(args []string, stderr io.Writer) (flags, error) {
	fs := flag.NewFlagSet("cisco-vk-yang-sync", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f flags
	fs.StringVar(&f.yangVersion, "yang-version", "1791",
		"Cisco-IOS-XE YANG release directory under yang-dir")
	fs.StringVar(&f.yangDir, "yang-dir", "",
		"root directory of YANG modules (required when --dry-run=false)")
	fs.StringVar(&f.familyIndex, "family-index",
		"internal/drivers/iosxe/configdriver/schema/families.yaml",
		"path to the netascode family index")
	fs.StringVar(&f.outAPI, "out-api", "api/config/v1alpha1",
		"destination directory for generated API types")
	fs.StringVar(&f.outCRD, "out-crd", "config/crd",
		"destination directory for generated CRD manifests")
	fs.StringVar(&f.outWriters, "out-writers",
		"internal/drivers/iosxe/configdriver/writers",
		"destination directory for generated writer skeletons")
	fs.BoolVar(&f.dryRun, "dry-run", true,
		"print actions rather than writing files (Phase-0 scaffold only supports dry-run)")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	return f, nil
}

// run is the testable entry point. It never calls os.Exit so unit tests
// can inspect its exitCode return value.
func run(args []string, stdout, stderr io.Writer) exitCode {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadFlags
	}

	info, err := os.Stat(f.familyIndex)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: family index not found: %v\n", err)
		return exitBadInput
	}
	if info.IsDir() {
		fmt.Fprintf(stderr, "ERROR: family index is a directory: %s\n", f.familyIndex)
		return exitBadInput
	}

	data, err := os.ReadFile(f.familyIndex)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: read family index: %v\n", err)
		return exitBadInput
	}

	var families map[string]map[string]any
	if err := yaml.Unmarshal(data, &families); err != nil {
		fmt.Fprintf(stderr, "ERROR: parse family index: %v\n", err)
		return exitBadInput
	}
	if len(families) == 0 {
		fmt.Fprintln(stderr, "ERROR: family index is empty")
		return exitBadInput
	}

	fmt.Fprintln(stdout, "cisco-vk-yang-sync (Phase-0 scaffold)")
	fmt.Fprintf(stdout, "  yang-version    = %s\n", f.yangVersion)
	fmt.Fprintf(stdout, "  yang-dir        = %s\n", showOrUnset(f.yangDir))
	fmt.Fprintf(stdout, "  family-index    = %s (%d families loaded)\n", f.familyIndex, len(families))
	fmt.Fprintf(stdout, "  out-api         = %s\n", f.outAPI)
	fmt.Fprintf(stdout, "  out-crd         = %s\n", f.outCRD)
	fmt.Fprintf(stdout, "  out-writers     = %s\n", f.outWriters)
	fmt.Fprintf(stdout, "  dry-run         = %v\n", f.dryRun)

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintln(stdout, "\nfamilies that would be processed:")
	for _, name := range names {
		writerPath := filepath.Join(f.outWriters, name+".go")
		fmt.Fprintf(stdout, "  - %s -> %s\n", name, writerPath)
	}

	if !f.dryRun {
		fmt.Fprintln(stderr, "\nERROR: non-dry-run mode requires the Phase-1 implementation")
		fmt.Fprintln(stderr, "       re-run with --dry-run, or wait for the follow-up PR")
		return exitNotYet
	}
	return exitOK
}

// showOrUnset renders an empty string as "(unset)" so the output is
// unambiguous about missing configuration.
func showOrUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
