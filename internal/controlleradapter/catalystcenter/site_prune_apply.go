// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func sitePruneApplyShape(config *configapi.NetworkControllerConfig) bool {
	return config != nil && config.DeletionTimestamp == nil && siteComparisonConfig(config) && config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && (config.Spec.DeletionPolicy == "" || config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyRetain) && config.Annotations[siteBulkAnnotation] != "true"
}

type sitePruneExecutor struct {
	a      *adapter
	config *configapi.NetworkControllerConfig
	source *configSourceSnapshot
	base   *sitePlannedAPExecutor
}

func (e *sitePruneExecutor) count() int { return e.base.count() }
func (e *sitePruneExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action == "Delete" || siteRecreateSelected(op.Resource) {
		return false, errConfigurationTransition
	}
	return e.base.verify(ctx, op)
}

func (e *sitePruneExecutor) next(ctx context.Context) (*configurationWrite, error) {
	nc, err := e.a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	if err := e.a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, e.config); err != nil {
		return nil, err
	}
	if err := e.base.sites.creatorRecordsMatch(nc.Status.Configuration); err != nil {
		return nil, err
	}
	if _, err := siteRetiredRecords(nc.Status.Configuration); err != nil {
		return nil, err
	}
	cohorts, err := siteCurrentRetirementCohorts(nc.Status.Configuration)
	if err != nil {
		return nil, err
	}
	archives, err := siteHistoryCohorts(nc.Status.Configuration)
	if err != nil {
		return nil, err
	}
	retiredPaths, retiredIDs := map[string]bool{}, map[string]bool{}
	for _, cohort := range cohorts {
		retiredPaths[cohort.Path], retiredIDs[cohort.ID] = true, true
	}
	for _, cohort := range archives {
		retiredIDs[cohort.ID] = true
	}
	actual, err := e.a.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]Site{}
	for _, site := range actual {
		if retiredIDs[site.ID] || retiredPaths[sitePathDigest(site.NameHierarchy)] {
			return nil, errConfigurationOwned
		}
		byID[site.ID] = site
	}
	wanted := map[string]bool{}
	for _, site := range e.base.sites.desired {
		wanted[sitePathDigest(site.path)] = true
	}
	omitted := map[string]Site{}
	for resource, record := range e.base.sites.creators {
		path := siteResourcePathDigest(resource)
		if retiredPaths[path] || wanted[path] {
			continue
		}
		site, found := byID[record.id]
		if !found || sitePathDigest(site.NameHierarchy) != path || site.Type != record.kind || site.ParentID != record.receipt.Operation.ParentID {
			return nil, errConfigurationOwned
		}
		allowed, err := e.source.siteSelection.allows(site.NameHierarchy)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		for _, want := range e.base.sites.desired {
			if strings.HasPrefix(want.path, site.NameHierarchy+"/") {
				return nil, errSiteRetirementReferenced
			}
		}
		omitted[site.ID] = site
	}
	leaves := []Site{}
	for _, site := range omitted {
		leaf := true
		for _, child := range actual {
			if !strings.HasPrefix(child.NameHierarchy, site.NameHierarchy+"/") {
				continue
			}
			if _, ownedOmission := omitted[child.ID]; !ownedOmission {
				return nil, errSiteRetirementReferenced
			}
			leaf = false
		}
		if leaf {
			leaves = append(leaves, site)
		}
	}
	if len(leaves) > 0 {
		sort.Slice(leaves, func(i, j int) bool {
			a, b := strings.Count(leaves[i].NameHierarchy, "/"), strings.Count(leaves[j].NameHierarchy, "/")
			if a != b {
				return a > b
			}
			return leaves[i].NameHierarchy < leaves[j].NameHierarchy
		})
		return e.a.siteRetirementWrite(ctx, nc, e.config, e.source, leaves[0].ID)
	}
	if len(omitted) > 0 {
		return nil, errSiteRetirementReferenced
	}
	mutation, err := nextSiteMutation(e.base.sites.desired, actual)
	if err != nil {
		return nil, err
	}
	if mutation != nil && retiredPaths[sitePathDigest(mutation.desired.path)] {
		if mutation.action != "Create" {
			return nil, errConfigurationOwned
		}
		_, write, err := e.a.siteRecreationExecution(ctx, nc, e.config, e.source, mutation.desired.path)
		return write, err
	}
	// Retired declarations are handled only by their explicit recreation path.
	// Preserve normal geometry/image/AP execution for the other declarations.
	base := *e.base
	sites := *e.base.sites
	sites.desired = nil
	for _, site := range e.base.sites.desired {
		if !retiredPaths[sitePathDigest(site.path)] {
			sites.desired = append(sites.desired, site)
		}
	}
	base.sites = &sites
	return base.next(ctx)
}

func (a *adapter) bindSitePrunePlan(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, base configurationApplyPlan, executor *sitePlannedAPExecutor) (configurationApplyPlan, error) {
	if !sitePruneApplyShape(config) {
		return configurationApplyPlan{}, errConfigurationTransition
	}
	cohorts, err := siteCurrentRetirementCohorts(nc.Status.Configuration)
	if err != nil {
		return configurationApplyPlan{}, err
	}
	for _, want := range executor.sites.desired {
		if _, retired := cohorts[sitePathDigest(want.path)]; retired && (len(executor.placements.desired) > 0 || len(executor.images.desired) > 0) {
			return configurationApplyPlan{}, errConfigurationTransition
		}
	}
	plan := base
	plan.executor = &sitePruneExecutor{a: a, config: config, source: source, base: executor}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op != nil && op.Action == "Delete" {
			return siteRetirementKnownTask(op) && op.Phase == "Unknown"
		}
		return base.recoverKnownTask != nil && base.recoverKnownTask(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if op.Action == "Delete" {
			return a.observeSiteRetirementTask(ctx, config, source, op, now)
		}
		return base.observeCompletion(ctx, op, now)
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && (op.Action == "Delete" || base.receiptOperation(op))
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if op.Action == "Delete" {
			return a.recordSiteRetired(ctx, config, source, op.ID)
		}
		if siteRecreateSelected(op.Resource) {
			want, found := siteDesiredOperation(executor.sites.desired, op.Resource)
			if !found {
				return errConfigurationStale
			}
			current, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			observer, write, err := a.siteRecreationExecution(ctx, current, config, source, want.path)
			if err != nil {
				return err
			}
			if observer == nil || write != nil {
				return errConfigurationTransition
			}
			return a.recordSiteCreated(ctx, current, config, source, observer, op)
		}
		return base.recordCompletion(ctx, op)
	}
	return plan, nil
}
