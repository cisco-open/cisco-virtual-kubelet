// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"io"
)

func runControllerExportCredentials(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller-export-credentials", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("model-version", "", "required exact pinned model version")
	output := fs.String("output", "", "new private resolved-model output file (required)")
	override := fs.String("defaults", "", "optional non-secret credential defaults override file")
	managed := fs.String("managed-sites", "[]", "JSON array of managed site references")
	global := fs.Bool("manage-global-settings", false, "select global credential definitions and root assignments")
	exact := fs.Bool("manage-specific-sites-only", false, "select exact managed paths")
	if fs.Parse(args) != nil {
		return 2
	}
	if *version != nacmodel.Version || *output == "" || *output == "-" || fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: require --model-version 0.5.0, --output new-file and at most one input")
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
	requested := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "managed-sites" || f.Name == "manage-global-settings" || f.Name == "manage-specific-sites-only" {
			requested = true
		}
	})
	var resolved []byte
	var meta catalystcenter.CredentialDefaultsProvenance
	if requested {
		options := nacmodel.SiteSelectionOptions{ManageGlobalSettings: *global, ManageSpecificSitesOnly: *exact}
		if len(*managed) > 65536 || json.Unmarshal([]byte(*managed), &options.ManagedSites) != nil || options.ManagedSites == nil {
			fmt.Fprintln(stderr, "ERROR: managed-sites must be a JSON array of declared site paths")
			return 2
		}
		resolved, meta, err = catalystcenter.ExportSelectedCredentials(raw, defaults, options)
	} else {
		resolved, meta, err = catalystcenter.ExportCredentialDefaultsWithOverrides(raw, defaults)
	}
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: credential export requires supported definitions/references, valid selection and no inline secret values")
		return 1
	}
	return writeControllerExport(*output, resolved, meta, stdout, stderr)
}
