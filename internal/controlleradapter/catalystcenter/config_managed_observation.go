// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type configurationPhysicalObservation[T any] struct {
	state    T
	physical []Device
	digest   string
}

// Bracket immutable managed identities with repeated native observations. This
// helper grants no reservation or native mutation authority and returns no
// partial evidence after a failed observation.
func observeConfigurationManagedTargets[T any](ctx context.Context, reader ctrlclient.Reader, namespace string, collect func(context.Context) (configurationPhysicalObservation[T], error)) (T, []contract.GroupTargetIdentity, error) {
	var empty T
	if reader == nil || namespace == "" || collect == nil {
		return empty, nil, errConfigurationDeviceAdmission
	}
	before, err := collect(ctx)
	if err != nil {
		return empty, nil, err
	}
	if before.digest == "" {
		return empty, nil, errConfigurationStale
	}
	bindings, err := resolveConfigurationManagedBindings(ctx, reader, namespace, before.physical)
	if err != nil {
		return empty, nil, err
	}
	targets, err := configurationGroupTargets(namespace, bindings)
	if err != nil {
		return empty, nil, err
	}
	after, err := collect(ctx)
	if err != nil {
		return empty, nil, err
	}
	if after.digest != before.digest || !reflect.DeepEqual(before.physical, after.physical) {
		return empty, nil, errConfigurationStale
	}
	fresh, err := resolveConfigurationManagedBindings(ctx, reader, namespace, after.physical)
	if err != nil {
		return empty, nil, err
	}
	if !reflect.DeepEqual(bindings, fresh) {
		return empty, nil, errConfigurationStale
	}
	return before.state, targets, nil
}
