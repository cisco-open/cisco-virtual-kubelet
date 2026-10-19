// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// A single whole-model observation contract surrounds every preparation and
// submission. No declaration is dropped because another action is selected.
func (a *adapter) observeLANLinkModel(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []lanLinkIntent) (map[string]lanLinkAddObservation, map[string]lanLinkIntent, error) {
	if !supportedLANLinkAddConfig(config) {
		return nil, nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	observed := map[string]lanLinkAddObservation{}
	declared := map[string]lanLinkIntent{}
	keys := lanLinkSourceKeys(wants)
	for _, want := range wants {
		observation, err := a.observeLANLinkWant(ctx, config, source, nc, want, time.Now())
		if err != nil {
			return nil, nil, err
		}
		observed[keys[want]], declared[keys[want]] = observation, want
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
	return observed, declared, nil
}

func (a *adapter) observeLANLinkWant(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, want lanLinkIntent, now time.Time) (lanLinkAddObservation, error) {
	var zero lanLinkAddObservation
	switch want.action {
	case "ADD_LINK":
		owned, found, err := a.observeOwnedLANLinkAdd(ctx, config, source, nc, want, now)
		if err != nil || found {
			return owned, err
		}
		snapshot, targets, err := a.client.lanLinkPreflightTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return zero, err
		}
		resource, err := lanLinkAddResource(want, snapshot)
		return lanLinkAddObservation{resource: resource, targets: targets}, err
	case "DELETE_LINK":
		owned, found, err := a.observeOwnedLANLinkDelete(ctx, config, source, nc, want, now)
		if err != nil || found {
			return owned, err
		}
		snapshot, targets, err := a.observeLANLinkDelete(ctx, config, source, want, now)
		if err != nil {
			return zero, err
		}
		return lanLinkAddObservation{resource: lanLinkDeleteResource(snapshot), targets: targets}, nil
	default:
		return zero, errConfigurationTransition
	}
}

func (a *adapter) lanLinkMixedLifecycle() lanLinkLifecycle {
	return lanLinkLifecycle{func(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (map[string]lanLinkAddObservation, map[string]lanLinkIntent, error) {
		wants, err := declaredLANLinks(source)
		if err != nil {
			return nil, nil, err
		}
		return a.observeLANLinkModel(ctx, config, source, wants)
	}, func(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanLinkIntent, guard func(context.Context) error) (*configurationWrite, error) {
		switch want.action {
		case "ADD_LINK":
			return a.lanLinkAddLifecycle().plan(ctx, config, source, want, guard)
		case "DELETE_LINK":
			return a.planManagedLANLinkDelete(ctx, config, source, want, guard)
		default:
			return nil, errConfigurationTransition
		}
	}, validLANLinkMutationResource}
}
