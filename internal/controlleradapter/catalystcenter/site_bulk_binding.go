// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const siteBulkResourcePrefix = "site-bulk-create:v1:"

// The source-bound bitset retains the exact missing member selection after a
// partial native result. The digest binds logical member order and every
// pre-existing declared-site/ancestor UUID; newly created UUIDs are deliberately
// absent until independently observed. This fits the existing journal resource
// envelope without adding payloads or a second ownership mechanism.
type siteBulkBinding struct {
	ordered []desiredSite
	missing []byte
}

func orderedBulkSites(desired []desiredSite) ([]desiredSite, error) {
	if len(desired) == 0 || len(desired) > 1000 {
		return nil, errInvalidResponse
	}
	ordered := append([]desiredSite(nil), desired...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].path < ordered[j].path })
	for i, site := range ordered {
		if !validSitePath(site.path) || site.path == "Global" || (i > 0 && ordered[i-1].path == site.path) || (site.kind != "area" && site.kind != "building" && site.kind != "floor") {
			return nil, errInvalidResponse
		}
	}
	return ordered, nil
}

func newSiteBulkBinding(desired []desiredSite, actual []Site) (*siteBulkBinding, error) {
	ordered, err := orderedBulkSites(desired)
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	for _, site := range actual {
		exists[site.NameHierarchy] = true
	}
	bits := make([]byte, (len(ordered)+7)/8)
	for i, site := range ordered {
		if !exists[site.path] {
			bits[i/8] |= 1 << uint(i%8)
		}
	}
	return &siteBulkBinding{ordered: ordered, missing: bits}, nil
}

func (b *siteBulkBinding) selected(i int) bool {
	return b.missing[i/8]&(1<<uint(i%8)) != 0
}

func (b *siteBulkBinding) resource(actual []Site) (string, error) {
	created := map[string]bool{}
	logical := make([][2]string, len(b.ordered))
	for i, site := range b.ordered {
		logical[i] = [2]string{site.path, site.kind}
		if b.selected(i) {
			created[site.path] = true
		}
	}
	if len(created) == 0 {
		return "", errConfigurationTransition
	}
	byPath := map[string]Site{}
	for _, site := range actual {
		if _, duplicate := byPath[site.NameHierarchy]; duplicate || !validAPIID(site.ID) {
			return "", errInvalidResponse
		}
		byPath[site.NameHierarchy] = site
	}
	anchors := map[string]string{}
	for _, site := range b.ordered {
		for path := site.path; path != ""; path = parentSitePath(path) {
			if !created[path] {
				anchor, ok := byPath[path]
				if !ok {
					return "", errConfigurationStale
				}
				anchors[path] = anchor.ID
			}
			if path == "Global" {
				break
			}
		}
	}
	paths := make([]string, 0, len(anchors))
	for path := range anchors {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	identities := make([][2]string, 0, len(paths))
	for _, path := range paths {
		identities = append(identities, [2]string{path, anchors[path]})
	}
	raw, err := json.Marshal(struct {
		Members [][2]string
		Anchors [][2]string
	}{logical, identities})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d:%s:%x", siteBulkResourcePrefix, len(b.ordered), base64.RawURLEncoding.EncodeToString(b.missing), sha256.Sum256(raw)), nil
}

func recoverSiteBulkBinding(desired []desiredSite, resource string) (*siteBulkBinding, error) {
	ordered, err := orderedBulkSites(desired)
	if err != nil {
		return nil, err
	}
	if len(resource) > 512 || !strings.HasPrefix(resource, siteBulkResourcePrefix) {
		return nil, errConfigurationTransition
	}
	parts := strings.Split(strings.TrimPrefix(resource, siteBulkResourcePrefix), ":")
	if len(parts) != 3 || parts[0] != strconv.Itoa(len(ordered)) {
		return nil, errConfigurationTransition
	}
	digest, err := hex.DecodeString(parts[2])
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != parts[2] {
		return nil, errConfigurationTransition
	}
	bits, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || len(bits) != (len(ordered)+7)/8 || base64.RawURLEncoding.EncodeToString(bits) != parts[1] {
		return nil, errConfigurationTransition
	}
	if tail := len(ordered) % 8; tail != 0 && bits[len(bits)-1]>>uint(tail) != 0 {
		return nil, errConfigurationTransition
	}
	selected := false
	for _, value := range bits {
		selected = selected || value != 0
	}
	if !selected {
		return nil, errConfigurationTransition
	}
	return &siteBulkBinding{ordered: ordered, missing: bits}, nil
}

func verifySiteBulkBinding(desired []desiredSite, actual []Site, resource string) (bool, error) {
	binding, err := recoverSiteBulkBinding(desired, resource)
	if err != nil {
		return false, err
	}
	current, err := binding.resource(actual)
	if err != nil {
		return false, err
	}
	if current != resource {
		return false, errConfigurationStale
	}
	drift, err := planSites(binding.ordered, actual)
	return err == nil && len(drift) == 0, err
}
