// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"sort"
	"strings"
)

var errWiredBulkNetworkContextUnavailable = errors.New("bulk provisioning network intent is not independently verifiable")

// This is scoped declared intent, not a complete rendered device configuration.
// Original values can be recompiled only from the unchanged source UID/version
// and config generation. A replaced source stays fenced; this does not retain a
// historical copy or add a payload/secret hash to the journal.
type wiredBulkNetworkContext map[string][]networkSetting

func (wiredBulkNetworkContext) String() string   { return "<private bulk network intent>" }
func (wiredBulkNetworkContext) GoString() string { return "<private bulk network intent>" }

func compileWiredBulkNetworkContext(source *configSourceSnapshot, batch wiredBulkBatch) (wiredBulkNetworkContext, error) {
	if err := declaredWiredBulkBatch(source, batch); err != nil {
		return nil, err
	}
	model, err := compileWiredBulkSource(source)
	if err != nil {
		return nil, err
	}
	if model.network == nil {
		return nil, nil // legacy private inventory-only plans retain their contract
	}
	intent := model.network
	// Allocation identity is independently retained by the v2 native observer.
	// External assignments have native identity evidence; definitions still need
	// their separate ownership/version receipts, never masked-value readback.
	if !wiredBulkCredentialDefinitionsSupported(intent) {
		return nil, errWiredBulkNetworkContextUnavailable
	}
	out := wiredBulkNetworkContext{}
	for _, member := range batch.members {
		type inherited struct {
			depth int
			want  networkSetting
		}
		selected := map[string]inherited{}
		for _, binding := range intent.bindings {
			if !siteWithin(member.desired.site, binding.path) {
				continue
			}
			depth := strings.Count(binding.path, "/")
			for _, want := range binding.settings {
				// Native AAA metadata is not a credential/authentication proof.
				if want.kind == "aaa" {
					return nil, errWiredBulkNetworkContextUnavailable
				}
				old, exists := selected[want.kind]
				if exists && old.depth == depth {
					return nil, errConfigurationOwned
				}
				if !exists || old.depth < depth {
					selected[want.kind] = inherited{depth, want}
				}
			}
		}
		if len(selected) == 0 {
			if len(intent.pools) == 0 && len(intent.reservations) == 0 && len(intent.assignments) == 0 && len(intent.credentials) == 0 {
				return nil, errWiredBulkNetworkContextUnavailable
			}
			// Dependency-only manifests need no invented scalar settings. The
			// v2 observer still binds their native identities. An empty scalar
			// selection cannot become rendered device proof: the worker command
			// planner explicitly refuses it.
			out[member.desired.reference.Name] = []networkSetting{}
			continue
		}
		kinds := make([]string, 0, len(selected))
		for kind := range selected {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			out[member.desired.reference.Name] = append(out[member.desired.reference.Name], selected[kind].want)
		}
	}
	return out, nil
}

// Require Catalyst Center's effective policy to still equal the original source
// at every relevant boundary. This guards intended policy only, not the actual
// device configuration, and cannot by itself publish a convergence receipt.
func (c *client) checkWiredBulkNetworkContext(ctx context.Context, source *configSourceSnapshot, batches []wiredBulkBatch) error {
	for _, batch := range batches {
		expected, err := compileWiredBulkNetworkContext(source, batch)
		if err != nil {
			return err
		}
		if expected == nil {
			continue
		}
		catalog, err := c.ListSites(ctx)
		if err != nil {
			return err
		}
		sites := make(map[string]Site, len(catalog))
		for _, site := range catalog {
			sites[site.NameHierarchy] = site
		}
		for _, member := range batch.members {
			for _, want := range expected[member.desired.reference.Name] {
				got, err := c.effectiveNetworkSetting(ctx, sites, member.desired.site, want.kind)
				if err != nil {
					return err
				}
				if len(networkSettingDrift(member.desired.site, want, got)) != 0 {
					return errConfigurationStale
				}
			}
		}
	}
	return nil
}
