// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Observation capability only. It is deliberately distinct from deletion-bound
// preparation/dispatch and is never accepted by the native submission gate.
type discoveryConfigDrainContext struct {
	ConfigUID, SpecDigest, DeletionDigest, OperationID string
	OperationGeneration                                int64
}

func discoveryDrainSnapshotValid(c, fresh *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if source == nil || source.discoveryDrain == nil || source.discoveryCleanup != nil || source.deletion != nil || !discoveryConfigCleanupShape(c) || !discoveryConfigCleanupShape(fresh) {
		return false
	}
	d := source.discoveryDrain
	return d.ConfigUID == string(c.UID) && fresh.UID == c.UID && fresh.Generation == c.Generation && fresh.ResourceVersion == c.ResourceVersion && reflect.DeepEqual(c.Spec, fresh.Spec) && reflect.DeepEqual(c.DeletionTimestamp, fresh.DeletionTimestamp) && d.SpecDigest == featureIdentityDigest(fresh.Spec) && d.DeletionDigest == featureIdentityDigest(fresh.DeletionTimestamp) && configurationOperationID.MatchString(d.OperationID)
}

func discoveryOperationGenerationMatches(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || c == nil {
		return false
	}
	if source == nil || source.discoveryDrain == nil {
		return op.ConfigGeneration == c.Generation
	}
	if !discoveryDrainSnapshotValid(c, c, source) {
		return false
	}
	d := source.discoveryDrain
	return d.OperationID == op.ID && d.OperationGeneration == op.ConfigGeneration && op.ConfigGeneration > 0 && (op.ConfigGeneration == c.Generation || op.ConfigGeneration+1 == c.Generation) && strings.HasPrefix(op.Resource, ownedDiscoveryPrefix) && ownedDiscoveryOperation(op)
}

// Drain only the exact pre-deletion owned inventory operation. Sources remain
// required, and generation drift is bounded to the deletion transition. This
// bridge does not adopt jobs or fabricate missing acknowledgements.
func (a *adapter) drainDiscoveryConfigOperation(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if c == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if !ownedDiscoveryOperation(op) || !strings.HasPrefix(op.Resource, ownedDiscoveryPrefix) || op.Section != "inventory" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || op.ConfigGeneration < 1 || (op.ConfigGeneration != c.Generation && op.ConfigGeneration+1 != c.Generation) || op.ControllerGeneration != nc.Generation || op.PreparedAt != nil {
		return errConfigurationStale
	}
	mask, _, valid := ownedDiscoveryFingerprintParts(op.Resource)
	if !valid || mask == 0 {
		return errConfigurationOwned
	}
	nc, source, err := a.discoveryCleanupInputForOperation(ctx, c, op)
	if err != nil {
		return err
	}
	if op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	source.discoveryDrain = &discoveryConfigDrainContext{ConfigUID: string(c.UID), SpecDigest: featureIdentityDigest(c.Spec), DeletionDigest: featureIdentityDigest(c.DeletionTimestamp), OperationID: op.ID, OperationGeneration: op.ConfigGeneration}
	if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
		return err
	}
	if op.Phase == "Prepared" {
		if op.TaskID != "" || op.SubmittedAt != nil {
			return errConfigurationTransition
		}
		return a.finishConfiguration(ctx, op.ID)
	}
	// API timestamps have second precision. Permit only the second containing
	// the deletion request; later submissions cannot enter the observation bridge.
	if op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || !op.SubmittedAt.Time.Before(c.DeletionTimestamp.Add(time.Second)) {
		return errConfigurationTransition
	}
	intent, err := parseInventoryIntent(source.raw)
	if err != nil {
		return err
	}
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client, desired: intent.discoveries}, owner: string(c.UID), receipts: nc.Status.Configuration.DeepCopy().ActionApplications, prune: true, retainedObservation: source.discoveryScope != nil}
	plan := configurationApplyPlan{section: "inventory", executor: e}
	a.bindOwnedDiscoveryExecutor(nc, c, source, &plan, e, "")
	switch op.Phase {
	case "Accepted", "Unknown":
		if !validAPIID(op.TaskID) || op.Phase == "Unknown" && !plan.canObserveUnknownTask(op) {
			return errConfigurationTransition
		}
		done, err := a.observeDiscoveryDeletionTask(ctx, c, op)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		return a.advanceConfiguration(ctx, op.ID, "Verifying", "")
	case "Verifying":
		return plan.recordCompletion(ctx, op)
	case "Verified":
		if !ownedDiscoveryReceiptPresent(nc.Status.Configuration, op, discoveryRetiredReceiptKeys(nc, op.ConfigUID)) {
			return errConfigurationOwned
		}
		return a.finishConfiguration(ctx, op.ID)
	default:
		return errConfigurationTransition
	}
}
