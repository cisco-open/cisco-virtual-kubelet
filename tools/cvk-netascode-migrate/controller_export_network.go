// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Explicit offline preprocessing belongs outside runtime providers. Payload is
// written only to a new mode-0600 file; stdout contains non-value provenance.
func runControllerExportNetwork(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller-export-network", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("model-version", "", "required exact pinned model version")
	output := fs.String("output", "", "new private resolved-model output file (required)")
	override := fs.String("defaults", "", "optional network-profile defaults override file")
	managedSites := fs.String("managed-sites", "", "optional JSON array of managed sites declared in the source")
	global := fs.Bool("manage-global-settings", false, "select Global settings using pinned module semantics")
	exact := fs.Bool("manage-specific-sites-only", false, "select only exact managed site paths")
	if fs.Parse(args) != nil {
		return 2
	}
	if *version != nacmodel.Version || *output == "" || *output == "-" || fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: require --model-version 0.5.0, --output new-file and at most one input")
		return 2
	}
	read := readControllerExportInput
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	raw, err := read(path, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: controller input unavailable or exceeds limit")
		return 1
	}
	var defaults []byte
	if *override != "" {
		if *override == "-" {
			fmt.Fprintln(stderr, "ERROR: defaults require an explicit file")
			return 2
		}
		defaults, err = read(*override, nil)
		if err != nil || len(defaults) == 0 {
			fmt.Fprintln(stderr, "ERROR: defaults input unavailable or exceeds limit")
			return 1
		}
	}
	selectionRequested := false
	managedSitesProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "managed-sites" {
			managedSitesProvided = true
		}
		if f.Name == "managed-sites" || f.Name == "manage-global-settings" || f.Name == "manage-specific-sites-only" {
			selectionRequested = true
		}
	})
	options := nacmodel.SiteSelectionOptions{ManagedSites: []string{}, ManageGlobalSettings: *global, ManageSpecificSitesOnly: *exact}
	if managedSitesProvided {
		if len(*managedSites) > 65536 || json.Unmarshal([]byte(*managedSites), &options.ManagedSites) != nil || options.ManagedSites == nil {
			fmt.Fprintln(stderr, "ERROR: managed-sites must be a JSON array of declared site paths")
			return 2
		}
	}
	var resolved []byte
	var meta nacmodel.NetworkDefaultsProvenance
	if selectionRequested {
		resolved, meta, err = catalystcenter.ExportSelectedNetworkSettings(raw, defaults, options)
	} else {
		resolved, meta, err = nacmodel.ExportNetworkDefaults(raw, defaults)
	}
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: network-profile defaults cannot be resolved without unsupported or ambiguous semantics")
		return 1
	}
	if catalystcenter.ValidateNetworkSettingsSource(resolved) != nil {
		fmt.Fprintln(stderr, "ERROR: exported source does not compile as supported network-profile intent")
		return 1
	}
	meta.ExecutionSyntaxValidated = true
	return writeControllerExport(*output, resolved, meta, stdout, stderr)
}
