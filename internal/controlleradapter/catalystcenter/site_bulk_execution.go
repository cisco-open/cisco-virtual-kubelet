// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Transport selection is outside the NetAsCode stripe, like upstream's
// use_bulk_api module option. Existing manifests retain singular execution.
const siteBulkAnnotation = "cisco.vk/catalyst-center-bulk-sites"

func (a *adapter) configureSiteBulkExecution(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, executor *siteConfigurationExecutor) error {
	option := config.Annotations[siteBulkAnnotation]
	if option != "" && option != "false" && option != "true" {
		return errInvalidResponse
	}
	executor.bulkCreate = option == "true"
	executor.bulkGuard = func(ctx context.Context, resource, rootID string) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
			return errConfigurationTransition
		}
		op := nc.Status.Configuration.Operation
		if op.Phase != "Submitting" || op.Section != "sites" || op.Action != "Create" || op.Resource != resource || op.ParentID != rootID || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version || op.TargetID != "" || op.TargetSerial != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || op.SubmittedAt == nil || op.TaskID != "" {
			return errConfigurationStale
		}
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.Section == "sites" && owner.ConfigUID == string(config.UID) && owner.ConfigName == config.Name {
				return nil
			}
		}
		return errConfigurationOwned
	}
	return nil
}

func (e *siteConfigurationExecutor) bulkWrite(ctx context.Context, actual []Site) (*configurationWrite, error) {
	if e.bulkGuard == nil {
		return nil, errConfigurationTransition
	}
	binding, err := newSiteBulkBinding(e.desired, actual)
	if err != nil {
		return nil, err
	}
	resource, err := binding.resource(actual)
	if err != nil {
		return nil, err
	}
	var rootID string
	for _, site := range actual {
		if site.Type == "global" && site.NameHierarchy == "Global" {
			rootID = site.ID
		}
	}
	if !validAPIID(rootID) {
		return nil, errInvalidResponse
	}
	submit, err := e.client.planGuardedSiteBulkCreate(ctx, e.desired, func(ctx context.Context) error {
		latest, err := e.client.ListSites(ctx)
		if err != nil {
			return err
		}
		prepared, err := newSiteBulkBinding(e.desired, latest)
		if err != nil {
			return err
		}
		current, err := prepared.resource(latest)
		if err != nil {
			return err
		}
		if current != resource {
			return errConfigurationStale
		}
		return e.bulkGuard(ctx, resource, rootID)
	})
	if err != nil {
		return nil, err
	}
	if submit == nil {
		return nil, errConfigurationStale
	}
	return &configurationWrite{resource: resource, action: "Create", identity: configurationResourceIdentity{parent: rootID}, submit: submit}, nil
}

// Recovery recognizes the resource even if bulk transport is later disabled.
// A retained Accepted/Unknown operation must never be replanned as a new batch.
func (e *siteConfigurationExecutor) verifyBulk(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Section != "sites" || op.Action != "Create" || !validAPIID(op.ParentID) || op.TargetID != "" || op.TargetSerial != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	actual, err := e.client.ListSites(ctx)
	if err != nil {
		return false, err
	}
	rootMatches := false
	for _, site := range actual {
		if site.Type == "global" && site.NameHierarchy == "Global" && site.ID == op.ParentID {
			rootMatches = true
		}
	}
	if !rootMatches {
		return false, errConfigurationStale
	}
	return verifySiteBulkBinding(e.desired, actual, op.Resource)
}
