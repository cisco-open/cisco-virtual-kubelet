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

// Command cisco-vk-config-docs generates per-family markdown reference
// pages by reading the authoritative sources:
//
//   - families.yaml for YANG paths, shape, dependencies, portal URL.
//   - the writers registry for the per-family managed-leaf set.
//
// The output is intentionally spare — one page per family, a single
// index page, and a tree of cross-links. It's not a replacement for
// the netascode portal itself; it's a CVK-specific reference that
// stays in lock-step with the driver because both this tool and the
// driver read the same sources.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/schema"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

type exitCode int

const (
	exitOK       exitCode = 0
	exitBadFlags exitCode = 2
	exitBadInput exitCode = 3
	exitGenerate exitCode = 4
)

type flags struct {
	outDir  string
	dryRun  bool
	dialect string
}

func parseFlags(args []string, stderr io.Writer) (flags, error) {
	fs := flag.NewFlagSet("cisco-vk-config-docs", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f flags
	fs.StringVar(&f.outDir, "out", "docs/reference/families",
		"destination directory; one <family>.md per family plus a README.md index")
	fs.BoolVar(&f.dryRun, "dry-run", false,
		"print what would be written without touching the filesystem")
	fs.StringVar(&f.dialect, "dialect", "cvk",
		"output dialect: 'cvk' (flat per-family pages, the default) or 'portal' (MkDocs-compatible directory tree mirroring netascode.cisco.com/docs/data_models/iosxe/)")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.dialect != "cvk" && f.dialect != "portal" {
		return f, fmt.Errorf("invalid --dialect %q (want cvk|portal)", f.dialect)
	}
	return f, nil
}

func run(args []string, stdout, stderr io.Writer) exitCode {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadFlags
	}

	fams, err := schema.LoadFamilies()
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: load families: %v\n", err)
		return exitBadInput
	}
	names := make([]string, 0, len(fams))
	for name := range fams {
		names = append(names, name)
	}
	sort.Strings(names)

	if !f.dryRun {
		if err := os.MkdirAll(f.outDir, 0o755); err != nil {
			fmt.Fprintf(stderr, "ERROR: mkdir %s: %v\n", f.outDir, err)
			return exitGenerate
		}
	}

	for _, name := range names {
		body := renderFamily(name, fams[name], f.dialect)
		target := familyTargetPath(f.outDir, name, f.dialect)
		if f.dryRun {
			fmt.Fprintf(stdout, "would write %s (%d bytes)\n", target, len(body))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			fmt.Fprintf(stderr, "ERROR: mkdir %s: %v\n", filepath.Dir(target), err)
			return exitGenerate
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			fmt.Fprintf(stderr, "ERROR: write %s: %v\n", target, err)
			return exitGenerate
		}
		fmt.Fprintf(stdout, "wrote %s\n", target)
	}

	indexBody := renderIndex(names, fams, f.dialect)
	target := indexTargetPath(f.outDir, f.dialect)
	if f.dryRun {
		fmt.Fprintf(stdout, "would write %s (%d bytes)\n", target, len(indexBody))
		return exitOK
	}
	if err := os.WriteFile(target, []byte(indexBody), 0o644); err != nil {
		fmt.Fprintf(stderr, "ERROR: write %s: %v\n", target, err)
		return exitGenerate
	}
	fmt.Fprintf(stdout, "wrote %s\n", target)
	return exitOK
}

// familyDoc is the view the templates render over. Everything is
// pre-sorted / pre-resolved so the templates themselves stay flat.
type familyDoc struct {
	Name          string
	Shape         string
	KeyFields     []string
	DependsOn     []string
	Portal        string
	YANGPaths     []string
	ManagedLeaves []string
	InnerKey      string
	Implemented   bool
}

// familyDocV2 carries the same fields as familyDoc plus the
// OpenConfig path slice. The portal-dialect template renders both
// dialects; the cvk-dialect template only references the native
// path slice for backward compatibility.
type familyDocV2 struct {
	familyDoc
	OpenConfigPaths []string
}

func renderFamily(name string, f schema.Family, dialect string) string {
	doc := familyDocV2{
		familyDoc: familyDoc{
			Name:      name,
			Shape:     f.Shape,
			KeyFields: append([]string(nil), f.KeyFields...),
			DependsOn: append([]string(nil), f.DependsOn...),
			Portal:    f.Portal,
			YANGPaths: append([]string(nil), f.YANGPaths...),
		},
		OpenConfigPaths: append([]string(nil), f.OpenConfigPaths...),
	}
	if s, ok := writers.Schema(name); ok {
		doc.ManagedLeaves = append([]string(nil), s.ManagedLeaves...)
		if doc.InnerKey == "" {
			doc.InnerKey = s.InnerKey
		}
		doc.Implemented = true
	}

	var buf strings.Builder
	tpl := familyTemplate
	if dialect == "portal" {
		tpl = portalFamilyTemplate
	}
	if err := tpl.Execute(&buf, doc); err != nil {
		return fmt.Sprintf("template error for %s: %v", name, err)
	}
	return buf.String()
}

// familyTargetPath chooses the per-family file path. cvk dialect
// flattens to <out>/<family>.md; portal dialect mirrors the
// netascode.cisco.com URL shape under data_models/iosxe/<family>/
// index.md so MkDocs picks each family up as its own section page.
func familyTargetPath(outDir, name, dialect string) string {
	if dialect == "portal" {
		return filepath.Join(outDir, "data_models", "iosxe", name, "index.md")
	}
	return filepath.Join(outDir, name+".md")
}

func indexTargetPath(outDir, dialect string) string {
	if dialect == "portal" {
		return filepath.Join(outDir, "data_models", "iosxe", "index.md")
	}
	return filepath.Join(outDir, "README.md")
}

func renderIndex(names []string, fams map[string]schema.Family, dialect string) string {
	type indexRow struct {
		Name        string
		Shape       string
		Implemented bool
		Portal      string
	}
	rows := make([]indexRow, 0, len(names))
	for _, name := range names {
		_, impl := writers.Schema(name)
		rows = append(rows, indexRow{
			Name:        name,
			Shape:       fams[name].Shape,
			Implemented: impl,
			Portal:      fams[name].Portal,
		})
	}
	var buf strings.Builder
	tpl := indexTemplate
	if dialect == "portal" {
		tpl = portalIndexTemplate
	}
	if err := tpl.Execute(&buf, rows); err != nil {
		return fmt.Sprintf("index template error: %v", err)
	}
	return buf.String()
}

var familyTemplate = template.Must(template.New("family").Parse(
	`# {{ .Name }}

{{ if .Implemented -}}
Status: **implemented** — CVK writes the managed leaves listed below on every reconcile.
{{- else -}}
Status: **skeleton** — the family is registered and reachable from an IOSXEConfig CR, but the writer returns ErrNotImplemented. A CR that lists {{ .Name }} in managedFamilies surfaces as Unsupported on status until the real writer lands.
{{- end }}

- Shape: ` + "`" + `{{ .Shape }}` + "`" + `
{{- if .KeyFields }}
- Key field(s): ` + "`" + `{{ range $i, $k := .KeyFields }}{{ if $i }}, {{ end }}{{ $k }}{{ end }}` + "`" + `
{{- end }}
{{- if .InnerKey }}
- netascode inner key: ` + "`" + `{{ .InnerKey }}` + "`" + `
{{- end }}
{{- if .DependsOn }}
- Depends on: {{ range $i, $d := .DependsOn }}{{ if $i }}, {{ end }}[{{ $d }}]({{ $d }}.md){{ end }}
{{- end }}
{{- if .Portal }}
- netascode portal: [{{ .Portal }}]({{ .Portal }})
{{- end }}

## YANG paths

{{ range .YANGPaths }}- ` + "`" + `{{ . }}` + "`" + `
{{ end }}

## Managed leaves

{{ if .ManagedLeaves -}}
The writer reads and writes the following leaves. Leaves outside this
set present on the device are preserved (additive merge).
{{ range .ManagedLeaves }}
- ` + "`" + `{{ . }}` + "`" + `
{{- end }}
{{ else -}}
_No managed leaves reported. The family is registered as a skeleton;
see the status line above._
{{ end }}
`))

var indexTemplate = template.Must(template.New("index").Parse(
	`# IOS-XE configuration driver — family reference

Auto-generated by cisco-vk-config-docs from families.yaml and the
writers registry. Do not edit by hand; re-run the generator.

| family | shape | status | portal |
|---|---|---|---|
{{ range . -}}
| [{{ .Name }}]({{ .Name }}.md) | {{ .Shape }} | {{ if .Implemented }}implemented{{ else }}skeleton{{ end }} | {{ if .Portal }}[↗]({{ .Portal }}){{ end }} |
{{ end }}
`))

// portalFamilyTemplate emits a per-family page in MkDocs-style
// netascode-portal layout. Pages live under
// data_models/iosxe/<family>/index.md so the URL shape mirrors
// netascode.cisco.com/docs/data_models/iosxe/<family>/. Front
// matter (title, parent) lets MkDocs build a navigation tree
// without an extra mkdocs.yml entry per family.
var portalFamilyTemplate = template.Must(template.New("portal-family").Parse(
	`---
title: {{ .Name }}
parent: IOS-XE
---

# {{ .Name }}

{{ if .Implemented -}}
**Status:** implemented in cisco-virtual-kubelet.
{{- else -}}
**Status:** registered as a skeleton; the writer returns ErrNotImplemented.
{{- end }}

## Overview

- Shape: ` + "`" + `{{ .Shape }}` + "`" + `
{{- if .KeyFields }}
- Key field(s): ` + "`" + `{{ range $i, $k := .KeyFields }}{{ if $i }}, {{ end }}{{ $k }}{{ end }}` + "`" + `
{{- end }}
{{- if .InnerKey }}
- Inner key: ` + "`" + `{{ .InnerKey }}` + "`" + `
{{- end }}
{{- if .DependsOn }}
- Depends on: {{ range $i, $d := .DependsOn }}{{ if $i }}, {{ end }}[{{ $d }}](../{{ $d }}/){{ end }}
{{- end }}
{{- if .Portal }}
- Upstream netascode page: [{{ .Portal }}]({{ .Portal }})
{{- end }}

## YANG paths

### Cisco-IOS-XE-native

{{ range .YANGPaths }}- ` + "`" + `{{ . }}` + "`" + `
{{ end }}

{{ if .OpenConfigPaths -}}
### OpenConfig

{{ range .OpenConfigPaths }}- ` + "`" + `{{ . }}` + "`" + `
{{ end }}
{{- end }}

## Managed leaves

{{ if .ManagedLeaves -}}
The writer reads and writes the following leaves; everything outside
this set is preserved as-is on the device.
{{ range .ManagedLeaves }}
- ` + "`" + `{{ . }}` + "`" + `
{{- end }}
{{ else -}}
_No managed leaves reported (skeleton family)._
{{ end }}
`))

// portalIndexTemplate is the MkDocs landing page that lives at
// data_models/iosxe/index.md. The generated table links to each
// family's directory rather than a flat .md so the URLs mirror the
// netascode portal one-for-one.
var portalIndexTemplate = template.Must(template.New("portal-index").Parse(
	`---
title: IOS-XE
nav_order: 1
has_children: true
---

# IOS-XE family reference

Auto-generated. Layout mirrors the upstream netascode portal
(https://netascode.cisco.com/docs/data_models/iosxe/) so an
operator's muscle memory transfers. Do not edit by hand; re-run
` + "`" + `cisco-vk-config-docs --dialect=portal` + "`" + `.

| family | shape | status | upstream |
|---|---|---|---|
{{ range . -}}
| [{{ .Name }}]({{ .Name }}/) | {{ .Shape }} | {{ if .Implemented }}implemented{{ else }}skeleton{{ end }} | {{ if .Portal }}[↗]({{ .Portal }}){{ end }} |
{{ end }}
`))

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
