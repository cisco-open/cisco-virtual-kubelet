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

func runControllerExportDiscovery(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller-export-discovery", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("model-version", "", "required exact pinned model version")
	output := fs.String("output", "", "new private resolved-model output file (required)")
	override := fs.String("defaults", "", "optional scoped non-secret discovery defaults file")
	managed := fs.String("managed-sites", "[]", "JSON array of managed site paths declared in the source")
	global := fs.Bool("manage-global-settings", false, "select global discovery jobs using pinned module semantics")
	exact := fs.Bool("manage-specific-sites-only", false, "validate exact managed site selection")
	if fs.Parse(args) != nil {
		return 2
	}
	if *version != nacmodel.Version || *output == "" || *output == "-" || fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: require --model-version 0.5.0, --output new-file and at most one input")
		return 2
	}
	options := nacmodel.SiteSelectionOptions{ManageGlobalSettings: *global, ManageSpecificSitesOnly: *exact}
	if len(*managed) > 65536 || json.Unmarshal([]byte(*managed), &options.ManagedSites) != nil || options.ManagedSites == nil {
		fmt.Fprintln(stderr, "ERROR: managed-sites must be a JSON array of declared site paths")
		return 2
	}
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	raw, err := readControllerExportInput(path, stdin)
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
		defaults, err = readControllerExportInput(*override, nil)
		if err != nil || len(defaults) == 0 {
			fmt.Fprintln(stderr, "ERROR: defaults input unavailable or exceeds limit")
			return 1
		}
	}
	var resolved []byte
	var meta catalystcenter.DiscoverySelectionProvenance
	if *override != "" {
		resolved, meta, err = catalystcenter.ExportSelectedDiscoveryWithDefaults(raw, defaults, options)
	} else {
		resolved, meta, err = catalystcenter.ExportSelectedDiscovery(raw, options)
	}
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: discovery selection requires validated resolved jobs and declared managed sites; other sections and inline credentials are unsupported")
		return 1
	}
	return writeControllerExport(*output, resolved, meta, stdout, stderr)
}
