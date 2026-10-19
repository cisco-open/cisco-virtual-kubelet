// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Build completion evidence for the existing group journal. This grants no
// mutation authority and performs no Kubernetes write. The caller must persist
// the receipt atomically using PersistVerifiedGroupReceipt, which checks the
// current owner, journal and reservations before advancing the operation.
func (c *client) verifiedFabricL3IPHandoffReceipt(ctx context.Context, want fabricL3IPHandoffDeploymentIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if op == nil || op.Section != "fabric" || op.Phase != "Verifying" || op.Action != "Create" || op.CancellationRequestedAt != nil || op.CreatedResourceID != "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	done, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	id, err := c.verifyFabricL3IPHandoffCreate(ctx, want.handoff, want.device, op)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errConfigurationBusy
	}
	deps, err := c.fabricL3IPHandoffDependencies(ctx, want.handoff, want.device)
	if err != nil {
		return nil, err
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
	// A replacement with equal configuration is not the object observed above.
	freshID, err := c.verifyFabricL3IPHandoffCreate(ctx, want.handoff, want.device, op)
	if err != nil {
		return nil, err
	}
	if freshID != id {
		return nil, errConfigurationStale
	}
	// Separate the stable logical identity and observed native UUID from the
	// immutable prepared operation; never rewrite its group digest after create.
	raw, err := json.Marshal([]any{want.device.fabricSite, op.ParentID, op.TargetID, want.handoff.port, want.handoff.transit, want.handoff.network, want.handoff.vlan})
	if err != nil {
		return nil, errInvalidResponse
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: fmt.Sprintf("fabric-l3-ip-handoff:%x:%s", sha256.Sum256(raw), id), Operation: *verified, VerifiedAt: &at}, nil
}
