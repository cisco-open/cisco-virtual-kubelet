// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func declaredPendingFabricEvent(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricSiteIntent) error {
	if config == nil {
		return errConfigurationTransition
	}
	var raw []byte
	if source != nil {
		raw = source.raw
	} else if config.Spec.Source.Inline != nil {
		raw = config.Spec.Source.Inline.Raw
	}
	sites, err := declaredPendingFabricEventSites(raw)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if site == want {
			return nil
		}
	}
	return errConfigurationStale
}

func declaredPendingFabricEventSites(raw []byte) ([]fabricSiteIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errConfigurationTransition
	}
	fabric, ok := root["fabric"].(map[string]any)
	if !ok || len(fabric) != 1 {
		return nil, errConfigurationTransition
	}
	model, _, events, err := parseFabricSitesWithPendingEvents(fabric["fabric_sites"])
	if err != nil {
		return nil, err
	}
	var selected []fabricSiteIntent
	for _, site := range model.sites {
		for _, event := range events {
			if event.path == site.path {
				selected = append(selected, site)
				break
			}
		}
	}
	return selected, nil
}

// Begin convergence verification only after owned native task completion and
// disappearance of the selected event. Neither fact releases the device group.
// The runtime caller must authorize its writer and installed group protocol.
func (a *adapter) beginPendingFabricEventVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricSiteIntent, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || authorize == nil {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		observed, err := a.observeOwnedPendingFabricEvent(ctx, config, source, want, op, now)
		if err != nil {
			return err
		}
		if observed == nil || !observed.taskComplete || observed.eventPending {
			return errConfigurationBusy
		}
		return nil
	}, now)
}

// Observe the exact retained group operation under the existing source/owner
// bracket. This never advances the operation or releases device reservations.
func (a *adapter) observeOwnedPendingFabricEvent(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricSiteIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*fabricPendingEventObservation, error) {
	if err := declaredPendingFabricEvent(config, source, want); err != nil {
		return nil, err
	}
	var result *fabricPendingEventObservation
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		snapshot, targets, err := a.client.pendingFabricEventTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return false, err
		}
		receipt, err := pendingFabricEventJournalReceipt(want, op, snapshot, targets, now)
		if err != nil {
			return false, err
		}
		result, err = a.client.observePendingFabricEvent(ctx, want, receipt, now)
		if err != nil {
			return false, err
		}
		fresh, freshTargets, err := a.client.pendingFabricEventTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(snapshot, fresh) || !reflect.DeepEqual(targets, freshTargets) {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errConfigurationBusy
	}
	return result, nil
}
