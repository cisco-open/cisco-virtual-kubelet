// Copyright 2026 Cisco Systems Inc.
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

// krew-manifest-owner-check prevents the update bot from targeting an
// upstream Krew entry that is not owned by this project.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	expectedAPIVersion = "krew.googlecontainertools.github.com/v1alpha2"
	expectedName       = "cisco-vk"
	expectedHomepage   = "https://github.com/cisco-open/cisco-virtual-kubelet"
	expectedURIPath    = "/cisco-open/cisco-virtual-kubelet/releases/download/"
)

func main() {
	manifestPath := flag.String("file", "", "path to the upstream Krew manifest")
	flag.Parse()
	if *manifestPath == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: krew-manifest-owner-check --file <manifest.yaml>")
		os.Exit(2)
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read manifest:", err)
		os.Exit(1)
	}
	if err := validateManifestOwnership(data); err != nil {
		fmt.Fprintln(os.Stderr, "upstream Krew manifest ownership check failed:", err)
		os.Exit(1)
	}
}

func validateManifestOwnership(data []byte) error {
	var document map[string]any
	if err := yaml.UnmarshalStrict(data, &document); err != nil {
		return fmt.Errorf("parse strict YAML: %w", err)
	}
	if got, err := stringField(document, "apiVersion"); err != nil || got != expectedAPIVersion {
		return fmt.Errorf("apiVersion must be %q", expectedAPIVersion)
	}
	if got, err := stringField(document, "kind"); err != nil || got != "Plugin" {
		return errors.New("kind must be \"Plugin\"")
	}
	metadata, err := mapField(document, "metadata")
	if err != nil {
		return err
	}
	if got, err := stringField(metadata, "name"); err != nil || got != expectedName {
		return fmt.Errorf("metadata.name must be %q", expectedName)
	}
	spec, err := mapField(document, "spec")
	if err != nil {
		return err
	}
	if got, err := stringField(spec, "homepage"); err != nil || got != expectedHomepage {
		return fmt.Errorf("spec.homepage must be %q", expectedHomepage)
	}
	platforms, ok := spec["platforms"].([]any)
	if !ok || len(platforms) == 0 {
		return errors.New("spec.platforms must be a non-empty list")
	}
	for index, item := range platforms {
		platform, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("spec.platforms[%d] must be a mapping", index)
		}
		rawURI, err := stringField(platform, "uri")
		if err != nil {
			return fmt.Errorf("spec.platforms[%d]: %w", index, err)
		}
		parsed, err := url.Parse(rawURI)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
			!strings.HasPrefix(parsed.EscapedPath(), expectedURIPath) {
			return fmt.Errorf("spec.platforms[%d].uri is not owned by %s", index, expectedHomepage)
		}
	}
	return nil
}

func mapField(parent map[string]any, name string) (map[string]any, error) {
	value, ok := parent[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a mapping", name)
	}
	return value, nil
}

func stringField(parent map[string]any, name string) (string, error) {
	value, ok := parent[name].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return value, nil
}
