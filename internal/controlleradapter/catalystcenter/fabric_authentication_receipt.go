// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *client) verifiedFabricAuthenticationReceipt(ctx context.Context, want fabricAuthIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if op == nil || op.Section != "fabric" || op.Action != "Update" || op.Phase != "Verifying" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || op.CancellationRequestedAt != nil || op.CreatedResourceID != "" || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	parts, err := parseFabricAuthManagedResource(op.Resource)
	if err != nil || parts[0] != fabricAuthDigest([]string{want.site.path, want.profile.Name}) {
		return nil, errConfigurationTransition
	}
	done, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		if err == nil {
			err = errConfigurationBusy
		}
		return nil, err
	}
	var baseline *fabricAuthPhysical
	for range 2 {
		state, err := c.fabricAuthenticationPhysical(ctx, want)
		if err != nil {
			return nil, err
		}
		if state.state.profile.ID != op.TargetID || state.state.fabricID != op.ParentID || fabricAuthDigest([]string{state.state.siteID, state.state.fabricID, state.state.profile.ID}) != parts[1] || state.digest != parts[4] {
			return nil, errConfigurationStale
		}
		if !authProfilesEqual(want.profile, state.state.profile) {
			return nil, errConfigurationBusy
		}
		if fabricAuthProfileDigest(state.state.profile) != parts[3] {
			return nil, errConfigurationStale
		}
		if len(state.physical) != len(op.PhysicalGroup.Targets) {
			return nil, errConfigurationStale
		}
		physical := map[string]string{}
		for _, d := range state.physical {
			physical[d.ID] = d.Serial
		}
		for _, target := range op.PhysicalGroup.Targets {
			if physical[target.InventoryID] != target.PhysicalIdentity {
				return nil, errConfigurationStale
			}
		}
		if baseline != nil && (baseline.observation != state.observation || !reflect.DeepEqual(baseline.physical, state.physical)) {
			return nil, errConfigurationStale
		}
		baseline = state
	}
	// A second task-tree observation closes the native result window without
	// using the successful PUT response itself as proof of task completion.
	done, err = c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		if err == nil {
			err = errConfigurationBusy
		}
		return nil, err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: "fabric-auth:receipt:v2:" + parts[0] + ":" + parts[1], Operation: *verified, VerifiedAt: &at}, nil
}
