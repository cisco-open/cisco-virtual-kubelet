// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	_ "embed"
	"errors"
)

//go:embed upstream/0.5.0/cc_sites.tf
var pinnedSiteExpressions []byte

//go:embed site_defaults.go
var siteDefaultsPolicy []byte

const SiteExpressionsSHA256 = "8d7f003049677a761a8206e1c1a8fa49f5871886fef61c5e46316c433b88cba9"

var SiteDefaultsPolicySHA256 = defaultsDigest(append([]byte(networkDefaultsPolicySHA+"\x00"+SiteExpressionsSHA256+"\x00"), siteDefaultsPolicy...))

var siteDefaultFields = map[string][]string{
	"areas":     {"parent_name"},
	"buildings": {"country", "address", "latitude", "longitude"},
	"floors":    {"floor_number", "rf_model", "width", "length", "height", "units_of_measure"},
}

// SiteDefaultsWithOverrides projects only defaults used by the pinned site
// expressions. The product exporter still decides missing-vs-null evaluation,
// upstream bulk/singular predicates, and execution syntax validity.
func SiteDefaultsWithOverrides(raw []byte) (map[string]map[string]any, error) {
	fail := func() (map[string]map[string]any, error) { return nil, errors.New("unsupported site defaults") }
	if Version != NetworkDefaultsVersion || Revision != NetworkDefaultsRevision || defaultsDigest(pinnedDefaults) != DefaultsSHA256 || defaultsDigest(pinnedSiteExpressions) != SiteExpressionsSHA256 {
		return fail()
	}
	stripe, err := readDefaultsStripe(pinnedDefaults, false)
	if err != nil {
		return fail()
	}
	sites, ok := stripe["sites"].(map[string]any)
	if !ok {
		return fail()
	}
	result := map[string]map[string]any{}
	for kind, fields := range siteDefaultFields {
		result[kind] = map[string]any{}
		source, _ := sites[kind].(map[string]any)
		for _, field := range fields {
			if v, exists := source[field]; exists {
				result[kind][field] = v
			}
		}
	}
	if len(raw) == 0 {
		return result, nil
	}
	stripe, err = readDefaultsStripe(raw, true)
	if err != nil || len(stripe) != 1 {
		return fail()
	}
	sites, ok = stripe["sites"].(map[string]any)
	if !ok {
		return fail()
	}
	for kind, value := range sites {
		allowed, exists := siteDefaultFields[kind]
		if !exists {
			return fail()
		}
		fields, ok := value.(map[string]any)
		if !ok {
			return fail()
		}
		for field, v := range fields {
			found := false
			for _, name := range allowed {
				found = found || field == name
			}
			if !found || v == nil {
				return fail()
			}
			// Bulk areas use literal Global in the resource map, while its count
			// predicate reads defaults. A different fallback is not one resolved site.
			if kind == "areas" && (field != "parent_name" || v != "Global") {
				return fail()
			}
			result[kind][field] = v
		}
	}
	return result, nil
}
