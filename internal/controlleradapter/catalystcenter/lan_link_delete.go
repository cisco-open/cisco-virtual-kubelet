// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func lanLinkDeleteResource(snapshot *lanLinkDeletePreflight) string {
	return lanLinkDeleteScopedPrefix + snapshot.logical + ":" + lanLinkDigest(snapshot.previous) + ":" + snapshot.physical + ":" + snapshot.declaration + ":" + snapshot.scope + ":" + snapshot.observationDigest()
}

// Typed private entry point retains its delete-only contract.
func (a *adapter) observeLANLinkDeleteModel(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (map[string]lanLinkAddObservation, map[string]lanLinkIntent, error) {
	wants, err := declaredLANLinks(source)
	if err != nil {
		return nil, nil, err
	}
	for _, want := range wants {
		if want.action != "DELETE_LINK" {
			return nil, nil, errConfigurationTransition
		}
	}
	return a.observeLANLinkModel(ctx, config, source, wants)
}

func (a *adapter) lanLinkDeleteLifecycle() lanLinkLifecycle {
	return lanLinkLifecycle{a.observeLANLinkDeleteModel, a.planManagedLANLinkDelete, func(resource, action string) bool {
		_, err := parseLANLinkDeleteResource(resource, action)
		return err == nil
	}}
}

// Both public and private entry points use the shared all-member lifecycle.
func (a *adapter) prepareLANLinkDelete(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (*cvk.NetworkControllerConfigurationOperation, error) {
	return a.prepareLANLinkMutation(ctx, config, source, name, a.lanLinkDeleteLifecycle())
}
func (a *adapter) dispatchLANLinkDelete(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, permit *contract.GroupSubmissionPermit) (Task, error) {
	return a.dispatchLANLinkMutation(ctx, config, source, name, permit, a.lanLinkDeleteLifecycle())
}

// Public composition parses the entire source before selecting an effect. The
// parser refuses shared ports, duplicate names and unsupported discovery fields.
func (a *adapter) publicLANLinkLifecycle(source *configSourceSnapshot) (lanLinkLifecycle, error) {
	if _, err := declaredLANLinks(source); err != nil {
		return lanLinkLifecycle{}, err
	}
	return a.lanLinkMixedLifecycle(), nil
}
