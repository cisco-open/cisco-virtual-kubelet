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
	SelectedDevice string
	Supported      []string
	Unsupported    []string
	Config         map[string]any
}

type inspectOptions struct {
	DeviceName      string
	Strict          bool
	DropUnsupported bool
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
	device := fs.String("device", "", "device name to select from a full iosxe.devices[] file")
	strict := fs.Bool("strict", false, "fail when the input contains families CVK does not manage")
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
	r, err := inspect(path, stdin, *familyIndex, inspectOptions{
		DeviceName: *device,
		Strict:     *strict,
	})
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
	strict := fs.Bool("strict", false, "fail when the input contains families CVK does not manage")
	dropUnsupported := fs.Bool("drop-unsupported", false, "omit unsupported families from spec.source.inline")
	targetYangVersion := fs.String("target-yang-version", "", "optional IOSXEConfig spec.targetYangVersion")
	driftPolicy := fs.String("drift-policy", "report", "initial drift policy: report, revert, or pause")
	transactional := fs.Bool("transactional", true, "enable transactional NETCONF candidate/commit apply")
	writeStartup := fs.Bool("write-startup", false, "copy running-config to startup-config after a successful apply")
	atomicReplace := fs.Bool("atomic-replace", false, "make managed families authoritative when used with transactional apply")
	confirmTimeout := fs.Int("confirm-timeout-seconds", 0, "confirmed-commit timeout in seconds; 0 disables")
	modelFormat := fs.String("model-format", "netascode-iosxe", "modelSource.format")
	modelVersion := fs.String("model-version", "", "modelSource.modelVersion")
	modelResolved := fs.Bool("resolved", true, "modelSource.resolved; production imports should use resolved NetAsCode")
	exporter := fs.String("exporter", "cvk-netascode-migrate", "modelSource.exporter")
	sourceRevision := fs.String("source-revision", "", "modelSource.sourceRevision, such as a Git SHA or Terraform plan ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: expected at most one YAML path")
		return 2
	}
	if err := validateEmitFlags(*driftPolicy, *modelFormat, *confirmTimeout); err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 2
	}
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	r, err := inspect(path, stdin, *familyIndex, inspectOptions{
		DeviceName:      *device,
		Strict:          *strict,
		DropUnsupported: *dropUnsupported,
	})
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	deviceName := *device
	if deviceName == "" {
		deviceName = r.SelectedDevice
	}
	if deviceName == "" {
		deviceName = *name
	}
	cr := starterCR{
		APIVersion: "config.cisco.vk/v1alpha1",
		Kind:       "IOSXEConfig",
		Metadata: metadata{
			Name:      *name,
			Namespace: *namespace,
		},
		Spec: crSpec{
			DeviceRef: deviceRef{Name: deviceName},
			ModelSource: &modelSource{
				Format:         *modelFormat,
				ModelVersion:   *modelVersion,
				Resolved:       *modelResolved,
				Exporter:       *exporter,
				SourceRevision: *sourceRevision,
			},
			ManagedFamilies:       r.Supported,
			Source:                map[string]any{"inline": r.Config},
			Transactional:         *transactional,
			DriftPolicy:           *driftPolicy,
			WriteStartup:          *writeStartup,
			TargetYangVersion:     *targetYangVersion,
			AtomicReplace:         *atomicReplace,
			ConfirmTimeoutSeconds: *confirmTimeout,
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

func validateEmitFlags(driftPolicy, modelFormat string, confirmTimeout int) error {
	switch driftPolicy {
	case "", "report", "revert", "pause":
	default:
		return fmt.Errorf("--drift-policy must be report, revert, or pause")
	}
	if modelFormat != "netascode-iosxe" {
		return fmt.Errorf("--model-format must be netascode-iosxe")
	}
	if confirmTimeout < 0 || confirmTimeout > 300 {
		return fmt.Errorf("--confirm-timeout-seconds must be between 0 and 300")
	}
	return nil
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

func inspect(path string, stdin io.Reader, familyIndex string, opts inspectOptions) (report, error) {
	families, err := loadFamilies(familyIndex)
	if err != nil {
		return report{}, err
	}
	raw, err := readInput(path, stdin)
	if err != nil {
		return report{}, err
	}
	config, selectedDevice, err := extractConfiguration(raw, opts.DeviceName)
	if err != nil {
		return report{}, err
	}
	supportedSet := make(map[string]struct{}, len(families))
	for name := range families {
		supportedSet[name] = struct{}{}
	}
	r := report{SelectedDevice: selectedDevice, Config: config}
	for name := range config {
		if _, ok := supportedSet[name]; ok {
			r.Supported = append(r.Supported, name)
		} else {
			r.Unsupported = append(r.Unsupported, name)
		}
	}
	sort.Strings(r.Supported)
	sort.Strings(r.Unsupported)
	if opts.Strict && len(r.Unsupported) > 0 {
		return report{}, fmt.Errorf("unsupported netascode families: %s", strings.Join(r.Unsupported, ", "))
	}
	if opts.DropUnsupported {
		filtered := make(map[string]any, len(r.Supported))
		for _, family := range r.Supported {
			filtered[family] = config[family]
		}
		r.Config = filtered
	}
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

func extractConfiguration(raw []byte, deviceName string) (map[string]any, string, error) {
	var root any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, "", fmt.Errorf("parse netascode YAML: %w", err)
	}
	top, ok := root.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("netascode YAML top-level must be a mapping, got %T", root)
	}
	if iosxe, ok := top["iosxe"]; ok {
		return selectEnvelopeConfiguration(iosxe, deviceName)
	}
	return top, "", nil
}

func selectEnvelopeConfiguration(iosxe any, deviceName string) (map[string]any, string, error) {
	envelope, ok := iosxe.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf(".iosxe must be a mapping, got %T", iosxe)
	}
	devices, ok := envelope["devices"].([]any)
	if !ok {
		return nil, "", errors.New(".iosxe.devices missing or not a list")
	}
	var fallback map[string]any
	var fallbackName string
	var names []string
	for i, d := range devices {
		device, ok := d.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf(".iosxe.devices[%d] must be a mapping, got %T", i, d)
		}
		name, _ := device["name"].(string)
		if name != "" {
			names = append(names, name)
		}
		cfg, ok := device["configuration"].(map[string]any)
		if !ok {
			continue
		}
		if deviceName != "" {
			if name == deviceName {
				return cfg, name, nil
			}
			continue
		}
		if fallback != nil {
			return nil, "", fmt.Errorf(".iosxe.devices contains multiple configurable devices (%s); pass --device", strings.Join(names, ", "))
		}
		fallback = cfg
		fallbackName = name
	}
	if deviceName != "" {
		return nil, "", fmt.Errorf(".iosxe.devices has no configurable device named %q; available devices: %s", deviceName, strings.Join(names, ", "))
	}
	if fallback == nil {
		return nil, "", errors.New(".iosxe.devices contains no device with a configuration block")
	}
	return fallback, fallbackName, nil
}

func writeReport(w io.Writer, r report) {
	if r.SelectedDevice != "" {
		fmt.Fprintf(w, "device: %s\n", r.SelectedDevice)
	}
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
	DeviceRef             deviceRef      `json:"deviceRef"`
	ModelSource           *modelSource   `json:"modelSource,omitempty"`
	ManagedFamilies       []string       `json:"managedFamilies"`
	Source                map[string]any `json:"source"`
	Transactional         bool           `json:"transactional,omitempty"`
	DriftPolicy           string         `json:"driftPolicy,omitempty"`
	WriteStartup          bool           `json:"writeStartup,omitempty"`
	TargetYangVersion     string         `json:"targetYangVersion,omitempty"`
	AtomicReplace         bool           `json:"atomicReplace,omitempty"`
	ConfirmTimeoutSeconds int            `json:"confirmTimeoutSeconds,omitempty"`
}

type deviceRef struct {
	Name string `json:"name"`
}

type modelSource struct {
	Format         string `json:"format"`
	ModelVersion   string `json:"modelVersion,omitempty"`
	Resolved       bool   `json:"resolved"`
	Exporter       string `json:"exporter,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
}
