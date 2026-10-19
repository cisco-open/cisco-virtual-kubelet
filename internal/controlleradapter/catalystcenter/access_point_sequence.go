// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"sort"
	"sync/atomic"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

const apHostnameOnlyCohort = "(hostname only)"

type accessPointConfigurationCohort struct {
	name    string
	members []accessPointDeviceIntent
}

type accessPointConfigurationPlannedCohort struct {
	name string
	plan *accessPointConfigurationPlan
}

type accessPointConfigurationSequence struct {
	digest  string
	cohorts []accessPointConfigurationPlannedCohort
}

type accessPointConfigurationSequenceGuard func(context.Context, string, string, string, []accessPointConfigurationMember) error

// Preserve the pinned module's named configuration groups, including distinct
// names with identical settings and its separate hostname-only group. Input is
// the parsed configuration-stage selection, not the whole inventory manifest;
// assignment/provisioning-only devices must remain in their owning stages.
func groupAccessPointConfigurations(wants []accessPointDeviceIntent) ([]accessPointConfigurationCohort, error) {
	if len(wants) == 0 || len(wants) > 1000 {
		return nil, errAccessPointModel
	}
	groups := map[string][]accessPointDeviceIntent{}
	names, serials := map[string]bool{}, map[string]bool{}
	for _, want := range wants {
		name, err := plannedAPText(want.inventory.reference.Name)
		if err != nil || names[name] {
			return nil, errAccessPointIdentity
		}
		serial, err := topologyidentity.CanonicalPhysicalIdentity(want.inventory.reference.Serial)
		if err != nil || serials[serial] {
			return nil, errAccessPointIdentity
		}
		names[name], serials[serial] = true, true
		key := apHostnameOnlyCohort
		if want.profile != nil {
			key, err = plannedAPText(want.profile.name)
			if err != nil || key == apHostnameOnlyCohort {
				return nil, errAccessPointModel
			}
		}
		groups[key] = append(groups[key], want)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	cohorts := make([]accessPointConfigurationCohort, 0, len(keys))
	for _, key := range keys {
		frozen, err := freezeAccessPointConfiguration(groups[key])
		if err != nil {
			return nil, err
		}
		cohorts = append(cohorts, accessPointConfigurationCohort{name: key, members: frozen})
	}
	return cohorts, nil
}

type accessPointConfigurationSequenceSnapshot struct {
	digest string
	groups []*accessPointConfigurationSnapshot
}

func (c *client) accessPointConfigurationSequenceSnapshot(ctx context.Context, cohorts []accessPointConfigurationCohort) (*accessPointConfigurationSequenceSnapshot, error) {
	catalog, err := c.accessPointDependencyCatalog(ctx)
	if err != nil {
		return nil, err
	}
	assignments := map[string]map[string]bool{}
	ids, serials, macs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	groups := make([]*accessPointConfigurationSnapshot, 0, len(cohorts))
	bindings := make([]map[string]string, 0, len(cohorts))
	for _, cohort := range cohorts {
		group, err := c.accessPointConfigurationSnapshotWithCatalog(ctx, cohort.members, catalog, assignments)
		if err != nil {
			return nil, err
		}
		for _, member := range group.members {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(member.Serial)
			if err != nil || ids[member.DeviceID] || serials[serial] || macs[member.EthernetMAC] {
				return nil, errAccessPointIdentity
			}
			ids[member.DeviceID], serials[serial], macs[member.EthernetMAC] = true, true, true
		}
		groups = append(groups, group)
		bindings = append(bindings, map[string]string{"name": cohort.name, "digest": group.digest})
	}
	digest, err := wirelessTagDigest(bindings)
	if err != nil {
		return nil, err
	}
	return &accessPointConfigurationSequenceSnapshot{digest: digest, groups: groups}, nil
}

// Preflight every cohort before exposing any write, and recheck the complete
// sequence around the caller's exact journal/source/provisioning/physical guard.
// Any selected attempt consumes this whole plan, even on denial or uncertainty.
// The caller must replan through durable journal/settlement recovery before
// selecting another cohort; this is not a multi-mutation execution loop.
func (c *client) planAccessPointConfigurationSequence(ctx context.Context, wants []accessPointDeviceIntent, guard accessPointConfigurationSequenceGuard) (*accessPointConfigurationSequence, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	cohorts, err := groupAccessPointConfigurations(wants)
	if err != nil {
		return nil, err
	}
	before, err := c.accessPointConfigurationSequenceSnapshot(ctx, cohorts)
	if err != nil {
		return nil, err
	}
	check := func(ctx context.Context) error {
		fresh, err := c.accessPointConfigurationSequenceSnapshot(ctx, cohorts)
		if err != nil {
			return err
		}
		if fresh.digest != before.digest {
			return errConfigurationStale
		}
		return nil
	}
	var used atomic.Bool
	planned := make([]accessPointConfigurationPlannedCohort, 0, len(cohorts))
	for i, cohort := range cohorts {
		plan, err := c.planAccessPointConfiguration(ctx, cohort.members, func(ctx context.Context, digest string, members []accessPointConfigurationMember) error {
			if digest != before.groups[i].digest {
				return errConfigurationStale
			}
			if err := check(ctx); err != nil {
				return err
			}
			if err := guard(ctx, before.digest, cohort.name, digest, members); err != nil {
				return err
			}
			return check(ctx)
		})
		if err != nil {
			return nil, err
		}
		if plan.digest != before.groups[i].digest {
			return nil, errConfigurationStale
		}
		submit := plan.submit
		plan.submit = func(ctx context.Context) (Task, error) {
			if !used.CompareAndSwap(false, true) {
				return Task{}, errConfigurationTransition
			}
			return submit(ctx)
		}
		planned = append(planned, accessPointConfigurationPlannedCohort{name: cohort.name, plan: plan})
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	return &accessPointConfigurationSequence{digest: before.digest, cohorts: planned}, nil
}
