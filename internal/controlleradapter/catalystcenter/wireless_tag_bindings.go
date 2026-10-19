// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const wirelessTagBoundPrefix = "tag-proof:v4:"

func wirelessTagBoundBaseline(native, graph any) any {
	if native == nil {
		return nil
	}
	return map[string]any{"version": 4, "native": native, "zoneReferences": graph}
}

func wirelessTagZoneGraph(ctx context.Context, c *client, profile wirelessProfileMetadata, policy bool) (any, error) {
	if !policy {
		return nil, nil
	}
	return c.wirelessProfileZoneBindings(ctx, profile, nil)
}

// One new envelope version distinguishes ordinary writes, member reductions
// and deletion without changing any v1/v2/v3 comparison or recovery meaning.
func wirelessTagBindingMode(resource string) string {
	if mode := wirelessTagLineageMode(resource); mode != "" {
		if mode == "recreate" {
			return "write"
		}
		return mode
	}
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		if local, ok := strings.CutPrefix(resource, ns+wirelessTagBoundPrefix); ok {
			mode, _, found := strings.Cut(local, ":")
			if found && (mode == "write" || mode == "members" || mode == "delete") {
				return mode
			}
		}
	}
	return ""
}

// Bound rows are independent copies. Legacy selectors/baselines remain
// available for retained operations; new proofs bind the complete native parent
// zone graph so deletion can reobserve it even after the tag is absent.
func bindWirelessTagRows(ctx context.Context, c *client, rows []wirelessProfileTagObservation, policy bool) ([]wirelessProfileTagObservation, error) {
	out := append([]wirelessProfileTagObservation{}, rows...)
	graphs := map[string]wirelessZoneBindings{}
	if policy && len(rows) > 0 {
		profiles, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, exists := graphs[row.profileID]; exists {
				continue
			}
			var profile *wirelessProfileMetadata
			for _, got := range profiles {
				if got.id == row.profileID {
					copy := got
					profile = &copy
				}
			}
			if profile == nil {
				return nil, errConfigurationOwned
			}
			graph, err := c.wirelessProfileZoneBindings(ctx, *profile, nil)
			if err != nil {
				return nil, err
			}
			graphs[row.profileID] = graph
		}
	}
	for i, row := range rows {
		var graph any
		if policy {
			graph = graphs[row.profileID]
		}
		bound, err := wirelessTagDigest(map[string]any{"version": 4, "nativeSelector": row.resource, "zoneReferences": graph})
		if err != nil {
			return nil, err
		}
		at := strings.LastIndex(row.resource, ":")
		if at < 0 {
			return nil, errConfigurationTransition
		}
		out[i].resource = row.resource[:at+1] + bound
		out[i].baseline = wirelessTagBoundBaseline(row.baseline, graph)
	}
	return out, nil
}

func wirelessTagBaselineForRecord(ctx context.Context, c *client, row wirelessProfileTagObservation, policy bool, old wirelessTagMemberProof) (any, error) {
	if wirelessTagBindingMode(old.row.Operation.Resource) == "" {
		return row.baseline, nil
	}
	bound, err := bindWirelessTagRows(ctx, c, []wirelessProfileTagObservation{row}, policy)
	if err != nil {
		return nil, err
	}
	return wirelessTagOperationBaseline(old.row.Operation.Resource, bound[0].baseline)
}

// Read-only attribute verification needs no comparison key. It cannot publish a
// private receipt; that path separately requires its projected key and custody.
func (e *networkProfileConfiguration) boundTagReadback(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) ([]wirelessProfileTagObservation, bool, error) {
	if op == nil || wirelessTagBindingMode(op.Resource) == "" || wirelessTagPruneSelected(op.Resource) {
		return nil, false, errConfigurationTransition
	}
	check := op.DeepCopy()
	check.Phase = "Unknown"
	if !wirelessTagPrivateEnvelope(check) || !e.selectsProfileMetadata(check) {
		return nil, false, errConfigurationOwned
	}
	canonical := wirelessTagCanonical(op.Resource)
	ns := strings.SplitN(canonical, "/", 2)[0] + "/"
	kind, _, _ := networkProfileMetadataResource(canonical)
	selected := []wirelessProfileTagObservation{}
	for _, tags := range e.tagExecutors {
		if tags.policy != (ns == "wireless-policy-tags/") {
			continue
		}
		rows, err := tags.observe(ctx)
		if err != nil {
			return nil, false, err
		}
		rows, err = bindWirelessTagRows(ctx, tags.client, rows, tags.policy)
		if err != nil {
			return nil, false, err
		}
		if kind == "policy-cohort" {
			rows = policyRowsForProfile(rows, op.ParentID)
			resource, _, err := wirelessPolicyCohortResource(rows)
			if err != nil || canonical != ns+resource {
				return nil, false, errConfigurationStale
			}
		}
		for _, row := range rows {
			if kind == "policy-cohort" || canonical == ns+row.resource {
				if row.actual == nil || row.profileID != op.ParentID || op.Action == "Update" && row.tagID != op.TargetID || !reflect.DeepEqual(row.payload, row.actual.fields) {
					return nil, false, errConfigurationStale
				}
				selected = append(selected, row)
			}
		}
		if wirelessTagMembershipSelected(op.Resource) {
			ids, err := decodeWirelessTagMembershipScope(op.Resource)
			if err != nil {
				return nil, false, err
			}
			if err := tags.client.wirelessTagUnusedScopes(ctx, ids); err != nil {
				return nil, false, err
			}
		}
	}
	keys := []string{}
	for _, row := range selected {
		key, valid := wirelessTagLogical(row.resource)
		if !valid {
			return nil, false, errConfigurationOwned
		}
		keys = append(keys, key+":")
	}
	sort.Strings(keys)
	if len(keys) == 0 || len(keys) > 256 || wirelessTagGroupKey(keys) != wirelessTagProducedGroup(op.Resource) {
		return nil, false, errConfigurationOwned
	}
	return selected, true, nil
}
