// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

// Package the first-party module's embedded upstream data, which module-level
// license detection cannot distinguish from the project's Apache-2.0 code.
// Input is the same target's `go list -deps -json` output used to build the image.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const modulePath = "github.com/cisco/virtual-kubelet-cisco"
const sdwan = "internal/controlleradapter/sdwan"
const iosxe = "internal/drivers/iosxe"
const schema = iosxe + "/configdriver/schema"
const generated = iosxe + "/configdriver/generated/"
const yangCommit = "647250f1c8f59aaf1ecdcf1d908fde96c036b1ba"
const yangLicense = "third_party/cisco-iosxe/LICENSE.md"
const yangLicenseSHA = "a65d05690c6d4b550ba902884a0091477d77027cd4fe8da30def51da133b08bf"

type goPackage struct {
	Dir, ImportPath               string
	GoFiles, CgoFiles, EmbedFiles []string
	Module                        *struct{ Path string }
	Error                         json.RawMessage
	DepsErrors                    []json.RawMessage
}

type entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

type bundle struct {
	root  string
	files map[string][]byte
}

func localPath(path string) bool {
	return filepath.IsLocal(path) && filepath.ToSlash(filepath.Clean(path)) == path && !strings.Contains(path, "\\")
}

func (b *bundle) read(path string) ([]byte, error) {
	if !localPath(path) {
		return nil, fmt.Errorf("non-local source path %q", path)
	}
	current := b.root
	for _, part := range strings.Split(path, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink source %s", path)
		}
	}
	info, err := os.Stat(current)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("source is not a nonempty regular file: %s", path)
	}
	return os.ReadFile(current)
}

func (b *bundle) add(paths ...string) error {
	for _, path := range paths {
		data, err := b.read(path)
		if err != nil {
			return err
		}
		b.files[path] = data
	}
	return nil
}

func (b *bundle) sdwanNotices() error {
	if err := b.add(sdwan+"/definitions/LICENSE", sdwan+"/definitions/README.md", sdwan+"/testdata/upstream-lock.json",
		"scripts/sdwan/generate-parcel-definitions.py", "scripts/sdwan/generate-classic-definitions.py", "scripts/sdwan/upstream-inventory.py"); err != nil {
		return err
	}
	if !bytes.Contains(b.files[sdwan+"/definitions/LICENSE"], []byte("Mozilla Public License Version 2.0")) {
		return fmt.Errorf("missing SD-WAN MPL-2.0 license")
	}
	var lock struct {
		Module   struct{ Commit, Repository string }
		Provider struct{ Commit, Repository string }
	}
	if err := json.Unmarshal(b.files[sdwan+"/testdata/upstream-lock.json"], &lock); err != nil {
		return err
	}
	commit := regexp.MustCompile(`^[0-9a-f]{40}$`)
	if !commit.MatchString(lock.Module.Commit) || !commit.MatchString(lock.Provider.Commit) ||
		lock.Module.Repository != "https://github.com/netascode/terraform-sdwan-nac-sdwan" ||
		lock.Provider.Repository != "https://github.com/CiscoDevNet/terraform-provider-sdwan" {
		return fmt.Errorf("invalid SD-WAN upstream provenance")
	}
	for _, name := range []string{"scalar-parcels.json", "classic-templates.json"} {
		data, err := b.read(sdwan + "/definitions/" + name)
		if err != nil {
			return err
		}
		var metadata struct {
			License  string `json:"license"`
			Module   string `json:"module_commit"`
			Provider string `json:"provider_commit"`
		}
		if err := json.Unmarshal(data, &metadata); err != nil {
			return err
		}
		if metadata.License != "MPL-2.0" || metadata.Module != lock.Module.Commit || metadata.Provider != lock.Provider.Commit {
			return fmt.Errorf("SD-WAN definition/provenance mismatch: %s", name)
		}
	}
	return nil
}

func (b *bundle) yangNotices() error {
	if err := b.add(yangLicense, "third_party/cisco-iosxe/README.md"); err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(b.files[yangLicense])) != yangLicenseSHA {
		return fmt.Errorf("Cisco API license differs from the pinned upstream text")
	}
	return nil
}

func collect(root string, packages io.Reader) (*bundle, error) {
	b := &bundle{root: root, files: map[string][]byte{}}
	decoder := json.NewDecoder(packages)
	seen := map[string]bool{}
	hasSDWAN, hasYANG := false, false
	for {
		var pkg goPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if len(pkg.Error) > 0 || len(pkg.DepsErrors) > 0 {
			return nil, fmt.Errorf("incomplete go list package: %s", pkg.ImportPath)
		}
		if pkg.Module == nil || pkg.Module.Path != modulePath {
			continue
		}
		if seen[pkg.ImportPath] {
			return nil, fmt.Errorf("duplicate package: %s", pkg.ImportPath)
		}
		seen[pkg.ImportPath] = true
		packageDir, err := filepath.EvalSymlinks(pkg.Dir)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, packageDir)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		if !localPath(rel) || pkg.ImportPath != modulePath+"/"+rel {
			return nil, fmt.Errorf("package outside source root: %s", pkg.Dir)
		}
		for _, file := range pkg.EmbedFiles {
			if !localPath(file) {
				return nil, fmt.Errorf("invalid embedded file: %s", file)
			}
			path := rel + "/" + file
			switch {
			case rel == sdwan && strings.HasPrefix(file, "definitions/") && strings.Count(file, "/") == 1 && strings.HasSuffix(file, ".json"):
				hasSDWAN = true
				data, err := b.read(path)
				if err != nil {
					return nil, err
				}
				if !json.Valid(data) {
					return nil, fmt.Errorf("invalid embedded JSON: %s", path)
				}
			case rel == schema && (file == "families.yaml" || file == "yang-versions.yaml"):
			default:
				return nil, fmt.Errorf("unclassified first-party embedded asset: %s", path)
			}
			if err := b.add(path); err != nil {
				return nil, err
			}
		}
		if strings.HasPrefix(rel+"/", generated) {
			parts := strings.Split(strings.TrimPrefix(rel, generated), "/")
			if len(parts) != 2 || !regexp.MustCompile(`^[0-9]{4}$`).MatchString(parts[0]) {
				return nil, fmt.Errorf("unclassified generated schema: %s", rel)
			}
			provenance := schema + "/yang/" + parts[0] + "/.provenance.yaml"
			if err := b.add(provenance); err != nil {
				return nil, err
			}
			data := string(b.files[provenance])
			if !strings.Contains(data, "\ncommit: "+yangCommit+"\n") || !strings.HasPrefix(data, "upstream: https://github.com/YangModels/yang.git\n") {
				return nil, fmt.Errorf("unreviewed YANG provenance: %s", provenance)
			}
			if len(pkg.GoFiles) == 0 {
				return nil, fmt.Errorf("generated package has no source: %s", rel)
			}
			for _, file := range append(pkg.GoFiles, pkg.CgoFiles...) {
				if !localPath(file) {
					return nil, fmt.Errorf("invalid generated source: %s", file)
				}
				if err := b.add(rel + "/" + file); err != nil {
					return nil, err
				}
			}
			hasYANG = true
		}
		if rel == iosxe {
			for _, file := range pkg.GoFiles {
				if file != "models.go" {
					continue
				}
				if err := b.add(iosxe + "/models.go"); err != nil {
					return nil, err
				}
				inputs, err := filepath.Glob(filepath.Join(root, "tests/yang/*.yang"))
				if err != nil {
					return nil, err
				}
				if len(inputs) == 0 {
					return nil, fmt.Errorf("IOS-XE model YANG inputs missing")
				}
				for _, input := range inputs {
					if err := b.add("tests/yang/" + filepath.Base(input)); err != nil {
						return nil, err
					}
				}
				hasYANG = true
			}
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("no first-party packages in package inventory")
	}
	if hasSDWAN {
		if err := b.sdwanNotices(); err != nil {
			return nil, err
		}
	}
	if hasYANG {
		if err := b.yangNotices(); err != nil {
			return nil, err
		}
	}
	if err := b.add("third_party/embedded/README.md", "LICENSE"); err != nil {
		return nil, err
	}
	return b, nil
}

func writeBundle(b *bundle, output string) error {
	if _, err := os.Lstat(output); err == nil {
		return fmt.Errorf("output already exists: %s", output)
	} else if !os.IsNotExist(err) {
		return err
	}
	rel, err := filepath.Rel(b.root, output)
	if err != nil {
		return err
	}
	if rel == "." || filepath.IsLocal(rel) {
		return fmt.Errorf("output must be outside the source checkout")
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".embedded-source-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	names := make([]string, 0, len(b.files))
	for path := range b.files {
		names = append(names, path)
	}
	sort.Strings(names)
	entries := make([]entry, 0, len(names))
	for _, path := range names {
		data := b.files[path]
		dest := filepath.Join(stage, "source", filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
		entries = append(entries, entry{Path: "source/" + path, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: len(data)})
	}
	manifest, err := json.MarshalIndent(struct {
		Version int     `json:"version"`
		Files   []entry `json:"files"`
	}{1, entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "MANIFEST.json"), append(manifest, '\n'), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "README.md"), b.files["third_party/embedded/README.md"], 0644); err != nil {
		return err
	}
	// Stable modes and timestamps; no absolute build paths or clock fields in the manifest.
	epoch := time.Unix(0, 0)
	if err := filepath.Walk(stage, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if info.IsDir() {
			mode = 0755
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
		return os.Chtimes(path, epoch, epoch)
	}); err != nil {
		return err
	}
	return os.Rename(stage, output)
}

func main() {
	root := flag.String("repo-root", ".", "source checkout")
	packages := flag.String("packages", "", "target go list -deps -json output")
	output := flag.String("output", "", "new redistribution directory outside checkout")
	flag.Parse()
	if *packages == "" || *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "--packages and --output are required")
		os.Exit(1)
	}
	absRoot, err := filepath.Abs(*root)
	if err == nil {
		absRoot, err = filepath.EvalSymlinks(absRoot)
	}
	var b *bundle
	if err == nil {
		var input *os.File
		input, err = os.Open(*packages)
		if err == nil {
			b, err = collect(absRoot, input)
			input.Close()
		}
	}
	if err == nil {
		var absOutput string
		absOutput, err = filepath.Abs(*output)
		if err == nil {
			var parent string
			parent, err = filepath.EvalSymlinks(filepath.Dir(absOutput))
			if err == nil {
				absOutput = filepath.Join(parent, filepath.Base(absOutput))
			}
		}
		if err == nil {
			err = writeBundle(b, absOutput)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("packaged %d embedded/generated source and attribution files\n", len(b.files))
}
