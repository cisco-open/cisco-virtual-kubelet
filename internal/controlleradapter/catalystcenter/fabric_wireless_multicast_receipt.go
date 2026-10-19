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

// Build completion evidence for the existing group journal. This grants no
// mutation authority and performs no Kubernetes write. The caller must persist
// the receipt atomically using PersistVerifiedGroupReceipt, which checks the
// current owner, journal and reservations before advancing the operation.
func (c *client) verifiedFabricWirelessMulticastReceipt(ctx context.Context, path string, enabled bool, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if op == nil || op.Section != "fabric" || op.Phase != "Verifying" || op.Action != "Update" || op.CancellationRequestedAt != nil || op.CreatedResourceID != "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	done, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	converged, err := c.verifyFabricWirelessMulticast(ctx, path, enabled, op)
	if err != nil {
		return nil, err
	}
	if !converged {
		return nil, errConfigurationBusy
	}
	deps, err := c.wirelessMulticastPhysical(ctx, path)
	if err != nil {
		return nil, err
	}
	if deps.dependencies.state.enabled != enabled {
		return nil, errConfigurationBusy
	}
	if fabricWirelessMulticastResource(path, enabled, deps.dependencies) != op.Resource {
		return nil, errConfigurationStale
	}
	if len(deps.physical) != len(op.PhysicalGroup.Targets) {
		return nil, errConfigurationStale
	}
	physical := make(map[string]string, len(deps.physical))
	for _, device := range deps.physical {
		physical[device.ID] = device.Serial
	}
	for _, target := range op.PhysicalGroup.Targets {
		if physical[target.InventoryID] != target.PhysicalIdentity {
			return nil, errConfigurationStale
		}
	}
	converged, err = c.verifyFabricWirelessMulticast(ctx, path, enabled, op)
	if err != nil {
		return nil, err
	}
	if !converged {
		return nil, errConfigurationBusy
	}
	fresh, err := c.wirelessMulticastPhysical(ctx, path)
	if err != nil {
		return nil, err
	}
	if fresh.digest != deps.digest || !reflect.DeepEqual(fresh.physical, deps.physical) {
		return nil, errConfigurationStale
	}
	raw, err := json.Marshal([]any{path, op.ParentID, op.TargetID})
	if err != nil {
		return nil, errInvalidResponse
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: fmt.Sprintf("fabric-wireless-multicast:%x", sha256.Sum256(raw)), Operation: *verified, VerifiedAt: &at}, nil
}
