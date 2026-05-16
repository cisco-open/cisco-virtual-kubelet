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

// Command cvk-netascode-migrate helps operators inspect a netascode-shaped
// IOS-XE YAML document before moving it under IOSXEConfig management.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	defaultFamilyIndex  = "internal/drivers/iosxe/configdriver/schema/families.yaml"
	defaultMatrixOutput = "docs/family-parity.md"
)

type family struct {
	YANGPaths      []string `json:"yang_paths"`
	OpenConfigPath []string `json:"openconfig_paths,omitempty"`
	Shape          string   `json:"shape"`
	KeyFields      []string `json:"key_fields,omitempty"`
	DependsOn      []string `json:"depends_on,omitempty"`
	Portal         string   `json:"portal,omitempty"`
}

type report struct {
	Supported   []string
	Unsupported []string
	Config      map[string]any
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "emit-cr":
			return runEmitCR(args[1:], stdin, stdout, stderr)
		case "matrix":
			return runMatrix(args[1:], stdout, stderr)
		case "-h", "--help", "help":
			printUsage(stdout)
			return 0
		}
	}
	return runReport(args, stdin, stdout, stderr)
}

func runReport(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cvk-netascode-migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	familyIndex := fs.String("family-index", defaultFamilyIndex, "path to schema/families.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: expected at most one YAML path")
		return 2
	}
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	r, err := inspect(path, stdin, *familyIndex)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	writeReport(stdout, r)
	return 0
}

func runEmitCR(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cvk-netascode-migrate emit-cr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	familyIndex := fs.String("family-index", defaultFamilyIndex, "path to schema/families.yaml")
	name := fs.String("name", "replace-me", "IOSXEConfig metadata.name")
	namespace := fs.String("namespace", "network", "IOSXEConfig metadata.namespace")
	device := fs.String("device", "", "CiscoDevice name for spec.deviceRef.name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: expected at most one YAML path")
		return 2
	}
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	deviceName := *device
	if deviceName == "" {
		deviceName = *name
	}
	r, err := inspect(path, stdin, *familyIndex)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	cr := starterCR{
		APIVersion: "config.cisco.vk/v1alpha1",
		Kind:       "IOSXEConfig",
		Metadata: metadata{
			Name:      *name,
			Namespace: *namespace,
		},
		Spec: crSpec{
			DeviceRef:       deviceRef{Name: deviceName},
			ManagedFamilies: r.Supported,
			Source:          map[string]any{"inline": r.Config},
		},
	}
	out, err := yaml.Marshal(cr)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: render IOSXEConfig: %v\n", err)
		return 1
	}
	_, _ = stdout.Write(out)
	return 0
}

func runMatrix(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cvk-netascode-migrate matrix", flag.ContinueOnError)
	fs.SetOutput(stderr)
	familyIndex := fs.String("family-index", defaultFamilyIndex, "path to schema/families.yaml")
	output := fs.String("output", defaultMatrixOutput, "output markdown path, or - for stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "ERROR: matrix does not accept positional arguments")
		return 2
	}
	families, err := loadFamilies(*familyIndex)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	body := renderMatrix(families)
	if *output == "-" {
		_, _ = io.WriteString(stdout, body)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fmt.Fprintf(stderr, "ERROR: mkdir %s: %v\n", filepath.Dir(*output), err)
		return 1
	}
	if err := os.WriteFile(*output, []byte(body), 0o644); err != nil {
		fmt.Fprintf(stderr, "ERROR: write %s: %v\n", *output, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", *output)
	return 0
}

func inspect(path string, stdin io.Reader, familyIndex string) (report, error) {
	families, err := loadFamilies(familyIndex)
	if err != nil {
		return report{}, err
	}
	raw, err := readInput(path, stdin)
	if err != nil {
		return report{}, err
	}
	config, err := extractConfiguration(raw)
	if err != nil {
		return report{}, err
	}
	supportedSet := make(map[string]struct{}, len(families))
	for name := range families {
		supportedSet[name] = struct{}{}
	}
	r := report{Config: config}
	for name := range config {
		if _, ok := supportedSet[name]; ok {
			r.Supported = append(r.Supported, name)
		} else {
			r.Unsupported = append(r.Unsupported, name)
		}
	}
	sort.Strings(r.Supported)
	sort.Strings(r.Unsupported)
	return r, nil
}

func readInput(path string, stdin io.Reader) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(stdin)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return raw, nil
}

func loadFamilies(path string) (map[string]family, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read family index %s: %w", path, err)
	}
	var out map[string]family
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse family index %s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("family index %s is empty", path)
	}
	return out, nil
}

func extractConfiguration(raw []byte) (map[string]any, error) {
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse netascode YAML: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("netascode YAML top-level must be a mapping, got %T", root)
	}
	if iosxe, ok := top["iosxe"]; ok {
		return unionEnvelopeConfigurations(iosxe)
	}
	return top, nil
}

func unionEnvelopeConfigurations(iosxe any) (map[string]any, error) {
	envelope, ok := iosxe.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(".iosxe must be a mapping, got %T", iosxe)
	}
	devices, ok := envelope["devices"].([]any)
	if !ok {
		return nil, errors.New(".iosxe.devices missing or not a list")
	}
	out := map[string]any{}
	for i, d := range devices {
		device, ok := d.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(".iosxe.devices[%d] must be a mapping, got %T", i, d)
		}
		cfg, ok := device["configuration"].(map[string]any)
		if !ok {
			continue
		}
		for family, body := range cfg {
			if _, exists := out[family]; !exists {
				out[family] = body
			}
		}
	}
	return out, nil
}

func writeReport(w io.Writer, r report) {
	fmt.Fprintln(w, "supported:")
	for _, family := range r.Supported {
		fmt.Fprintf(w, "  - %s\n", family)
	}
	fmt.Fprintln(w, "unsupported_passthrough:")
	for _, family := range r.Unsupported {
		fmt.Fprintf(w, "  - %s\n", family)
	}
}

func renderMatrix(families map[string]family) string {
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)

	var b bytes.Buffer
	b.WriteString("# CVK netascode family parity\n\n")
	b.WriteString("Auto-generated by `cvk-netascode-migrate matrix`; run `make parity-matrix` to refresh.\n\n")
	b.WriteString("| Family | CVK status | Shape | Key fields | Depends on | Native YANG paths | Portal |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, name := range names {
		f := families[name]
		fmt.Fprintf(&b, "| `%s` | managed | %s | %s | %s | %s | %s |\n",
			name,
			escapeCell(f.Shape),
			codeList(f.KeyFields),
			codeList(f.DependsOn),
			codeList(f.YANGPaths),
			portalCell(f.Portal),
		)
	}
	return b.String()
}

func codeList(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, "`"+escapeCell(item)+"`")
	}
	return strings.Join(out, "<br>")
}

func portalCell(portal string) string {
	if portal == "" {
		return "-"
	}
	return "[docs](" + escapeCell(portal) + ")"
}

func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  cvk-netascode-migrate [--family-index path] [netascode.yaml|-]
  cvk-netascode-migrate emit-cr [flags] [netascode.yaml|-]
  cvk-netascode-migrate matrix [--output docs/family-parity.md]`)
}

type starterCR struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   metadata `json:"metadata"`
	Spec       crSpec   `json:"spec"`
}

type metadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

type crSpec struct {
	DeviceRef       deviceRef      `json:"deviceRef"`
	ManagedFamilies []string       `json:"managedFamilies"`
	Source          map[string]any `json:"source"`
}

type deviceRef struct {
	Name string `json:"name"`
}
