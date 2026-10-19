// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type fabricPortAssignmentDependencies struct {
	prerequisites      json.RawMessage
	deviceID, fabricID string
	physical           []Device
	ports              []lanLinkInterface
	before             []fabricPortAssignment
	selection          *fabricPortAssignmentSelection
	digest             string
}

func (c *client) fabricPortAssignmentDependencies(ctx context.Context, want fabricPortAssignmentDeploymentIntent) (*fabricPortAssignmentDependencies, error) {
	deps, err := c.observeFabricPortAssignments(ctx, want)
	if err != nil {
		return nil, err
	}
	if deps.selection == nil {
		return nil, errConfigurationStale
	}
	return deps, nil
}
func (c *client) observeFabricPortAssignments(ctx context.Context, want fabricPortAssignmentDeploymentIntent) (*fabricPortAssignmentDependencies, error) {
	rows, err := expandFabricPortAssignments(want.assignments)
	if err != nil || len(rows) == 0 || !slices.Contains(want.device.roles, "EDGE_NODE") {
		return nil, errInvalidResponse
	}
	base, err := c.fabricDeviceDependencies(ctx, want.device)
	if err != nil {
		return nil, err
	}
	actual, present := base.before[base.target.ID]
	if !present || !fabricDeviceConfigurationMatches(base.native, actual) {
		return nil, errConfigurationBusy
	}
	interfaces, err := c.lanLinkInterfaces(ctx, base.target.ID)
	if err != nil {
		return nil, err
	}
	ports := []lanLinkInterface{}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, row := range rows {
		port, err := bindLANLinkInterface(base.target.ID, *row.Interface, interfaces)
		if err != nil {
			return nil, err
		}
		if ids[port.ID] {
			return nil, errInvalidResponse
		}
		ids[port.ID] = true
		names[strings.ToLower(port.PortName)] = true
		ports = append(ports, port)
	}
	channels, err := c.fabricPortChannels(ctx, base.native.FabricID, base.target.ID)
	if err != nil {
		return nil, err
	}
	for _, channel := range channels {
		for _, port := range channel.Interfaces {
			if names[strings.ToLower(port)] {
				return nil, errConfigurationStale
			}
		}
	}
	vlans, err := c.fabricPortAssignmentVLANs(ctx, base.native.FabricID, rows)
	if err != nil {
		return nil, err
	}
	groups, err := c.fabricPortAssignmentGroups(ctx, rows, vlans)
	if err != nil {
		return nil, err
	}
	before, err := c.fabricPortAssignments(ctx, base.native.FabricID, base.target.ID)
	if err != nil {
		return nil, err
	}
	selection, err := selectFabricPortAssignments(rows, before)
	if err != nil && !errors.Is(err, errConfigurationStale) {
		return nil, err
	}
	prerequisites, err := json.Marshal([]any{base.digest, rows, ports, channels, vlans, groups})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal([]any{json.RawMessage(prerequisites), before})
	if err != nil {
		return nil, err
	}
	return &fabricPortAssignmentDependencies{prerequisites: prerequisites, deviceID: base.target.ID, fabricID: base.native.FabricID, physical: base.physical, ports: ports, before: before, selection: selection, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}

func (c *client) fabricPortAssignmentTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricPortAssignmentDeploymentIntent) (*fabricPortAssignmentDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricPortAssignmentDependencies], error) {
		deps, err := c.fabricPortAssignmentDependencies(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*fabricPortAssignmentDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricPortAssignmentDependencies]{state: deps, physical: deps.physical, digest: deps.digest}, nil
	})
}
