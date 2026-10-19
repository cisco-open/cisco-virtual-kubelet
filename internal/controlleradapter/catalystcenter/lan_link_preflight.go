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

type lanLinkPreflight struct {
	dependencies *lanLinkDependencies
	sessions     *lanActiveSessionSnapshot
	usage        *lanPoolUsage
}

// Initial preflight requires an idle LAN service. It observes native usage and
// refuses fewer than two conservatively remaining addresses for ADD_LINK. It
// neither reserves an address range nor proves operation/device eligibility.
func (c *client) lanLinkPreflight(ctx context.Context, want lanLinkIntent) (*lanLinkPreflight, error) {
	sessions, err := c.lanActiveSessions(ctx)
	if err != nil {
		return nil, err
	}
	if sessions.Maximum == 0 || len(sessions.Active) != 0 {
		return nil, errConfigurationBusy
	}
	deps, err := c.lanLinkDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	result := &lanLinkPreflight{dependencies: deps, sessions: sessions}
	if want.action == "ADD_LINK" {
		if deps.pool == nil {
			return nil, errConfigurationTransition
		}
		result.usage, err = c.lanPoolUsage(ctx, deps.pool.pool.primary)
		if err != nil {
			return nil, err
		}
		if result.usage.remaining() < 2 {
			return nil, errConfigurationBusy
		}
	}
	fresh, err := c.lanLinkDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	current, err := c.lanActiveSessions(ctx)
	if err != nil {
		return nil, err
	}
	if deps.observationDigest() != fresh.observationDigest() || !reflect.DeepEqual(sessions, current) {
		return nil, errConfigurationStale
	}
	return result, nil
}

func (s *lanLinkPreflight) observationDigest() string {
	raw, _ := json.Marshal(struct {
		Dependencies string
		Sessions     *lanActiveSessionSnapshot
		Usage        *lanPoolUsage
	}{s.dependencies.observationDigest(), s.sessions, s.usage})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (c *client) lanLinkPreflightTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want lanLinkIntent) (*lanLinkPreflight, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*lanLinkPreflight], error) {
		before, err := c.lanLinkPreflight(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*lanLinkPreflight]{}, err
		}
		endpoints := before.dependencies.adjacency.endpoints
		physical, err := bindMulticastRPDevices([]string{endpoints.primary.device.ID, endpoints.secondary.device.ID}, []Device{endpoints.primary.device, endpoints.secondary.device})
		if err != nil {
			return configurationPhysicalObservation[*lanLinkPreflight]{}, err
		}
		return configurationPhysicalObservation[*lanLinkPreflight]{state: before, physical: physical, digest: before.observationDigest()}, nil
	})
}
