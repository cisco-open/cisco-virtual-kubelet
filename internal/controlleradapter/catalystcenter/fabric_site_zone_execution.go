// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
)

// Parent and child resources retain stable journal identities. Validate all
// existing zones before allowing an earlier site creation; a missing parent is
// expected only while the site's own executor still has creation work.
type fabricSiteZoneExecutor struct {
	sites *fabricSiteExecutor
	zones *fabricZoneExecutor
}

func (e *fabricSiteZoneExecutor) count() int { return e.sites.count() + e.zones.count() }
func (e *fabricSiteZoneExecutor) next(ctx context.Context) (*configurationWrite, error) {
	states, err := e.zones.observe(ctx)
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		if state.exists && state.actual.Authentication != state.want.authentication {
			return nil, errConfigurationTransition
		}
	}
	site, err := e.sites.next(ctx)
	if err != nil {
		return nil, err
	}
	if site != nil {
		return site, nil
	}
	return e.zones.next(ctx)
}
func (e *fabricSiteZoneExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if strings.HasPrefix(op.Resource, "fabric-zone:") {
		return e.zones.verify(ctx, op)
	}
	return e.sites.verify(ctx, op)
}
func (e *fabricSiteZoneExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	sites, err := e.sites.report(ctx)
	if err != nil {
		return nil, err
	}
	zones, err := e.zones.report(ctx)
	if err != nil {
		return nil, err
	}
	return append(sites, zones...), nil
}
