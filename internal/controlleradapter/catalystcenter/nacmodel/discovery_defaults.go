// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	_ "embed"
	"errors"
)

//go:embed upstream/0.5.0/cc_discovery.tf
var discoveryTerraform []byte

//go:embed discovery_defaults.go
var discoveryDefaultsPolicy []byte

//go:embed document.go
var discoveryDocumentPolicy []byte

const DiscoveryTerraformSHA256 = "114f165a3fabd312de99d37c79ed85fdc9119a145225152d949246161bcf246c"

var DiscoveryDefaultsPolicySHA256 = defaultsDigest(append(append([]byte(DiscoveryTerraformSHA256+"\x00"+DefaultsSHA256+"\x00"), discoveryDefaultsPolicy...), discoveryDocumentPolicy...))

// Discovery defaults are not invented from provider behavior. The pinned bundle
// currently supplies none; supported explicit overrides are resolved separately.
func PinnedDiscoveryDefaults() (map[string]any, error) {
	if Version != NetworkDefaultsVersion || Revision != NetworkDefaultsRevision || defaultsDigest(pinnedDefaults) != DefaultsSHA256 || defaultsDigest(discoveryTerraform) != DiscoveryTerraformSHA256 {
		return nil, errors.New("pinned discovery defaults unavailable")
	}
	node, err := ParseDefaultsDocument(pinnedDefaults)
	if err != nil {
		return nil, err
	}
	var model map[string]any
	if node.Decode(&model) != nil {
		return nil, errors.New("invalid pinned defaults")
	}
	if _, present := model["inventory"]; !present {
		return map[string]any{}, nil
	}
	inventory, ok := model["inventory"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid pinned inventory defaults")
	}
	if _, present := inventory["discovery"]; !present {
		return map[string]any{}, nil
	}
	fields, ok := inventory["discovery"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid pinned discovery defaults")
	}
	return fields, nil
}

func DiscoveryDefaultOverrides(raw []byte) (map[string]any, error) {
	node, err := ParseDefaultsDocument(raw)
	if err != nil {
		return nil, err
	}
	var model map[string]any
	if node.Decode(&model) != nil || len(model) != 1 {
		return nil, errors.New("unsupported discovery defaults scope")
	}
	inventory, ok := model["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return nil, errors.New("unsupported discovery defaults scope")
	}
	fields, ok := inventory["discovery"].(map[string]any)
	if !ok {
		return nil, errors.New("unsupported discovery defaults scope")
	}
	return fields, nil
}
