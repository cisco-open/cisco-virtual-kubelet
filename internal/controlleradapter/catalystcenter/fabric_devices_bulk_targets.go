// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *fabricDevicesBulkSnapshot) observationDigest() string {
	digests := []string{}
	for _, entry := range s.entries {
		digests = append(digests, entry.digest)
	}
	raw, _ := json.Marshal(digests)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Bind the full incoming/peer/parent footprint using the existing repeated
// native observer and immutable CiscoDevice/Node identity resolver.
func (c *client) fabricDevicesBulkTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, wants []fabricDeviceIntent) (*fabricDevicesBulkSnapshot, []contract.GroupTargetIdentity, error) {
	return c.fabricDevicesBulkPreflightTargets(ctx, reader, namespace, wants, nil)
}

func (c *client) fabricDevicesBulkPreflightTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, wants []fabricDeviceIntent, proposal *fabricDevicesParentProposal) (*fabricDevicesBulkSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricDevicesBulkSnapshot], error) {
		snapshot, err := c.fabricDevicesBulkPreflightSnapshot(ctx, wants, proposal)
		if err != nil {
			return configurationPhysicalObservation[*fabricDevicesBulkSnapshot]{}, err
		}
		return configurationPhysicalObservation[*fabricDevicesBulkSnapshot]{state: snapshot, physical: snapshot.physical, digest: snapshot.observationDigest()}, nil
	})
}

// This is preparation evidence, not a claim or dispatch permit. Every native
// and managed binding is rechecked around the caller's complete-footprint guard.
func (c *client) planManagedFabricDevicesBulk(ctx context.Context, reader ctrlclient.Reader, namespace string, wants []fabricDeviceIntent, guard func(context.Context, []Device) error) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.fabricDevicesBulkTargets(ctx, reader, namespace, wants)
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
		fresh, current, err := c.fabricDevicesBulkTargets(ctx, reader, namespace, wants)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, fresh) || !reflect.DeepEqual(targets, current) {
			return errConfigurationStale
		}
		return nil
	}
	write, err := c.planFabricDevicesBulk(ctx, wants, func(ctx context.Context, physical []Device) error {
		if !reflect.DeepEqual(physical, before.physical) {
			return errConfigurationStale
		}
		if err := check(ctx); err != nil {
			return err
		}
		if err := guard(ctx, physical); err != nil {
			return err
		}
		return check(ctx)
	})
	if err != nil {
		return nil, nil, err
	}
	if write == nil || write.resource != before.resource(before.mask) || write.identity.target != before.fabric || write.identity.parent != before.fabric {
		return nil, nil, errConfigurationStale
	}
	if err := check(ctx); err != nil {
		return nil, nil, err
	}
	write.identity.groupTargets = targets
	return write, targets, nil
}
