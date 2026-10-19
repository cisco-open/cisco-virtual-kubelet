// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sync/atomic"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The native verifier excludes the selected policy from its dependency digest
// because creation is an expected postcondition. Preparation must additionally
// pin that policy's complete baseline, including its absence.
func (c *client) extranetManagedTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricExtranetIntent) (*fabricExtranetSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricExtranetSnapshot], error) {
		state, err := c.extranetSnapshot(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*fabricExtranetSnapshot]{}, err
		}
		digest, err := wirelessTagDigest(map[string]any{"dependencies": state.digest, "policies": state.policies})
		if err != nil {
			return configurationPhysicalObservation[*fabricExtranetSnapshot]{}, err
		}
		return configurationPhysicalObservation[*fabricExtranetSnapshot]{state: state, physical: state.physical, digest: digest}, nil
	})
}

// Private dispatch-ready native plan. A denied or uncertain call consumes the
// closure; a new plan still requires an independently obtained group permit.
// The caller's guard runs before the native primitive's final reobservation.
func (c *client) planGuardedExtranetCreate(ctx context.Context, want fabricExtranetIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	write, err := c.planExtranetCreate(ctx, want)
	if err != nil || write == nil {
		return write, err
	}
	return guardExtranetWrite(write, guard), nil
}

func guardExtranetWrite(write *configurationWrite, guard func(context.Context) error) *configurationWrite {
	var used atomic.Bool
	copy := *write
	copy.submit = func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		return write.submit(ctx)
	}
	return &copy
}

// Reuse the common native/managed identity observer; no reservations, leases,
// Node patches or controller mutations are created. The returned write always
// refuses dispatch. Public Apply remains gated until the complete group
// preparation, dispatch and recovery lifecycle is connected.
func (c *client) planManagedExtranetCreate(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricExtranetIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	return c.planManagedExtranetMutation(ctx, reader, namespace, want, "Create", func(ctx context.Context) (*configurationWrite, error) {
		return c.planGuardedExtranetCreate(ctx, want, func(context.Context) error { return errConfigurationDeviceAdmission })
	})
}

// Expansion keeps the existing retained-ownership rules. Observation binds the
// union of old/new scope because the native planner accepts additive changes
// only; removals cannot enter this path. This still cannot dispatch or prepare
// an operation and requires the future group coordinator for public execution.
func (a *adapter) planManagedOwnedExtranetExpansion(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricExtranetIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	if a == nil || a.client == nil || config == nil {
		return nil, nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, nil, err
	}
	declared, err := declaredExtranetPolicy(source, want.policy.Name)
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(declared, want) {
		return nil, nil, errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	write, targets, err := a.client.planManagedExtranetMutation(ctx, a.statusReader, config.Namespace, want, "Update", func(ctx context.Context) (*configurationWrite, error) {
		write, err := a.planOwnedExtranetExpansion(ctx, config, source, want)
		if err != nil || write == nil {
			return write, err
		}
		return guardExtranetWrite(write, func(context.Context) error { return errConfigurationDeviceAdmission }), nil
	})
	if err != nil {
		return nil, nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}

func declaredExtranetPolicy(source *configSourceSnapshot, name string) (fabricExtranetIntent, error) {
	if source == nil {
		return fabricExtranetIntent{}, errConfigurationStale
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return fabricExtranetIntent{}, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return fabricExtranetIntent{}, errInvalidResponse
	}
	fabric, ok := root["fabric"].(map[string]any)
	if !ok {
		return fabricExtranetIntent{}, errInvalidResponse
	}
	intents, err := parseFabricExtranets(fabric["extranet_policies"])
	if err != nil {
		return fabricExtranetIntent{}, err
	}
	for _, want := range intents {
		if want.policy.Name == name {
			return want, nil
		}
	}
	return fabricExtranetIntent{}, errConfigurationStale
}

func (c *client) planManagedExtranetMutation(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricExtranetIntent, action string, plan func(context.Context) (*configurationWrite, error)) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	if plan == nil || action != "Create" && action != "Update" {
		return nil, nil, errConfigurationTransition
	}
	before, targets, err := c.extranetManagedTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	write, err := plan(ctx)
	if err != nil {
		return nil, nil, err
	}
	got, existed := before.policies[before.desired.Name]
	resource, target := before.resource(), ""
	if action == "Update" {
		if !existed {
			return nil, nil, errConfigurationStale
		}
		resource, target = before.updateResource(got.ID), got.ID
	}
	if write == nil && (!existed || !extranetPolicyMatches(before.desired, got)) || write != nil && (action == "Create" && existed || write.resource != resource || write.action != action || write.identity.target != target || write.identity.parent != "" || write.identity.serial != "") {
		return nil, nil, errConfigurationStale
	}
	after, fresh, err := c.extranetManagedTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if before.digest != after.digest || !reflect.DeepEqual(before.policies, after.policies) || !reflect.DeepEqual(targets, fresh) {
		return nil, nil, errConfigurationStale
	}
	if write != nil && action == "Update" {
		baseline, err := wirelessTagDigest(got)
		if err != nil {
			return nil, nil, err
		}
		copy := *write
		copy.resource = "extranet-update-v2:" + got.ID + ":" + baseline + ":" + before.digest
		write = &copy
	}
	return write, targets, nil
}
