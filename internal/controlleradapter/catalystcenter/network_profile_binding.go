// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

var errSwitchingProfileBinding = errors.New("switching profile original template project binding is unavailable or changed")

// The exact legacy empty-set digest proves there were no template parents to
// bind. Other legacy digests cannot establish their original project lineage.
func legacyEmptySwitchingProfileBinding(binding string) bool {
	return binding == profileDependencyIdentity(networkProfile{})
}

func validSwitchingProfileBinding(binding string) bool {
	if !strings.HasPrefix(binding, "sp2-") {
		return false
	}
	hash := strings.TrimPrefix(binding, "sp2-")
	return len(hash) == 64 && strings.IndexFunc(hash, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) < 0
}

func boundSwitchingProfileDependency(w switchingProfileIntent, desired networkProfile, projects map[string]templateProject, definitions map[string]templateSummary) (string, error) {
	knownProjects := map[string]bool{}
	for _, project := range projects {
		knownProjects[project.ID] = validAPIID(project.ID)
	}
	rows := []string{}
	for _, group := range []struct {
		kind string
		refs []string
	}{{"dayn", w.dayn}, {"onboarding", w.onboarding}} {
		for _, ref := range group.refs {
			definition, err := resolveProfileTemplate(ref, projects, definitions)
			if err != nil {
				return "", err
			}
			if !knownProjects[definition.ProjectID] || !validAPIID(definition.ID) {
				return "", errSwitchingProfileBinding
			}
			raw, _ := json.Marshal([]string{group.kind, definition.ProjectID, definition.ID})
			rows = append(rows, string(raw))
		}
	}
	sort.Strings(rows)
	// Neither inherited settings nor template contents enter retained identity.
	raw, _ := json.Marshal(struct {
		Membership string
		Parents    []string
	}{profileDependencyIdentity(desired), rows})
	return fmt.Sprintf("sp2-%x", sha256.Sum256(raw)), nil
}

// Compare opaque inherited settings semantically in memory. Never retain raw
// values or unkeyed digests; protected comparisons require the approved key.
func sameSwitchingProfileObservation(before, after []switchingProfileObservation) bool {
	if len(before) != len(after) {
		return false
	}
	for i, first := range before {
		second := after[i]
		if !sameSwitchingNative(first.native, second.native) || first.binding != second.binding || !reflect.DeepEqual(first.desired, second.desired) || first.actual.ID != second.actual.ID || !switchingProfileMatches(first.actual, second.actual) {
			return false
		}
		a, validA := privateSwitchingProfileSettings(first.actual)
		b, validB := privateSwitchingProfileSettings(second.actual)
		if !validA || !validB || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}

// Used by dispatch and acknowledged recovery. Values remain private in-memory
// observations; raw values and unkeyed digests never enter status or logs.
func privateSwitchingProfileSettings(profile networkProfile) (any, bool) {
	for _, attr := range profile.Attributes {
		if attr.Key == "site.settings" {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(attr.raw))
			decoder.UseNumber()
			if decoder.Decode(&value) != nil {
				return nil, false
			}
			normalizePrivateProfileAttributes(value)
			return value, true
		}
	}
	return nil, true
}

// Only unique-key attribs envelopes are unordered. Do not reinterpret value
// subtrees, arbitrary arrays, or duplicate-key sequences. The original raw
// bytes remain untouched for the preserving API update.
func normalizePrivateProfileAttributes(value any) {
	node, ok := value.(map[string]any)
	if !ok {
		return
	}
	if key, ok := node["key"].(string); !ok || key == "" {
		return
	}
	items, ok := node["attribs"].([]any)
	if !ok {
		return
	}
	seen := map[string]bool{}
	for _, item := range items {
		child, ok := item.(map[string]any)
		if !ok {
			return
		}
		key, ok := child["key"].(string)
		if !ok || key == "" || seen[key] {
			return
		}
		seen[key] = true
	}
	for _, item := range items {
		normalizePrivateProfileAttributes(item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].(map[string]any)["key"].(string) < items[j].(map[string]any)["key"].(string)
	})
}
