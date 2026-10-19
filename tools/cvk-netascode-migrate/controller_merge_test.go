// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func TestControllerMergeOrderAndComponents(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	os.MkdirAll(filepath.Join(tree, "nested"), 0700)
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(tree, "a.yml"), `catalyst_center: {network_settings: {value: first, members: [a]}}
defaults: {catalyst_center: {sites: {floors: {height: 3}}}}`)
	write(filepath.Join(tree, "nested", "b.yaml"), `catalyst_center: {network_settings: {value: second, members: [b]}}`)
	write(filepath.Join(tree, "ignored.json"), `invalid ignored file`)
	file := filepath.Join(dir, "explicit.yaml")
	write(file, `catalyst_center: {network_settings: {value: explicit, members: [c]}}`)
	var binding string
	for _, component := range []string{"all", "model", "defaults"} {
		out := filepath.Join(dir, component+".json")
		var stdout, stderr bytes.Buffer
		code := run([]string{"controller-merge", "--model-version", "0.5.0", "--output", out, "--component", component, "--yaml-file", file, "--yaml-directory", tree, "-"}, strings.NewReader(`catalyst_center: {network_settings: {value: final-private-leaf, members: [d]}}`), &stdout, &stderr)
		if code != 0 {
			t.Fatal(code, stderr.String())
		}
		raw, _ := os.ReadFile(out)
		info, _ := os.Stat(out)
		if info.Mode().Perm() != 0600 {
			t.Fatal("output not private")
		}
		var meta nacmodel.ModelMergeProvenance
		if json.Unmarshal(stdout.Bytes(), &meta) != nil || len(meta.InputsSHA256) != 4 || meta.Resolved || meta.ConfigurationParity {
			t.Fatal("provenance invalid")
		}
		if strings.Contains(stdout.String(), "final-private-leaf") || strings.Contains(stdout.String(), dir) {
			t.Fatal("stdout leaked source values/paths")
		}
		if binding != "" && binding != meta.OrderedInputsSHA256 {
			t.Fatal("components do not bind same ordered sources")
		}
		binding = meta.OrderedInputsSHA256
		var got map[string]any
		json.Unmarshal(raw, &got)
		if component != "defaults" {
			value := got["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)
			if value["value"] != "final-private-leaf" {
				t.Fatal("precedence changed")
			}
			members := value["members"].([]any)
			if len(members) != 4 || members[0] != "a" || members[1] != "b" || members[2] != "c" || members[3] != "d" {
				t.Fatal("directory/file/inline order changed")
			}
		}
		if component == "defaults" {
			if _, ok := got["catalyst_center"]; ok {
				t.Fatal("component leaked model")
			}
			if _, ok := got["defaults"]; !ok {
				t.Fatal("defaults lost")
			}
		}
		if code := run([]string{"controller-merge", "--model-version", "0.5.0", "--output", out, "--yaml-file", file}, nil, &stdout, &stderr); code == 0 {
			t.Fatal("existing output overwritten")
		}
	}
}

func TestControllerMergeRefusal(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte(`catalyst_center: {sites: !env SECRET}`), 0600)
	for i, args := range [][]string{
		{}, {"--yaml-file", bad}, {"--component", "unknown", "--yaml-file", bad}, {"--yaml-directory", filepath.Join(dir, "absent")}, {"--yaml-file", dir}, {"--yaml-file", "-"}, {"--yaml-file", bad, "a", "b"},
	} {
		out := filepath.Join(dir, string(rune('a'+i))+".out")
		var stdout, stderr bytes.Buffer
		all := append([]string{"controller-merge", "--model-version", "0.5.0", "--output", out}, args...)
		if run(all, nil, &stdout, &stderr) == 0 {
			t.Fatal("invalid input accepted")
		}
		if _, e := os.Stat(out); !os.IsNotExist(e) {
			t.Fatal("partial artifact created")
		}
		if stdout.Len() != 0 || strings.Contains(stderr.String(), "SECRET") {
			t.Fatal("refusal leaked model/provenance")
		}
	}
	syms := filepath.Join(dir, "syms")
	os.Mkdir(syms, 0700)
	os.Symlink(bad, filepath.Join(syms, "escape.yaml"))
	var stdout, stderr bytes.Buffer
	if run([]string{"controller-merge", "--model-version", "0.5.0", "--output", filepath.Join(dir, "sym.out"), "--yaml-directory", syms}, nil, &stdout, &stderr) == 0 {
		t.Fatal("directory symlink followed")
	}
}

func TestControllerMergeFeedsSiteDefaults(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.yaml")
	b := filepath.Join(dir, "b.yaml")
	os.WriteFile(a, []byte(`catalyst_center: {sites: {floors: [{name: Floor, parent_name: Global/Lab/B, floor_number: 1}]}}
defaults: {catalyst_center: {sites: {floors: {width: 20, length: 30, height: 4, units_of_measure: meters}}}}`), 0600)
	os.WriteFile(b, []byte(`catalyst_center: {sites: {floors: [{name: Floor, parent_name: Global/Lab/B, rf_model: Free Space}]}}`), 0600)
	model := filepath.Join(dir, "model.json")
	defaults := filepath.Join(dir, "defaults.json")
	var stdout, stderr bytes.Buffer
	for _, pair := range [][2]string{{"model", model}, {"defaults", defaults}} {
		if run([]string{"controller-merge", "--model-version", "0.5.0", "--output", pair[1], "--component", pair[0], "--yaml-file", a, "--yaml-file", b}, nil, &stdout, &stderr) != 0 {
			t.Fatal(stderr.String())
		}
	}
	out := filepath.Join(dir, "resolved.json")
	if run([]string{"controller-export-sites", "--model-version", "0.5.0", "--output", out, "--defaults", defaults, model}, nil, &stdout, &stderr) != 0 {
		t.Fatal(stderr.String())
	}
	raw, _ := os.ReadFile(out)
	if !bytes.Contains(raw, []byte(`"width":20`)) && !bytes.Contains(raw, []byte(`"width": 20`)) {
		t.Fatal("merged default not resolved")
	}
}

func TestControllerMergeExplicitYAMLBindings(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.yaml")
	secret := "fixture-only-private-source-leaf"
	t.Setenv("CVK_SOURCE_VALUE", secret)
	input := `catalyst_center: {sites: {one: &base {value: !env CVK_SOURCE_VALUE}, two: *base}}`
	os.WriteFile(source, []byte(input), 0600)
	for _, tc := range []struct {
		name  string
		flags []string
		ok    bool
	}{
		{"strict", nil, false}, {"unbound", []string{"--resolve-yaml"}, false}, {"explicit", []string{"--resolve-yaml", "--env", "CVK_SOURCE_VALUE"}, true}, {"missing-mode", []string{"--env", "CVK_SOURCE_VALUE"}, false}, {"missing-value", []string{"--resolve-yaml", "--env", "CVK_SOURCE_MISSING"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, tc.name+".json")
			var stdout, stderr bytes.Buffer
			args := append([]string{"controller-merge", "--model-version", "0.5.0", "--output", out, "--yaml-file", source}, tc.flags...)
			code := run(args, nil, &stdout, &stderr)
			if (code == 0) != tc.ok {
				t.Fatal(code, stderr.String())
			}
			if strings.Contains(stdout.String()+stderr.String(), secret) {
				t.Fatal("resolved source leaf leaked")
			}
			if !tc.ok {
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("partial artifact written")
				}
				return
			}
			raw, _ := os.ReadFile(out)
			if !bytes.Contains(raw, []byte(secret)) {
				t.Fatal("explicit binding not resolved")
			}
			var meta nacmodel.ModelMergeProvenance
			json.Unmarshal(stdout.Bytes(), &meta)
			if !meta.YAMLSourceResolved || meta.EnvironmentBindings != 1 || meta.InputSnapshotSHA256 == "" {
				t.Fatal("binding provenance missing")
			}
		})
	}
}
