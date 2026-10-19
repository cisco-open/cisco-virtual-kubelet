// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Resolve all affected managed devices before preparing a group journal. A
// successful result is evidence only: reservation and actor admission are still
// required before submit. Existing matching handoffs yield no create plan.
func (c *client) planManagedFabricL2Handoff(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricL2HandoffDeploymentIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	if reader == nil || namespace == "" {
		return nil, nil, errConfigurationDeviceAdmission
	}
	deps, err := c.fabricL2HandoffPrerequisites(ctx, want)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := resolveConfigurationManagedBindings(ctx, reader, namespace, deps.physical)
	if err != nil {
		return nil, nil, err
	}
	targets, err := configurationGroupTargets(namespace, bindings)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planFabricL2HandoffDeployment(ctx, want)
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.identity.target != deps.native.NetworkDeviceID || write.identity.parent != deps.native.FabricID || !strings.HasSuffix(write.resource, ":"+deps.digest)) {
		return nil, nil, errConfigurationStale
	}
	fresh, err := c.fabricL2HandoffPrerequisites(ctx, want)
	if err != nil {
		return nil, nil, err
	}
	if fresh.digest != deps.digest {
		return nil, nil, errConfigurationStale
	}
	refreshed, err := resolveConfigurationManagedBindings(ctx, reader, namespace, fresh.physical)
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(bindings, refreshed) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
