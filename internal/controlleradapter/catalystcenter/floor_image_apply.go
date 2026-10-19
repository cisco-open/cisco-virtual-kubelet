// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (a *adapter) compileSiteImageApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if source == nil || config == nil || nc == nil {
		return plan, errConfigurationTransition
	}
	desired, placements, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
	if err != nil {
		return plan, err
	}
	if err = validateSiteExecution(desired); err != nil {
		return plan, err
	}
	executor := &sitePlannedAPExecutor{
		sites:      &siteConfigurationExecutor{client: a.client, desired: desired},
		placements: &plannedAPExecutor{client: a.client, desired: placements},
		images:     &floorImageExecutor{client: a.client, desired: images, configName: config.Name, configUID: string(config.UID), synchronous: true},
	}
	executor.images.declaration = &cvk.NetworkControllerConfigurationOperation{ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version}
	if nc.Status.Configuration != nil {
		executor.images.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
	}
	executor.placements.identities, err = plannedAPIdentityRecords(nc.Status.Configuration)
	if err != nil {
		return plan, err
	}
	executor.placements.retainIdentity = true
	executor.placements.identityOwner = string(config.UID)
	executor.placements.sourceMode = "i"
	if config.Spec.Source.ConfigMapRef != nil {
		executor.placements.sourceMode = "c"
	}
	executor.placements.applied, err = plannedAPAppliedRecords(nc.Status.Configuration)
	if err != nil {
		return plan, err
	}
	executor.placements.floorEffects, err = plannedAPFloorAuthorities(nc.Status.Configuration)
	if err != nil {
		return plan, err
	}
	executor.retainedGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "sites", resource, action, identity, siteOrDesignOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if err := executor.sites.creatorRecordsMatch(state); err != nil {
				return err
			}
			return executor.placements.identityRecordsMatch(state)
		})
	}
	executor.placements.dispatchGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "sites", resource, action, identity, siteDesignOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if err := executor.sites.creatorRecordsMatch(state); err != nil {
				return err
			}
			if err := executor.placements.identityRecordsMatch(state); err != nil {
				return err
			}
			if plannedAPObservationSelected(op) {
				return plannedAPIdentityCapacity(state, op)
			}
			if plannedAPAppliedSelected(op) {
				return plannedAPApplyCapacity(state, op)
			}
			return nil
		})
	}
	executor.sites.creators, err = siteCreatedRecords(nc.Status.Configuration)
	if err != nil {
		return plan, err
	}
	executor.sites.applied, err = siteAppliedRecords(nc.Status.Configuration)
	if err != nil {
		return plan, err
	}
	executor.sites.creatorOwner = string(config.UID)
	if sitePrivateRequired(nc.Status.Configuration) && !siteComparisonConfig(config) {
		return plan, errConfigurationSecret
	}
	privateAP := config.Annotations[plannedAPBaselineAnnotation] == "true"
	if plannedAPPrivateRequired(nc.Status.Configuration) && !privateAP {
		return plan, errConfigurationSecret
	}
	if privateAP && !siteComparisonConfig(config) {
		return plan, errConfigurationSecret
	}
	if siteComparisonConfig(config) {
		if config.Annotations[siteBulkAnnotation] == "true" {
			return plan, errConfigurationTransition
		}
		comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
		if err != nil {
			return plan, err
		}
		executor.sites.comparison = comparison
		if privateAP {
			executor.placements.comparison = comparison
		}
		executor.sites.privateResource = func(site desiredSite) string { return sitePrivateCreateResource(site, config, comparison) }
	}
	if err := a.configureSiteBulkExecution(config, source, executor.sites); err != nil {
		return plan, err
	}
	a.configureSingularSiteDispatch(nc, config, source, executor.sites)
	a.configureSiteDesignDispatch(nc, config, source, executor)
	executor.floorHandoff = config.Annotations[siteFloorHandoffAnnotation] == "true"
	if executor.floorHandoff {
		if !privateAP || executor.sites.comparison == nil {
			return plan, errConfigurationSecret
		}
		executor.configureFloorHandoff(func(ctx context.Context) error {
			var current configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
				return err
			}
			if current.UID != config.UID || current.Generation != config.Generation || current.Annotations[siteFloorHandoffAnnotation] != "true" || current.Annotations[plannedAPBaselineAnnotation] != "true" {
				return errConfigurationStale
			}
			return nil
		})
	}
	plan = configurationApplyPlan{executor: executor, section: "sites", sections: []configapi.NetworkControllerSectionStatus{{Name: "sites", ResourceCount: int64(executor.count())}},
		finalize:         func(ctx context.Context) error { return a.finalizeFloorImageOmissions(ctx, config, source, executor) },
		recoverKnownTask: siteKnownTaskRecovery(desired, placements...), observeCompletion: a.client.siteTaskCompletion(desired, placements...),
		receiptOperation: func(op *cvk.NetworkControllerConfigurationOperation) bool {
			return siteCreateSelected(op) || siteAppliedSelected(op) || plannedAPObservationSelected(op) || plannedAPAppliedSelected(op)
		}, recordCompletion: func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
			if plannedAPObservationSelected(op) || plannedAPAppliedSelected(op) {
				return a.recordPlannedAPIdentity(ctx, nc, config, source, executor.placements, op)
			}
			if siteAppliedSelected(op) {
				return a.recordSiteApplied(ctx, nc, config, source, executor.sites, op)
			}
			return a.recordSiteCreated(ctx, nc, config, source, executor.sites, op)
		}}
	if config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return a.bindSitePrunePlan(nc, config, source, plan, executor)
	}
	return plan, nil
}
