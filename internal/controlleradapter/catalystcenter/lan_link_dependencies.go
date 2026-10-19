// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type lanLinkDependencies struct {
	adjacency *lanLinkAdjacencySnapshot
	pool      *lanLinkPoolDependencies
}

// Keep pool/session observation bracketed by exact endpoint/adjacency reads.
// DELETE_LINK without a model pool has no pool dependency; ownership and delete
// permission must still be established by the eventual admitted executor.
func (c *client) lanLinkDependencies(ctx context.Context, want lanLinkIntent) (*lanLinkDependencies, error) {
	adjacency, err := c.lanLinkAdjacencySnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	result := &lanLinkDependencies{adjacency: adjacency}
	if want.pool != "" {
		result.pool, err = c.lanLinkPoolDependencies(ctx, want, adjacency.endpoints)
		if err != nil {
			return nil, err
		}
	}
	if err := c.revalidateLANLinkAdjacency(ctx, want, adjacency); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *lanLinkDependencies) observationDigest() string {
	type poolObservation struct {
		PrimarySite, SecondarySite       Site
		PrimaryOrigins, SecondaryOrigins []lanLinkDiscoveryOrigin
		PoolDigest                       string
	}
	var pool *poolObservation
	if s.pool != nil {
		pool = &poolObservation{s.pool.primary.site, s.pool.secondary.site, s.pool.primary.origins, s.pool.secondary.origins, s.pool.pool.digest}
	}
	raw, _ := json.Marshal(struct {
		Adjacency string
		Pool      *poolObservation
	}{s.adjacency.observationDigest(), pool})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (c *client) lanLinkDependencyTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want lanLinkIntent) (*lanLinkDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*lanLinkDependencies], error) {
		deps, err := c.lanLinkDependencies(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkDependencies]{}, err
		}
		endpoints := deps.adjacency.endpoints
		physical, err := bindMulticastRPDevices([]string{endpoints.primary.device.ID, endpoints.secondary.device.ID}, []Device{endpoints.primary.device, endpoints.secondary.device})
		if err != nil {
			return configurationPhysicalObservation[*lanLinkDependencies]{}, err
		}
		return configurationPhysicalObservation[*lanLinkDependencies]{state: deps, physical: physical, digest: deps.observationDigest()}, nil
	})
}
