// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Native/task evidence only. The caller must revalidate source and managed
// authority and atomically publish via the shared group receipt contract.
func (c *client) verifiedFabricEWLCReceipt(ctx context.Context, want fabricEWLCIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	return verifyFabricEWLCReceipt(ctx, want.settings, op, now, c.configurationTaskTreeComplete, func(ctx context.Context) (*fabricEWLCFabricDependencies, error) {
		return c.fabricEWLCFabricDependencies(ctx, want)
	})
}

func verifyFabricEWLCReceipt(ctx context.Context, want fabricEWLCSettings, op *cvk.NetworkControllerConfigurationOperation, now time.Time, task func(context.Context, string, time.Time, time.Time) (bool, error), collect func(context.Context) (*fabricEWLCFabricDependencies, error)) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if op == nil || op.Section != "inventory" || op.Phase != "Verifying" || op.Action != "Update" || op.CancellationRequestedAt != nil || op.CreatedResourceID != "" || op.TargetSerial != "" || len(op.CredentialVersions) != 0 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || task == nil || collect == nil || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	_, resultDigest, err := parseFabricEWLCResource(op.Resource)
	if err != nil {
		return nil, err
	}
	// A false setting does not attest the separate cleanup reload.
	if !want.enabled {
		return nil, errConfigurationBusy
	}
	done, err := task(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	var baseline *fabricEWLCFabricDependencies
	for read := 0; read < 2; read++ {
		deps, err := collect(ctx)
		if err != nil {
			return nil, err
		}
		if deps == nil || deps.foundation == nil || deps.scope == nil || deps.locations == nil || deps.digest == "" {
			return nil, errInvalidResponse
		}
		payload, err := fabricEWLCPayload(deps.target.ID, want)
		if err != nil {
			return nil, err
		}
		if op.TargetID != deps.target.ID || op.ParentID != deps.scope.fabricID || resultDigest != fabricEWLCResultDigest(deps, payload) {
			return nil, errConfigurationStale
		}
		if deps.observed == nil || *deps.observed != payload {
			return nil, errConfigurationBusy
		}
		physical := map[string]string{}
		for _, device := range deps.foundation.physical {
			if physical[device.ID] != "" {
				return nil, errConfigurationStale
			}
			physical[device.ID] = device.Serial
		}
		if len(physical) != len(op.PhysicalGroup.Targets) {
			return nil, errConfigurationStale
		}
		for _, target := range op.PhysicalGroup.Targets {
			if physical[target.InventoryID] != target.PhysicalIdentity {
				return nil, errConfigurationStale
			}
		}
		if baseline != nil && (deps.digest != baseline.digest || !reflect.DeepEqual(deps.foundation.physical, baseline.foundation.physical)) {
			return nil, errConfigurationStale
		}
		baseline = deps
	}
	raw, _ := json.Marshal([]string{op.ParentID, op.TargetID})
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprintf("fabric-ewlc:%x", sha256.Sum256(raw)), Operation: *verified, VerifiedAt: &at}, nil
}
