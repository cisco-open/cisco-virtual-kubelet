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

type fabricPortChannelDependencies struct {
	deviceID, fabricID     string
	physical               []Device
	ports                  []lanLinkInterface
	before                 []fabricPortChannel
	existing               *fabricPortChannel
	conflict               bool
	digest, recoveryDigest string
}

// A model-local ID never resolves native identity. Only a complete member set
// and requested attributes may match an observed channel. Shared or partially
// overlapping members are a conflict, not permission to modify/adopt a channel.
func selectFabricPortChannel(want fabricPortChannelIntent, rows []fabricPortChannel) (*fabricPortChannel, error) {
	if want.validate() != nil {
		return nil, errInvalidResponse
	}
	// The qualified 2.3.7.6 reader has no VLAN readback. Do not infer that optional
	// VLAN intent is compliant, or start an operation we cannot later verify.
	if want.NativeVLAN != nil || want.AllowedVLANs != nil {
		return nil, errConfigurationTransition
	}
	desired := slices.Clone(want.Interfaces)
	slices.Sort(desired)
	members := map[string]bool{}
	for _, name := range desired {
		members[strings.ToLower(name)] = true
	}
	var selected *fabricPortChannel
	for _, row := range rows {
		overlaps := false
		for _, name := range row.Interfaces {
			overlaps = overlaps || members[strings.ToLower(name)]
		}
		if !overlaps {
			continue
		}
		actual := slices.Clone(row.Interfaces)
		slices.Sort(actual)
		if selected != nil || !slices.Equal(actual, desired) || row.Type != want.Type || want.Protocol != nil && row.Protocol != *want.Protocol || want.Description != nil && (row.Description == nil || *row.Description != *want.Description) {
			return nil, errConfigurationStale
		}
		copy := row
		copy.Interfaces = actual
		if row.Description != nil {
			description := *row.Description
			copy.Description = &description
		}
		selected = &copy
	}
	return selected, nil
}

func (c *client) fabricPortChannelDependencies(ctx context.Context, want fabricPortChannelDeploymentIntent) (*fabricPortChannelDependencies, error) {
	deps, err := c.observeFabricPortChannel(ctx, want)
	if err != nil {
		return nil, err
	}
	if deps.conflict {
		return nil, errConfigurationStale
	}
	return deps, nil
}

// Observation retains conflicting evidence for Report; execution rejects it above.
func (c *client) observeFabricPortChannel(ctx context.Context, want fabricPortChannelDeploymentIntent) (*fabricPortChannelDependencies, error) {
	if want.channel.validate() != nil || !slices.Contains(want.device.roles, "EDGE_NODE") {
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
	ports, err := bindFabricPortChannelInterfaces(base.target.ID, want.channel, interfaces)
	if err != nil {
		return nil, err
	}
	rows, err := c.fabricPortChannels(ctx, base.native.FabricID, base.target.ID)
	if err != nil {
		return nil, err
	}
	existing, err := selectFabricPortChannel(want.channel, rows)
	conflict := errors.Is(err, errConfigurationStale)
	if err != nil && !conflict {
		return nil, err
	}
	assignments, err := c.fabricPortAssignments(ctx, base.native.FabricID, base.target.ID)
	if err != nil {
		return nil, err
	}
	members := map[string]bool{}
	for _, port := range want.channel.Interfaces {
		members[strings.ToLower(port)] = true
	}
	for _, assignment := range assignments {
		if members[strings.ToLower(assignment.Interface)] {
			return nil, errConfigurationStale
		}
	}
	evidence := []any{base.digest, want.channel, ports, rows, assignments}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	// A create can add exactly the requested channel. Preserve all other native
	// channels and the physical/base identities in the recovery digest.
	baseline := make([]fabricPortChannel, 0, len(rows))
	for _, row := range rows {
		if existing == nil || row.ID != existing.ID {
			baseline = append(baseline, row)
		}
	}
	evidence[3] = baseline
	recovery, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	return &fabricPortChannelDependencies{deviceID: base.target.ID, fabricID: base.native.FabricID, physical: base.physical, ports: ports, before: rows, existing: existing, conflict: conflict, digest: fmt.Sprintf("%x", sha256.Sum256(raw)), recoveryDigest: fmt.Sprintf("%x", sha256.Sum256(recovery))}, nil
}

// Reuse the shared two-observation native/Kubernetes binding boundary. This is
// evidence only; it creates neither reservations nor submission authority.
func (c *client) fabricPortChannelTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricPortChannelDeploymentIntent) (*fabricPortChannelDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricPortChannelDependencies], error) {
		deps, err := c.fabricPortChannelDependencies(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*fabricPortChannelDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricPortChannelDependencies]{state: deps, physical: deps.physical, digest: deps.digest}, nil
	})
}
