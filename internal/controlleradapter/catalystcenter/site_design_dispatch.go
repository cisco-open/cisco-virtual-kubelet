// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func siteDesignOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Section != "sites" || op.CreatedResourceID != "" || op.TargetSerial != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || op.PreparedAt != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 {
		return false
	}
	if strings.HasPrefix(op.Resource, "floor-image-sync:") {
		_, err := floorImageReceiptKey(op.Resource)
		return err == nil && op.Action == "Update" && validAPIID(op.TargetID) && op.ParentID == ""
	}
	if plannedAPResourceKey(op.Resource) != "" {
		if (strings.HasPrefix(op.Resource, plannedAPObservePrefix) || strings.HasPrefix(op.Resource, plannedAPPrivateObservePrefix)) && op.Action != "Create" {
			return false
		}
		if plannedAPAppliedSelected(op) && op.Action != "Update" {
			return false
		}
		return validAPIID(op.ParentID) && (op.Action == "Create" && op.TargetID == "" || op.Action == "Update" && validAPIID(op.TargetID) && op.TargetID != op.ParentID)
	}
	return false
}

func (a *adapter) configureSiteDesignDispatch(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *sitePlannedAPExecutor) {
	e.designGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "sites", resource, action, identity, siteDesignOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			return e.sites.creatorRecordsMatch(state)
		})
	}
}

// Compose resource-specific senders with site ownership/key/state authority.
// Image synchronous receipts and planned-AP task protocols remain unchanged.
func (e *sitePlannedAPExecutor) guardPrivateDesign(ctx context.Context, observed []Site, write *configurationWrite) (*configurationWrite, error) {
	if write == nil || e.sites.comparison == nil {
		return write, nil
	}
	shape := &cvk.NetworkControllerConfigurationOperation{Section: "sites", Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
	if !siteDesignOperation(shape) || e.designGuard == nil {
		return nil, errConfigurationTransition
	}
	path := ""
	if strings.HasPrefix(write.resource, "floor-image-sync:") {
		for _, want := range e.images.desired {
			if want.synchronousOperationResource() == write.resource {
				path = want.floor
			}
		}
	} else {
		for _, want := range e.placements.desired {
			if plannedAPResourceMatches(write.resource, want) {
				path = want.floor
			}
		}
	}
	if !validSitePath(path) || path == "Global" {
		return nil, errConfigurationTransition
	}
	creator, owned := siteCreatorForResource(e.sites.creators, path)
	if !owned || creator.kind != "floor" || creator.keyID != e.sites.comparison.keyID {
		return nil, errConfigurationOwned
	}
	floorID := write.identity.parent
	if strings.HasPrefix(write.resource, "floor-image-sync:") {
		floorID = write.identity.target
	}
	if floorID != creator.id {
		return nil, errConfigurationOwned
	}
	if err := e.sites.comparison.check(ctx); err != nil {
		return nil, err
	}
	before, err := e.sites.client.siteNativeFootprint(ctx, observed, path, true)
	if err != nil {
		return nil, err
	}
	observe := func(ctx context.Context) error {
		if err := e.sites.comparison.check(ctx); err != nil {
			return err
		}
		current, err := e.sites.client.ListSites(ctx)
		if err != nil {
			return err
		}
		if err := e.sites.checkCreatorIdentities(current); err != nil {
			return err
		}
		if err := e.sites.checkPrivateBaselines(ctx, current); err != nil {
			return err
		}
		after, err := e.sites.client.siteNativeFootprint(ctx, current, path, true)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) {
			return errConfigurationStale
		}
		return e.sites.comparison.check(ctx)
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		if err := observe(ctx); err != nil {
			return err
		}
		if err := e.designGuard(ctx, write.resource, write.action, write.identity); err != nil {
			return err
		}
		return observe(ctx)
	})
	return write, nil
}
