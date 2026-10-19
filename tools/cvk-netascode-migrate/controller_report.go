// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	yamlv3 "go.yaml.in/yaml/v3"
)

const controllerInputLimit = 1 << 20

var controllerSectionName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type controllerSectionReport struct {
	nacmodel.Section
	Present bool `json:"present"`
}

type controllerParityReport struct {
	Format                    string                    `json:"format"`
	ModelVersion              string                    `json:"modelVersion"`
	UpstreamRevision          string                    `json:"upstreamRevision"`
	UpstreamRepository        string                    `json:"upstreamRepository"`
	Validation                string                    `json:"validation"`
	TemplateContentsChecked   bool                      `json:"templateContentsChecked"`
	TemplateReferencesChecked bool                      `json:"templateReferencesChecked"`
	ConfigurationParity       bool                      `json:"configurationParity"`
	Sections                  []controllerSectionReport `json:"sections"`
	UnknownSections           []string                  `json:"unknownSections"`
	Limitations               []string                  `json:"limitations"`
}

// This is an offline coverage report, not a replacement for nac-validate, a
// Terraform resolver, or a live drift probe. Never echo configuration values.
func runControllerReport(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cvk-netascode-migrate controller-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("model-version", "", "exact upstream model baseline (required)")
	templateDir := fs.String("template-directory", "", "validate external template files from this directory without printing contents")
	checkTemplates := fs.Bool("check-template-references", false, "validate template identities and composite reference graph without resolving content or defaults")
	requireParity := fs.Bool("require-config-parity", false, "exit 3 unless every supplied section supports configuration read, plan and apply")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version != nacmodel.Version || fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: require --model-version 0.5.0 and at most one input path")
		return 2
	}
	input := stdin
	if fs.NArg() == 1 && fs.Arg(0) != "-" {
		file, err := os.Open(fs.Arg(0))
		if err != nil {
			fmt.Fprintln(stderr, "ERROR: cannot open controller model input")
			return 1
		}
		defer file.Close()
		input = file
	}
	var assets []map[string]string
	if *templateDir != "" {
		files, err := readTemplateAssets(*templateDir)
		if err != nil {
			fmt.Fprintln(stderr, "ERROR: cannot resolve template assets:", err)
			return 1
		}
		assets = append(assets, files)
	}
	report, err := inspectControllerModelWithTemplates(input, *checkTemplates || len(assets) > 0, assets...)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, "ERROR: cannot write report")
		return 1
	}
	if len(report.UnknownSections) != 0 {
		return 1
	}
	if *requireParity && !report.ConfigurationParity {
		return 3
	}
	return 0
}

func inspectControllerModel(input io.Reader) (controllerParityReport, error) {
	return inspectControllerModelWithTemplates(input, false)
}
func inspectControllerModelWithTemplates(input io.Reader, checkTemplates bool, assets ...map[string]string) (controllerParityReport, error) {
	r := controllerParityReport{
		Format: nacmodel.Format, ModelVersion: nacmodel.Version,
		UpstreamRevision: nacmodel.Revision, UpstreamRepository: nacmodel.Repository,
		Validation: "section-boundary-only", UnknownSections: []string{},
		Limitations: []string{
			"No full nested schema validation, default expansion, appliance UUID/version resolution or live drift comparison is performed.",
			"Bounded reporting, site create/update and selected network-setting updates are implemented; operational inventory and SWIM do not establish full configuration read/plan/apply parity.",
			"Keep existing configuration ownership until read/plan/apply and cutover are qualified per section.",
		},
	}
	raw, err := io.ReadAll(io.LimitReader(input, controllerInputLimit+1))
	if err != nil || len(raw) > controllerInputLimit {
		return r, errors.New("cannot read controller model within the 1 MiB limit")
	}
	stripe, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return r, err
	}
	if checkTemplates {
		index, err := nacmodel.IndexTemplates(raw)
		if err != nil {
			return r, err
		}
		if len(assets) > 0 {
			if _, err := index.ResolveTemplateContents(assets[0]); err != nil {
				return r, err
			}
			r.TemplateContentsChecked = true
		}
		r.TemplateReferencesChecked = true
		r.Validation = "section-boundary-and-template-reference-graph"
		if r.TemplateContentsChecked {
			r.Validation += "-and-content-files"
		}
	}
	present := map[string]bool{}
	for i := 0; i < len(stripe.Content); i += 2 {
		name := stripe.Content[i].Value
		if !controllerSectionName.MatchString(name) {
			return r, errors.New("invalid controller section identifier")
		}
		// Structural recognition only: leaf-level validity belongs to nac-validate.
		value := stripe.Content[i+1]
		if value.Kind != yamlv3.MappingNode && value.Kind != yamlv3.SequenceNode {
			return r, errors.New("controller sections must be non-null objects or lists")
		}
		present[name] = true
	}
	r.ConfigurationParity = true
	for _, section := range nacmodel.Catalog() {
		included := present[section.Name]
		r.Sections = append(r.Sections, controllerSectionReport{section, included})
		if included && !(section.ConfigurationRead && section.Plan && section.Apply) {
			r.ConfigurationParity = false
		}
		delete(present, section.Name)
	}
	for name := range present {
		r.UnknownSections = append(r.UnknownSections, name)
	}
	sort.Strings(r.UnknownSections)
	if len(r.UnknownSections) > 0 {
		r.ConfigurationParity = false
	}
	return r, nil
}

// Explicit local input only. Refuse symlink assets, and never print file names,
// file contents or underlying filesystem errors that could reveal input data.
func readTemplateAssets(directory string) (map[string]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.New("template directory unavailable")
	}
	files := map[string]string{}
	total := 0
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".j2" && ext != ".vlt" && ext != ".vtl" {
			continue
		}
		if strings.ContainsAny(entry.Name(), "\\\x00") || entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid template asset")
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("template asset is not a regular file")
		}
		file, err := os.Open(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, errors.New("template asset unavailable")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, int64(controllerInputLimit-total+1)))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, errors.New("cannot read template asset")
		}
		total += len(raw)
		if total > controllerInputLimit {
			return nil, errors.New("template assets exceed 1 MiB")
		}
		files[entry.Name()] = string(raw)
	}
	return files, nil
}
