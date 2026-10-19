// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"reflect"
	"slices"
	"testing"
)

func TestPinnedSiteSelection(t *testing.T) {
	declared := []string{"Global/Lab", "Global/Lab2", "Global/Lab/B", "Global/Other"}
	for _, tc := range []struct {
		name string
		opts SiteSelectionOptions
		want []string
		bad  bool
	}{
		{"all", SiteSelectionOptions{}, []string{"Global", "Global/Lab", "Global/Lab2", "Global/Lab/B", "Global/Other"}, false},
		{"global", SiteSelectionOptions{ManageGlobalSettings: true}, []string{"Global"}, false},
		{"prefix", SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, []string{"Global/Lab", "Global/Lab2", "Global/Lab/B"}, false},
		{"exact", SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}, []string{"Global/Lab"}, false},
		{"both", SiteSelectionOptions{ManagedSites: []string{"Global/Other"}, ManageGlobalSettings: true}, []string{"Global", "Global/Other"}, false},
		{"empty-exact", SiteSelectionOptions{ManageSpecificSitesOnly: true}, nil, true},
		{"missing-prefix", SiteSelectionOptions{ManagedSites: []string{"Global/La"}}, nil, true},
		{"global-not-yaml-site", SiteSelectionOptions{ManagedSites: []string{"Global"}}, nil, true},
		{"duplicate", SiteSelectionOptions{ManagedSites: []string{"Global/Lab", "Global/Lab"}}, []string{"Global/Lab", "Global/Lab2", "Global/Lab/B"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSiteSelection(declared, tc.opts)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid module options accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{}
			for path := range got {
				paths = append(paths, path)
			}
			slices.Sort(paths)
			slices.Sort(tc.want)
			if !reflect.DeepEqual(paths, tc.want) {
				t.Fatal("wrong pinned selection", paths)
			}
		})
	}
}
