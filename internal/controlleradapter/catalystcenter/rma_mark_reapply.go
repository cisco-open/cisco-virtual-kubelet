// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"reflect"
	"time"
)

// A new explicit mark may supersede a completed cleanup only after original mark
// ownership and both reservations were retired. Require complete native absence:
// re-marking a retained NON-FAULTY record needs different appliance qualification.
// This retires bookkeeping only. The next reconciliation must freshly prepare
// and obtain admission for the new native mutation.
func (a *adapter) retireCleanupForNewMark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent, cleanupID string) error {
	if a == nil || config == nil || !supportedConfigurationApply(config) || source == nil || want.action != rmaMark || !configurationOperationID.MatchString(cleanupID) {
		return errConfigurationTransition
	}
	inventory, _, actions, err := parseRMAInventory(source.raw)
	if err != nil {
		return err
	}
	action, ok := actions[ref.Name]
	if !ok || !reflect.DeepEqual(action, want) {
		return errConfigurationStale
	}
	declared := false
	for _, device := range inventory.devices {
		if reflect.DeepEqual(device.reference, ref) {
			declared = true
		}
	}
	if !declared {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return errConfigurationBusy
	}
	snapshot, err := a.client.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return err
	}
	if snapshot.record != nil {
		return errConfigurationTransition
	}
	cleanup, err := deviceReplacementUnmarkApplied(nc.Status.Configuration, config, snapshot, time.Now())
	if err != nil {
		return err
	}
	if cleanup == nil || cleanup.Operation.ID != cleanupID {
		return errConfigurationStale
	}
	scope, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		return err
	}
	mark, err := ownedRMAMarkReceipt(nc.Status.Configuration, config, scope, snapshot.target.ID, snapshot.serial, snapshot.siteID, time.Now())
	if err != nil {
		return err
	}
	if mark != nil {
		return errConfigurationBusy
	}
	if err := a.requireRetiredRMAReservations(ctx, nc, cleanupID); err != nil {
		return err
	}
	if err := a.client.revalidateDeviceReplacement(ctx, ref, want, snapshot); err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return errConfigurationStale
	}
	if err := a.requireRetiredRMAReservations(ctx, nc, cleanupID); err != nil {
		return err
	}
	return a.persistRetiredRMAReceipt(ctx, nc, cleanup)
}
