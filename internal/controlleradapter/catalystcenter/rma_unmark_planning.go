// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Explicit prune of a mark on a still-declared device. Complete device removal
// and CR deletion require separate source/retirement handling and are not implied.
func rmaUnmarkPruneIntent(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (configurationDeviceReference, deviceReplacementIntent, error) {
	var zero configurationDeviceReference
	if config == nil || source == nil || name == "" || config.Spec.Mode != configapi.NetworkControllerApplyModeApply || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete || !config.DeletionTimestamp.IsZero() {
		return zero, deviceReplacementIntent{}, errConfigurationTransition
	}
	inventory, states, actions, err := parseRMAInventoryProjection(source.raw, false)
	if err != nil {
		return zero, deviceReplacementIntent{}, err
	}
	if _, present := actions[name]; present {
		return zero, deviceReplacementIntent{}, errConfigurationTransition
	}
	for _, device := range inventory.devices {
		if device.reference.Name != name {
			continue
		}
		if states[name] != "PROVISION" || device.reference.Serial == "" || device.wireless != nil {
			return zero, deviceReplacementIntent{}, errConfigurationTransition
		}
		return device.reference, deviceReplacementIntent{action: rmaMark, site: device.site}, nil
	}
	return zero, deviceReplacementIntent{}, errConfigurationTransition
}

// Owned mark-removal planner: policy, current source and the original verified
// native mark receipt must agree, or a verified cleanup must already converge.
func (a *adapter) planOwnedDeviceReplacementUnmark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (*configurationWrite, error) {
	ref, want, err := rmaUnmarkPruneIntent(config, source, name)
	if err != nil {
		return nil, err
	}
	return a.planOwnedDeviceReplacementAction(ctx, config, source, ref, want, func(ctx context.Context, state *cvk.NetworkControllerConfigurationStatus, snapshot *deviceReplacementSnapshot) (*configurationWrite, error) {
		cleanup, err := deviceReplacementUnmarkApplied(state, config, snapshot, time.Now())
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			return nil, nil
		}
		applied, err := deviceReplacementMarkApplied(state, config, snapshot, time.Now())
		if err != nil {
			return nil, err
		}
		if !applied {
			return nil, nil
		}
		return a.planOwnedNativeDeviceReplacementUnmark(ctx, config, source, name)
	})
}

// Revalidate ownership for both idle planning and exact Prepared recovery. The
// enclosing executor supplies the journal bracket and physical admission.
func (a *adapter) planOwnedNativeDeviceReplacementUnmark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (*configurationWrite, error) {
	ref, want, err := rmaUnmarkPruneIntent(config, source, name)
	if err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.client.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return nil, err
	}
	owned, err := deviceReplacementMarkApplied(nc.Status.Configuration, config, snapshot, time.Now())
	if err != nil {
		return nil, err
	}
	if !owned || snapshot.record == nil {
		return nil, errConfigurationOwned
	}
	recordID := snapshot.record.ID
	write, err := a.client.planDeviceReplacementUnmark(ctx, ref, want, recordID)
	if err != nil {
		return nil, err
	}
	guarded := *write
	guarded.submit = func(ctx context.Context) (Task, error) {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return Task{}, err
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return Task{}, err
		}
		observed, err := a.client.deviceReplacementSnapshot(ctx, ref, want)
		if err != nil {
			return Task{}, err
		}
		owned, err := deviceReplacementMarkApplied(fresh.Status.Configuration, config, observed, time.Now())
		if err != nil {
			return Task{}, err
		}
		if !owned || observed.record == nil || observed.record.ID != recordID {
			return Task{}, errConfigurationOwned
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return Task{}, err
		}
		latest, err := a.currentController(ctx)
		if err != nil {
			return Task{}, err
		}
		if latest.UID != fresh.UID || latest.ResourceVersion != fresh.ResourceVersion {
			return Task{}, errConfigurationStale
		}
		return write.submit(ctx)
	}
	return &guarded, nil
}
