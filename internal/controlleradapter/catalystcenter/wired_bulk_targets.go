// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *client) wiredBulkTargets(ctx context.Context, reader ctrlclient.Reader, namespace, path string, members []wiredBulkMember, dependencies ...wiredBulkDependencyObserver) (*wiredBulkSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*wiredBulkSnapshot], error) {
		snapshot, err := c.wiredBulkSnapshot(ctx, path, members, dependencies...)
		if err != nil {
			return configurationPhysicalObservation[*wiredBulkSnapshot]{}, err
		}
		return configurationPhysicalObservation[*wiredBulkSnapshot]{state: snapshot, physical: snapshot.physical(), digest: snapshot.digest}, nil
	})
}

// Reuse all existing managed identity/alias/Node checks. This does not reserve
// devices, authorize a claim, publish completion or enable public dispatch.
func (c *client) planManagedWiredBulkProvision(ctx context.Context, reader ctrlclient.Reader, namespace, path string, members []wiredBulkMember, guard wiredBulkGuard, dependencies ...wiredBulkDependencyObserver) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.wiredBulkTargets(ctx, reader, namespace, path, members, dependencies...)
	if err != nil {
		return nil, nil, err
	}
	if before.mask == 0 {
		return nil, targets, nil
	}
	if guard == nil {
		return nil, nil, errConfigurationDeviceAdmission
	}
	check := func(ctx context.Context) error {
		fresh, current, err := c.wiredBulkTargets(ctx, reader, namespace, path, members, dependencies...)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, fresh) || !reflect.DeepEqual(targets, current) {
			return errConfigurationStale
		}
		return nil
	}
	write, err := c.planWiredBulkProvision(ctx, path, members, func(ctx context.Context, physical []Device) error {
		if !reflect.DeepEqual(physical, before.physical()) {
			return errConfigurationStale
		}
		if err := check(ctx); err != nil {
			return err
		}
		if err := guard(ctx, physical); err != nil {
			return err
		}
		return check(ctx)
	}, dependencies...)
	if err != nil {
		return nil, nil, err
	}
	if write == nil || write.resource != before.resource(before.mask) {
		return nil, nil, errConfigurationStale
	}
	write.identity.groupTargets = targets
	return write, targets, nil
}
