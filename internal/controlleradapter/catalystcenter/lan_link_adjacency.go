// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type lanLinkAdjacencySnapshot struct {
	endpoints *lanLinkEndpointSnapshot
	link      *lanPhysicalLink
}

// Bind exact native ports and their entire incident topology. An absent link
// proves only native observation; it is not a delete receipt or free-port proof.
func (c *client) lanLinkAdjacencySnapshot(ctx context.Context, want lanLinkIntent) (*lanLinkAdjacencySnapshot, error) {
	endpoints, err := c.lanLinkEndpointSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	topology, err := c.lanPhysicalTopology(ctx)
	if err != nil {
		return nil, err
	}
	link, err := bindLANLinkAdjacency(endpoints, topology)
	if err != nil {
		return nil, err
	}
	return &lanLinkAdjacencySnapshot{endpoints: endpoints, link: link}, nil
}

func (s *lanLinkAdjacencySnapshot) observationDigest() string {
	raw, _ := json.Marshal(struct {
		Primary, Secondary         Device
		PrimaryPort, SecondaryPort lanLinkInterface
		Link                       *lanPhysicalLink
	}{s.endpoints.primary.device, s.endpoints.secondary.device, s.endpoints.primary.port, s.endpoints.secondary.port, s.link})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (c *client) revalidateLANLinkAdjacency(ctx context.Context, want lanLinkIntent, before *lanLinkAdjacencySnapshot) error {
	if before == nil {
		return errConfigurationStale
	}
	after, err := c.lanLinkAdjacencySnapshot(ctx, want)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return errConfigurationStale
	}
	return nil
}

// Reuse complete repeated native/managed identity binding. This is footprint
// evidence, not a mutation permit, pool/discovery eligibility or completion.
func (c *client) lanLinkAdjacencyTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want lanLinkIntent) (*lanLinkAdjacencySnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*lanLinkAdjacencySnapshot], error) {
		snapshot, err := c.lanLinkAdjacencySnapshot(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkAdjacencySnapshot]{}, err
		}
		physical, err := bindMulticastRPDevices([]string{snapshot.endpoints.primary.device.ID, snapshot.endpoints.secondary.device.ID}, []Device{snapshot.endpoints.primary.device, snapshot.endpoints.secondary.device})
		if err != nil {
			return configurationPhysicalObservation[*lanLinkAdjacencySnapshot]{}, err
		}
		return configurationPhysicalObservation[*lanLinkAdjacencySnapshot]{state: snapshot, physical: physical, digest: snapshot.observationDigest()}, nil
	})
}
