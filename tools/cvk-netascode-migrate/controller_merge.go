// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type controllerSourcePaths []string

func (p *controllerSourcePaths) String() string { return "explicit source paths" }
func (p *controllerSourcePaths) Set(v string) error {
	if v == "" || v == "-" {
		return errors.New("source paths must be explicit")
	}
	*p = append(*p, v)
	return nil
}

func runControllerMerge(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("controller-merge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	version := flags.String("model-version", "", "required exact pinned model version")
	output := flags.String("output", "", "new private merged-source output file")
	resolveYAML := flags.Bool("resolve-yaml", false, "resolve offline YAML references/tags without relaxing runtime parsing")
	var environment controllerEnvironmentNames
	flags.Var(&environment, "env", "authorize one environment variable by name; repeatable, requires --resolve-yaml")
	component := flags.String("component", "all", "output all, model, or defaults from the same merged snapshot")
	var dirs, files controllerSourcePaths
	flags.Var(&dirs, "yaml-directory", "explicit recursively read YAML directory, repeatable")
	flags.Var(&files, "yaml-file", "explicit YAML/JSON file, repeatable")
	if flags.Parse(args) != nil {
		return 2
	}
	if *version != nacmodel.Version || *output == "" || *output == "-" || flags.NArg() > 1 || len(dirs)+len(files) > 128 || (len(dirs)+len(files) == 0 && flags.NArg() == 0) || (*component != "all" && *component != "model" && *component != "defaults") {
		fmt.Fprintln(stderr, "ERROR: require --model-version 0.5.0, --output new-file, explicit sources and component all/model/defaults")
		return 2
	}
	options := nacmodel.ModelSourceOptions{ResolveYAML: *resolveYAML}
	if len(environment) > 128 || len(environment) > 0 && !*resolveYAML {
		fmt.Fprintln(stderr, "ERROR: environment bindings require --resolve-yaml and at most 128 explicit names")
		return 2
	}
	if len(environment) > 0 {
		options.Environment = map[string]string{}
		for _, name := range environment {
			if name == "" || name != strings.TrimSpace(name) || strings.ContainsAny(name, "\x00=") {
				fmt.Fprintln(stderr, "ERROR: invalid explicit environment binding name")
				return 2
			}
			value, ok := os.LookupEnv(name)
			if !ok || value == "" {
				fmt.Fprintln(stderr, "ERROR: explicit environment binding unavailable")
				return 1
			}
			options.Environment[name] = value
		}
	}
	var paths []string
	for _, dir := range dirs {
		var selected []string
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink source")
			}
			if d.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			if !d.Type().IsRegular() {
				return errors.New("nonregular source")
			}
			selected = append(selected, path)
			if len(paths)+len(selected) > 128 {
				return errors.New("source count limit")
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(stderr, "ERROR: explicit YAML directory unavailable or unsupported")
			return 1
		}
		sort.Strings(selected)
		paths = append(paths, selected...)
	}
	paths = append(paths, files...)
	if flags.NArg() == 1 {
		paths = append(paths, flags.Arg(0))
	}
	if len(paths) == 0 || len(paths) > 128 {
		fmt.Fprintln(stderr, "ERROR: require between one and 128 source documents")
		return 1
	}
	inputs := make([][]byte, 0, len(paths))
	total := 0
	for _, path := range paths {
		if path != "-" {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				fmt.Fprintln(stderr, "ERROR: explicit model source must be a readable regular file")
				return 1
			}
		}
		b, err := readControllerExportInput(path, stdin)
		total += len(b)
		if err != nil || total > controllerInputLimit {
			fmt.Fprintln(stderr, "ERROR: merged source unavailable or exceeds total input limit")
			return 1
		}
		inputs = append(inputs, b)
	}
	merged, meta, err := nacmodel.MergeModelSourcesWithOptions(inputs, *component, options)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: unsupported controller merge input; require explicit bindings and unambiguous bounded YAML")
		return 1
	}
	return writeControllerExport(*output, merged, meta, stdout, stderr)
}

type controllerEnvironmentNames []string

func (p *controllerEnvironmentNames) String() string         { return "explicit environment names" }
func (p *controllerEnvironmentNames) Set(value string) error { *p = append(*p, value); return nil }
