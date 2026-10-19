// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
)

// Prospective membership is planning evidence only. It never verifies an
// association deletion: actual native tag absence is required by that path.
type profileSiteTagProjection map[string]wirelessProfileTagObservation

func profileSiteTagIdentity(parent, id string, policy bool) string {
	kind := "siteTags"
	if policy {
		kind = "policyTags"
	}
	return kind + "/" + parent + "/" + id
}
func (e *networkProfileConfiguration) tagRemovalProjection(ctx context.Context) (profileSiteTagProjection, error) {
	out := profileSiteTagProjection{}
	for _, tags := range e.tagExecutors {
		rows, err := tags.observe(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.actual == nil || wirelessTagRetainsMembers(row.actual.fields, row.payload) {
				continue
			}
			if tags.removal == nil {
				return nil, errConfigurationOwned
			}
			if err := tags.removal(ctx, row); err != nil {
				return nil, err
			}
			key := profileSiteTagIdentity(row.profileID, row.tagID, tags.policy)
			if _, exists := out[key]; exists {
				return nil, errConfigurationOwned
			}
			out[key] = row
		}
	}
	if e.tagPrune != nil {
		rows, err := e.tagPrune.snapshot(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[profileSiteTagIdentity(row.ParentID, row.TagID, row.Namespace == "wireless-policy-tags/")] = wirelessProfileTagObservation{profileID: row.ParentID, tagID: row.TagID, actual: row.Actual, payload: map[string]any{"siteIds": []string{}}}
		}
	}
	return out, nil
}
func (e *profileSitePruneExecutor) tagDependencyWrite(ctx context.Context) (*configurationWrite, error) {
	next, err := e.base.next(ctx)
	if err != nil {
		return nil, err
	}
	// Only an existing private, nonempty membership reduction may precede a
	// reference-blocked association prune. Ordinary additions/definition changes
	// keep the original complete-cohort prerequisite refusal.
	if next == nil || !(next.action == "Update" && wirelessTagMembershipSelected(next.resource) || next.action == "Delete" && wirelessTagPruneSelected(next.resource)) {
		return nil, errProfileSitePruneReferenced
	}
	projected, err := e.base.tagRemovalProjection(ctx)
	if err != nil {
		return nil, err
	}
	if len(projected) == 0 {
		return nil, errProfileSitePruneReferenced
	}
	observed, err := e.snapshotWithTags(ctx, projected)
	if err != nil {
		return nil, err
	}
	guardConfigurationPreflight(next, func(ctx context.Context) error {
		// Revalidate the complete projected removal cohort and every association
		// candidate in addition to the tag executor's native/key/source guards.
		for range 2 {
			fresh, err := e.base.tagRemovalProjection(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(projected, fresh) {
				return errConfigurationStale
			}
			rows, err := e.snapshotWithTags(ctx, fresh)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(observed, rows) {
				return errConfigurationStale
			}
		}
		return e.proof(ctx, e.predecessor)
	})
	return next, nil
}
