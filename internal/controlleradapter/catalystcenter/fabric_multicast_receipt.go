// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"time"
)

// Verify owned task completion and stable native identity without rewriting the
// immutable group operation. The native UUID belongs in the application key.
func (c *client) verifiedFabricMulticastVNReceipt(ctx context.Context, path string, wants []fabricMulticastVNIntent, name string, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if op == nil || op.Section != "fabric" || op.Phase != "Verifying" || (op.Action != "Create" && op.Action != "Update") || op.CancellationRequestedAt != nil || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	done, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	var baseline *fabricMulticastSnapshot
	id := ""
	for read := 0; read < 2; read++ {
		state, err := c.multicastSnapshot(ctx, path, wants)
		if err != nil {
			return nil, err
		}
		converged, err := verifyFabricMulticastVNSnapshot(state, path, wants, name, op)
		if err != nil {
			return nil, err
		}
		if !converged {
			return nil, errConfigurationBusy
		}
		observed, exists := state.before[name]
		if !exists || !validAPIID(observed.ID) {
			return nil, errConfigurationBusy
		}
		physical := map[string]string{}
		for _, device := range state.devices {
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
		if baseline != nil && (id != observed.ID || baseline.digest != state.digest || !reflect.DeepEqual(baseline.devices, state.devices)) {
			return nil, errConfigurationStale
		}
		baseline, id = state, observed.ID
	}
	raw, _ := json.Marshal([]string{path, op.ParentID, name})
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: fmt.Sprintf("fabric-multicast-vn-application:%x:%s", sha256.Sum256(raw), id), Operation: *verified, VerifiedAt: &at}, nil
}
