// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type sdaTransitSourceSnapshot struct {
	entries  map[string]*sdaTransitSnapshot
	physical []Device
	digest   string
}

// Preflight every declaration before preparing the first physical action.
// Shared inventory/site/transit/assignment reads are local to this observation.
func (c *client) sdaTransitSourceSnapshot(ctx context.Context, wants []sdaTransitIntent) (*sdaTransitSourceSnapshot, error) {
	if len(wants) == 0 || len(wants) > 1000 {
		return nil, errConfigurationTransition
	}
	count := 0
	names := map[string]bool{}
	frozen := make([]sdaTransitIntent, len(wants))
	for i, want := range wants {
		if !validProfileName(want.name) || names[want.name] || len(want.controlPlanes) == 0 {
			return nil, errConfigurationTransition
		}
		names[want.name] = true
		count += len(want.controlPlanes)
		if count > 1000 {
			return nil, errConfigurationDeviceAdmission
		}
		frozen[i] = freezeSDATransitIntent(want)
	}
	sort.Slice(frozen, func(i, j int) bool { return frozen[i].name < frozen[j].name })
	view, err := c.sdaTransitNativeInventory(ctx)
	if err != nil {
		return nil, err
	}
	result := &sdaTransitSourceSnapshot{entries: map[string]*sdaTransitSnapshot{}}
	used := map[string]bool{}
	identities := []any{}
	for _, want := range frozen {
		if _, exists := result.entries[want.name]; exists {
			return nil, errConfigurationTransition
		}
		s, err := c.sdaTransitSnapshotWithInventory(ctx, want, view)
		if err != nil {
			return nil, err
		}
		if s.actual != nil && !sdaTransitMatches(s.native, *s.actual) {
			return nil, errConfigurationTransition
		}
		for _, device := range s.physical {
			if used[device.ID] {
				return nil, errConfigurationTransition
			}
			used[device.ID] = true
			result.physical = append(result.physical, device)
		}
		result.entries[want.name] = s
		identities = append(identities, []any{want.name, s.digest, s.actual})
	}
	sort.Slice(result.physical, func(i, j int) bool { return result.physical[i].ID < result.physical[j].ID })
	raw, err := json.Marshal([]any{identities, result.physical})
	if err != nil {
		return nil, err
	}
	result.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return result, nil
}

// The complete read dependency population is validated through the existing
// managed observer. Only the selected immutable subset becomes an action group.
func (c *client) sdaTransitSourceTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, wants []sdaTransitIntent) (*sdaTransitSourceSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*sdaTransitSourceSnapshot], error) {
		s, err := c.sdaTransitSourceSnapshot(ctx, wants)
		if err != nil {
			return configurationPhysicalObservation[*sdaTransitSourceSnapshot]{}, err
		}
		return configurationPhysicalObservation[*sdaTransitSourceSnapshot]{state: s, physical: s.physical, digest: s.digest}, nil
	})
}

func declaredSDATransitSource(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) ([]sdaTransitIntent, error) {
	if config == nil {
		return nil, errConfigurationTransition
	}
	if source != nil {
		return parseSDATransitIntents(source.raw)
	}
	if config.Spec.Source.Inline != nil {
		return parseSDATransitIntents(config.Spec.Source.Inline.Raw)
	}
	return nil, errConfigurationTransition
}

func (a *adapter) preflightSDATransitSource(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent) (*sdaTransitSnapshot, []contract.GroupTargetIdentity, error) {
	wants, err := declaredSDATransitSource(config, source)
	if err != nil {
		return nil, nil, err
	}
	s, targets, err := a.client.sdaTransitSourceTargets(ctx, a.statusReader, config.Namespace, wants)
	if err != nil {
		return nil, nil, err
	}
	selected, exists := s.entries[want.name]
	if !exists {
		return nil, nil, errConfigurationStale
	}
	ids := map[string]bool{}
	for _, device := range selected.physical {
		ids[device.ID] = true
	}
	group := []contract.GroupTargetIdentity{}
	for _, target := range targets {
		if ids[target.InventoryID] {
			group = append(group, target)
		}
	}
	group, err = contract.CanonicalGroupTargets(group)
	if err != nil {
		return nil, nil, err
	}
	return selected, group, nil
}
