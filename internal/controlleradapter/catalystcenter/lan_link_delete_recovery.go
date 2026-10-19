// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	"strings"
	"time"
)

// Recover the admitted declaration and its original ADD receipt. No current
// pool lookup is needed: that pool may legitimately disappear after DELETE.
func (a *adapter) retainedLANLinkDelete(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (lanLinkIntent, error) {
	var zero lanLinkIntent
	binding, err := validateLANLinkDeleteCompletionOperation(op, now)
	if err != nil {
		return zero, err
	}
	if err := requireLANLinkSectionOwner(nc, config); err != nil {
		return zero, err
	}
	wants, err := declaredLANLinks(source)
	if err != nil {
		return zero, err
	}
	var selected lanLinkIntent
	found := false
	for _, want := range wants {
		if want.action != "DELETE_LINK" {
			continue
		}
		if exactLANLinkDeleteDeclaration(want, binding) {
			if found {
				return zero, errConfigurationStale
			}
			selected, found = want, true
		}
	}
	if !found {
		return zero, errConfigurationStale
	}
	previous, err := latestLANLinkEffect(nc, config, binding.logical, now)
	if err != nil {
		return zero, err
	}
	if previous == nil || previous.deleted || lanLinkDigest(previous.receipt) != binding.owner {
		return zero, errConfigurationOwned
	}
	if _, err := validateLANLinkAddVerifiedReceipt(previous.receipt, now); err != nil {
		return zero, err
	}
	old := previous.receipt.Operation
	if old.SourceUID != string(source.uid) || old.ControllerGeneration != nc.Generation || old.TargetID != op.TargetID || old.ParentID != op.ParentID || !reflect.DeepEqual(old.PhysicalGroup.Targets, op.PhysicalGroup.Targets) {
		return zero, errConfigurationStale
	}
	if err := a.requireLANLinkPredecessorRetired(ctx, nc, previous.receipt); err != nil {
		return zero, err
	}
	return selected, nil
}

// Positive complete interface records are mandatory even if LLDP disappears.
// This is inventory evidence only; device CLI proof is still required before
// the atomic receipt writer can mark this operation Verified.
func (a *adapter) lanLinkDeleteCompletionTargets(ctx context.Context, config *configapi.NetworkControllerConfig, want lanLinkIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*lanLinkAdjacencySnapshot, []contract.GroupTargetIdentity, error) {
	binding, err := validateLANLinkDeleteCompletionOperation(op, now)
	if err != nil {
		return nil, nil, err
	}
	if want.action != "DELETE_LINK" || !observedLANLinkDeleteDeclaration(want, binding) {
		return nil, nil, errConfigurationStale
	}
	snapshot, targets, err := observeConfigurationManagedTargets(ctx, a.statusReader, config.Namespace, func(ctx context.Context) (configurationPhysicalObservation[*lanLinkAdjacencySnapshot], error) {
		var zero configurationPhysicalObservation[*lanLinkAdjacencySnapshot]
		sessions, err := a.client.lanActiveSessions(ctx)
		if err != nil {
			return zero, err
		}
		if sessions.Maximum == 0 || len(sessions.Active) != 0 {
			return zero, errConfigurationBusy
		}
		native, err := a.client.lanLinkAdjacencySnapshot(ctx, want)
		if err != nil {
			return zero, err
		}
		physical, err := lanLinkDeletePhysicalDigest(native.endpoints)
		if err != nil {
			return zero, err
		}
		if want.pool != "" {
			logical, err := lanLinkAddLogicalDigest(want, native.endpoints)
			if err != nil {
				return zero, err
			}
			if logical != binding.logical {
				return zero, errConfigurationOwned
			}
		}
		if physical != binding.physical || native.endpoints.primary.device.ID != op.TargetID || native.endpoints.secondary.device.ID != op.ParentID {
			return zero, errConfigurationStale
		}
		for _, endpoint := range []lanLinkEndpoint{native.endpoints.primary, native.endpoints.secondary} {
			if !strings.EqualFold(endpoint.device.Reachability, "reachable") || endpoint.port.IPv4 != "" || endpoint.port.Mask != "" {
				return zero, errConfigurationBusy
			}
		}
		if link := native.link; link != nil && (link.StartIP != "" || link.StartMask != "" || link.EndIP != "" || link.EndMask != "") {
			return zero, errConfigurationBusy
		}
		fresh, err := a.client.lanActiveSessions(ctx)
		if err != nil {
			return zero, err
		}
		if !reflect.DeepEqual(sessions, fresh) {
			return zero, errConfigurationStale
		}
		devices, err := bindMulticastRPDevices([]string{op.TargetID, op.ParentID}, []Device{native.endpoints.primary.device, native.endpoints.secondary.device})
		if err != nil {
			return zero, err
		}
		return configurationPhysicalObservation[*lanLinkAdjacencySnapshot]{state: native, physical: devices, digest: lanLinkDigest([]string{native.observationDigest(), lanLinkDigest(sessions)})}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
		return nil, nil, errConfigurationStale
	}
	return snapshot, targets, nil
}

func (a *adapter) beginLANLinkDeleteVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || !supportedLANLinkAddConfig(config) || authorize == nil {
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
	want, err := a.retainedLANLinkDelete(ctx, config, source, nc, op, now)
	if err != nil {
		return err
	}
	return a.beginLANLinkVerification(ctx, config, source, nc, op, members, authorize, now, func(ctx context.Context) error {
		_, _, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, op, now)
		return err
	})
}
