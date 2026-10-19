// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"reflect"
	"time"
)

// Readiness is not physical admission. Both known inventory identities are
// returned for later reservation binding; an absent spare is not assumed free.
// PnP-only replacements need their own authenticated identity/readiness path.
func bindOwnedReplacementReadiness(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, snapshot *deviceReplacementSnapshot, now time.Time) ([]Device, *cvk.NetworkControllerConfigurationActionApplication, error) {
	if state == nil || state.Operation != nil || snapshot == nil || snapshot.record == nil || snapshot.replacement == nil || snapshot.siteID == "" || snapshot.provisionID == "" {
		return nil, nil, errConfigurationBusy
	}
	scope, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		return nil, nil, err
	}
	receipt, err := ownedRMAMarkReceipt(state, config, scope, snapshot.target.ID, snapshot.serial, snapshot.siteID, now)
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil {
		return nil, nil, errConfigurationOwned
	}
	record := snapshot.record
	serial, err := topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
	if err != nil || serial != snapshot.serial || record.FaultyID != snapshot.target.ID || receipt.Resource != "rma-mark-record:"+record.ID || record.CreatedAt <= 0 || record.CreatedAt < receipt.Operation.SubmittedAt.Add(-configurationTaskClockSkew).UnixMilli() || record.CreatedAt > receipt.VerifiedAt.Add(configurationTaskClockSkew).UnixMilli() {
		return nil, nil, errConfigurationStale
	}
	if record.Status != "READY-FOR-REPLACEMENT" || record.WorkflowID != "" || record.WorkflowFailedStep != "" || record.ReplacedAt != 0 {
		return nil, nil, errConfigurationTransition
	}
	if record.ReplacementSerial != "" {
		replacement, err := topologyidentity.CanonicalPhysicalIdentity(record.ReplacementSerial)
		if err != nil || replacement != snapshot.replacementSerial {
			return nil, nil, errConfigurationOwned
		}
	}
	devices, err := bindMulticastRPDevices([]string{snapshot.target.ID, snapshot.replacement.ID}, []Device{snapshot.target, *snapshot.replacement})
	if err != nil || len(devices) != 2 {
		return nil, nil, errConfigurationDeviceAdmission
	}
	expected := map[string]string{snapshot.target.ID: snapshot.serial, snapshot.replacement.ID: snapshot.replacementSerial}
	for _, device := range devices {
		if expected[device.ID] != device.Serial {
			return nil, nil, errConfigurationDeviceAdmission
		}
	}
	return devices, receipt, nil
}

// Observe the owned pre-workflow state through existing authenticated readers.
// It neither starts a workflow nor treats a task success as replacement success.
func (a *adapter) observeOwnedReplacementReadiness(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent, now time.Time) ([]Device, error) {
	if a == nil || config == nil || source == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 || want.action != rmaReplace {
		return nil, errConfigurationTransition
	}
	inventory, _, actions, err := parseRMAInventory(source.raw)
	if err != nil {
		return nil, err
	}
	if actual, ok := actions[ref.Name]; !ok || !reflect.DeepEqual(actual, want) {
		return nil, errConfigurationStale
	}
	declared := false
	for _, device := range inventory.devices {
		if reflect.DeepEqual(device.reference, ref) {
			declared = true
		}
	}
	if !declared {
		return nil, errConfigurationStale
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
	devices, receipt, err := bindOwnedReplacementReadiness(nc.Status.Configuration, config, snapshot, now)
	if err != nil {
		return nil, err
	}
	for _, id := range []string{snapshot.record.NetworkReadinessTaskID, snapshot.record.ReadinessTaskID} {
		if id == "" {
			continue
		}
		done, err := a.client.configurationTaskComplete(ctx, id, receipt.Operation.SubmittedAt.Time, now)
		if err != nil {
			return nil, err
		}
		if !done {
			return nil, errConfigurationBusy
		}
	}
	if err := a.client.revalidateDeviceReplacement(ctx, ref, want, snapshot); err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return devices, nil
}
