// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type lanLinkDeletePreflight struct {
	adjacency                             *lanLinkAdjacencySnapshot
	sessions                              *lanActiveSessionSnapshot
	previous                              *cvk.NetworkControllerConfigurationActionApplication
	logical, physical, declaration, scope string
	// Resolved pool is ownership evidence only. Native deletion keeps the exact
	// declared payload, including omission of the optional ipPoolName.
	pool string
}

func (s *lanLinkDeletePreflight) observationDigest() string {
	return lanLinkDigest(struct {
		Adjacency                                   string
		Sessions                                    *lanActiveSessionSnapshot
		Previous                                    *cvk.NetworkControllerConfigurationActionApplication
		Logical, Physical, Declaration, Scope, Pool string
	}{s.adjacency.observationDigest(), s.sessions, s.previous, s.logical, s.physical, s.declaration, s.scope, s.pool})
}

func requireLANLinkSectionOwner(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig) error {
	if nc == nil || nc.Status.Configuration == nil {
		return errConfigurationOwned
	}
	count := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == "lan_automation" {
			if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
				return errConfigurationOwned
			}
			count++
		}
	}
	if count != 1 {
		return errConfigurationOwned
	}
	return nil
}

func (a *adapter) requireLANLinkPredecessorRetired(ctx context.Context, nc *cvk.NetworkController, receipt *cvk.NetworkControllerConfigurationActionApplication) error {
	var members ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &members, ctrlclient.InNamespace(nc.Namespace)); err != nil {
		return err
	}
	for _, member := range members.Items {
		if member.Spec.Controller.UID == string(nc.UID) && member.Spec.OperationID == receipt.Operation.ID {
			return errConfigurationBusy
		}
	}
	return nil
}

func (a *adapter) observeLANLinkDelete(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanLinkIntent, now time.Time) (*lanLinkDeletePreflight, []contract.GroupTargetIdentity, error) {
	if want.action != "DELETE_LINK" || want.validate() != nil || !supportedLANLinkAddConfig(config) {
		return nil, nil, errConfigurationTransition
	}
	declarations, err := declaredLANLinks(source)
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, declared := range declarations {
		if declared == want {
			found = true
		}
	}
	if !found {
		return nil, nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := requireLANLinkSectionOwner(nc, config); err != nil {
		return nil, nil, err
	}
	collect := func(ctx context.Context) (configurationPhysicalObservation[*lanLinkDeletePreflight], error) {
		var zero configurationPhysicalObservation[*lanLinkDeletePreflight]
		sessions, err := a.client.lanActiveSessions(ctx)
		if err != nil {
			return zero, err
		}
		if sessions.Maximum == 0 || len(sessions.Active) != 0 {
			return zero, errConfigurationBusy
		}
		adjacency, err := a.client.lanLinkAdjacencySnapshot(ctx, want)
		if err != nil {
			return zero, err
		}
		for _, endpoint := range []lanLinkEndpoint{adjacency.endpoints.primary, adjacency.endpoints.secondary} {
			if !strings.EqualFold(endpoint.device.Reachability, "reachable") {
				return zero, errConfigurationBusy
			}
		}
		physical, err := lanLinkDeletePhysicalDigest(adjacency.endpoints)
		if err != nil {
			return zero, err
		}
		names := []string{want.pool}
		if want.pool == "" {
			inventory, err := a.client.loadAnycastPoolInventory(ctx)
			if err != nil {
				return zero, err
			}
			names = nil
			for name, pool := range inventory.byName {
				if pool.Type == "LAN" && pool.V4 != nil {
					names = append(names, name)
				}
			}
			sort.Strings(names)
		}
		var result *lanLinkDeletePreflight
		for _, name := range names {
			resolved := want
			resolved.pool = name
			logical, err := lanLinkAddLogicalDigest(resolved, adjacency.endpoints)
			if err != nil {
				return zero, err
			}
			latest, err := latestLANLinkEffect(nc, config, logical, now)
			if err != nil {
				return zero, err
			}
			if latest == nil || latest.deleted {
				continue
			}
			op := latest.receipt.Operation.DeepCopy()
			if op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) {
				return zero, errConfigurationOwned
			}
			binding, err := validateLANLinkAddVerifiedReceipt(latest.receipt, now)
			if err != nil {
				return zero, err
			}
			if op.TargetID != adjacency.endpoints.primary.device.ID || op.ParentID != adjacency.endpoints.secondary.device.ID {
				return zero, errConfigurationStale
			}
			deps, err := a.client.lanLinkDependencies(ctx, resolved)
			if err != nil {
				return zero, err
			}
			recovery, err := lanLinkAddRecoveryDigest(deps)
			if err != nil {
				return zero, err
			}
			if !reflect.DeepEqual(deps.adjacency, adjacency) {
				return zero, errConfigurationStale
			}
			if recovery != binding.recovery || lanLinkConfiguredIPv4(deps) != nil {
				if want.pool == "" {
					continue
				}
				return zero, errConfigurationStale
			}
			digest, err := lanLinkConfiguredOwnershipDigest(deps)
			if err != nil {
				return zero, err
			}
			if digest != latest.result {
				if want.pool == "" {
					continue
				}
				return zero, errConfigurationStale
			}

			if result != nil {
				return zero, errConfigurationOwned
			}
			if err := a.requireLANLinkPredecessorRetired(ctx, nc, latest.receipt); err != nil {
				return zero, err
			}
			result = &lanLinkDeletePreflight{adjacency: adjacency, sessions: sessions, previous: latest.receipt.DeepCopy(), logical: logical, physical: physical, declaration: lanLinkDeclarationDigest(want), scope: lanLinkDeclarationScope(want), pool: name}
		}
		if result == nil {
			return zero, errConfigurationOwned
		}
		if err := a.client.revalidateLANLinkAdjacency(ctx, want, adjacency); err != nil {
			return zero, err
		}
		current, err := a.client.lanActiveSessions(ctx)
		if err != nil {
			return zero, err
		}
		if !reflect.DeepEqual(sessions, current) {
			return zero, errConfigurationStale
		}
		endpoints := adjacency.endpoints
		scope, err := bindMulticastRPDevices([]string{endpoints.primary.device.ID, endpoints.secondary.device.ID}, []Device{endpoints.primary.device, endpoints.secondary.device})
		if err != nil {
			return zero, err
		}
		return configurationPhysicalObservation[*lanLinkDeletePreflight]{state: result, physical: scope, digest: result.observationDigest()}, nil
	}
	result, targets, err := observeConfigurationManagedTargets(ctx, a.statusReader, config.Namespace, collect)
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(targets, result.previous.Operation.PhysicalGroup.Targets) {
		return nil, nil, errConfigurationStale
	}

	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, nil, err
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	if current.UID != nc.UID || current.ResourceVersion != nc.ResourceVersion {
		return nil, nil, errConfigurationStale
	}
	return result, targets, nil
}
