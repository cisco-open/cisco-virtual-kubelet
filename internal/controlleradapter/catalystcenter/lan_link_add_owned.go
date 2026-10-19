// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Bind the positively observed result, excluding allocator usage, topology
// cache identifiers and discovery address lists. A historic receipt can suppress
// a repeated write; it cannot attest current independent device convergence.
func lanLinkAddResultDigest(snapshot *lanLinkCompletionSnapshot) (string, error) {
	if err := lanLinkAddNativeResult(snapshot); err != nil {
		return "", err
	}
	return lanLinkConfiguredOwnershipDigest(snapshot.dependencies)
}

// Reconstruct the configuration commitment of a valid ADD receipt. Its format
// commits mandatory successful ADD states as "up"; these constants do not claim
// that a currently observed DELETE target is operationally up.
func lanLinkConfiguredOwnershipDigest(deps *lanLinkDependencies) (string, error) {
	if deps == nil || deps.adjacency == nil || deps.adjacency.endpoints == nil {
		return "", errConfigurationTransition
	}
	recovery, err := lanLinkAddRecoveryDigest(deps)
	if err != nil {
		return "", err
	}
	endpoints := deps.adjacency.endpoints
	normalize := func(port lanLinkInterface) lanLinkInterface {
		port.AdminStatus = "up"
		port.Status = "up"
		return port
	}
	raw, err := json.Marshal(struct {
		Recovery           string
		Primary, Secondary lanLinkInterface
	}{recovery, normalize(endpoints.primary.port), normalize(endpoints.secondary.port)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// Only an exact, singly owned result may bypass new-ADD admission. Missing or
// altered evidence never falls through to adopting addressed physical ports.
func (a *adapter) observeOwnedLANLinkAdd(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, want lanLinkIntent, now time.Time) (lanLinkAddObservation, bool, error) {
	var zero lanLinkAddObservation
	if nc.Status.Configuration == nil || len(nc.Status.Configuration.ActionApplications) == 0 {
		return zero, false, nil
	}
	endpoints, err := a.client.lanLinkEndpointSnapshot(ctx, want)
	if err != nil {
		return zero, false, err
	}
	logical, err := lanLinkAddLogicalDigest(want, endpoints)
	if err != nil {
		return zero, false, err
	}
	key := "lan-link:" + logical
	effect, err := latestLANLinkEffect(nc, config, logical, now)
	if err != nil {
		return zero, true, err
	}
	if effect == nil {
		return zero, false, nil
	}
	if effect.deleted {
		if err := a.validateLANLinkReAdd(ctx, config, source, nc, want, effect, now); err != nil {
			return zero, true, err
		}
		return zero, false, nil
	}
	found := effect.receipt
	if _, err := validateLANLinkAddVerifiedReceipt(found, now); err != nil {
		return zero, true, err
	}
	op := found.Operation.DeepCopy()
	if op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) {
		return zero, true, errConfigurationOwned
	}
	owned := false
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == "lan_automation" {
			if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
				return zero, true, errConfigurationOwned
			}
			owned = true
		}
	}
	if !owned {
		return zero, true, errConfigurationOwned
	}
	op.Phase = "Verifying"
	native, targets, err := a.client.lanLinkCompletionTargets(ctx, a.statusReader, config.Namespace, want, op, now)
	if err != nil {
		return zero, true, err
	}
	result, err := lanLinkAddResultDigest(native)
	if err != nil {
		return zero, true, err
	}
	if found.Resource != key+":"+result {
		return zero, true, errConfigurationStale
	}
	return lanLinkAddObservation{resource: found.Resource, targets: targets, owned: true}, true, nil
}
