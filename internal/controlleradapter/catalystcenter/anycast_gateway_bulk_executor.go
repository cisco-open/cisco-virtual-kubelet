// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
)

// Preserve the existing semantic parent ordering, deferring a complete native
// fabric cohort if any member needs an uncreated parent. Preflight all ready
// cohorts before returning a closure; a bad later cohort cannot be ignored.
func (e *anycastGatewayExecutor) nextBulk(ctx context.Context) (*configurationWrite, error) {
	guard := e.bulkGuard
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	type cohort struct {
		path    string
		members []fabricAnycastIntent
		waiting bool
	}
	groups := map[string]*cohort{}
	for _, state := range states {
		if !state.deps.exists {
			if err := validateAnycastCreateIntent(state.want); err != nil {
				return nil, err
			}
		} else if _, err := e.client.anycastGatewayWrite(state.want, state.deps); err != nil {
			// A cohort waiting on another parent must not hide an existing
			// mismatching member and permit a write in an earlier cohort.
			return nil, err
		}
		id := state.deps.fabricID
		group := groups[id]
		if group == nil {
			group = &cohort{path: state.want.path}
			groups[id] = group
		}
		if state.want.path < group.path {
			group.path = state.want.path
		}
		group.members = append(group.members, state.want)
		group.waiting = group.waiting || state.deps.pendingParent
	}
	keys := []string{}
	for id := range groups {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool {
		if groups[keys[i]].path != groups[keys[j]].path {
			return groups[keys[i]].path < groups[keys[j]].path
		}
		return keys[i] < keys[j]
	})
	check := func(ctx context.Context) error {
		fresh, err := e.observe(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(states, fresh) {
			return errConfigurationStale
		}
		return nil
	}
	var first *configurationWrite
	waiting := false
	for _, id := range keys {
		group := groups[id]
		if group.waiting {
			waiting = true
			continue
		}
		// Validate cross-member allocation across the entire cohort before
		// selecting any bounded request. Chunking must not hide a late conflict.
		if _, err := e.client.observeAnycastBulk(ctx, group.members); err != nil {
			return nil, err
		}
		members := canonicalAnycastMembers(group.members)
		digest, err := anycastCohortDigest(members)
		if err != nil {
			return nil, err
		}
		for start := 0; start < len(members); start += anycastBulkRequestLimit {
			end := min(start+anycastBulkRequestLimit, len(members))
			var planned *configurationWrite
			planned, err = e.client.planAnycastGatewayBulk(ctx, members[start:end], func(ctx context.Context) error {
				if err := check(ctx); err != nil {
					return err
				}
				if planned == nil {
					return errConfigurationTransition
				}
				if err := guard(ctx, planned.resource, planned.identity.parent); err != nil {
					return err
				}
				return check(ctx)
			})
			if err != nil {
				return nil, err
			}
			if planned != nil && len(members) > anycastBulkRequestLimit {
				planned.resource, err = anycastChunkResource(start, len(members), digest, planned.resource)
				if err != nil {
					return nil, err
				}
			}
			if first == nil && planned != nil {
				first = planned
			}
		}
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	if first == nil && waiting {
		return nil, errConfigurationBusy
	}
	return first, nil
}
