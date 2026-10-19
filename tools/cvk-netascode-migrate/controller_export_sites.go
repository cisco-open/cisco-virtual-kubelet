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

func runControllerExportSites(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller-export-sites", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("model-version", "", "required exact pinned model version")
	output := fs.String("output", "", "new private resolved-model output file (required)")
	override := fs.String("defaults", "", "optional non-secret site defaults override file")
	snapshot := fs.String("source-configmap", "", "explicit offline ConfigMap YAML/JSON containing model, image index and BinaryData")
	modelKey := fs.String("model-key", "", "Data key containing the model in source-configmap")
	bound := fs.Bool("output-configmap", false, "emit one complete ConfigMap artifact, required for managed site selection")
	managed := fs.String("managed-sites", "[]", "JSON array of declared managed site paths")
	global := fs.Bool("manage-global-settings", false, "select Global settings, excluding unrequested sites")
	exact := fs.Bool("manage-specific-sites-only", false, "select exact declared paths")
	bulk := fs.Bool("use-bulk-api", false, "resolve pinned bulk site expressions; match the manifest bulk-sites option")
	if fs.Parse(args) != nil {
		return 2
	}
	if *version != nacmodel.Version || *output == "" || *output == "-" || fs.NArg() > 1 {
		fmt.Fprintln(stderr, "ERROR: require --model-version 0.5.0, --output new-file and at most one input")
		return 2
	}
	if (*snapshot != "" && (fs.NArg() != 0 || *modelKey == "")) || (*snapshot == "" && *modelKey != "") {
		fmt.Fprintln(stderr, "ERROR: use --source-configmap with --model-key and no positional model, or supply one model input")
		return 2
	}
	selectionRequested := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "managed-sites" || f.Name == "manage-global-settings" || f.Name == "manage-specific-sites-only" {
			selectionRequested = true
		}
	})
	if (*bound && *snapshot == "") || (selectionRequested && !*bound) {
		fmt.Fprintln(stderr, "ERROR: selected sites require --source-configmap, --model-key and --output-configmap")
		return 2
	}
	var selection *nacmodel.SiteSelectionOptions
	if selectionRequested {
		selection = &nacmodel.SiteSelectionOptions{ManageGlobalSettings: *global, ManageSpecificSitesOnly: *exact}
		if len(*managed) > 65536 || json.Unmarshal([]byte(*managed), &selection.ManagedSites) != nil || selection.ManagedSites == nil {
			fmt.Fprintln(stderr, "ERROR: managed-sites must be a JSON array of declared site paths")
			return 2
		}
	}
	path := "-"
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	if *snapshot != "" {
		path = *snapshot
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
	var meta catalystcenter.SiteDefaultsProvenance
	if *bound {
		resolved, meta, err = catalystcenter.ExportSiteDefaultsBoundConfigMap(raw, *modelKey, defaults, *bulk, selection)
	} else if *snapshot != "" {
		resolved, meta, err = catalystcenter.ExportSiteDefaultsConfigMap(raw, *modelKey, defaults, *bulk)
	} else {
		resolved, meta, err = catalystcenter.ExportSiteDefaults(raw, defaults, *bulk)
	}
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: site export requires an already-selected site model, supported defaults, valid explicit identities and a same-ConfigMap snapshot for images")
		return 1
	}
	return writeControllerExport(*output, resolved, meta, stdout, stderr)
}
