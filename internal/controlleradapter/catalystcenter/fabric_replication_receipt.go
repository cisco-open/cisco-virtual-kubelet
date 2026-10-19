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
	"strings"
	"time"
)

// The task and complete retained fabric dependencies must converge twice. Only
// the intended replication mode is excluded from result identity; this function
// grants no authority and never releases reservations or writes Kubernetes.
func (c *client) verifiedFabricReplicationReceipt(ctx context.Context, path, mode string, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if !validFabricReplicationMode(mode) || op == nil || op.Section != "fabric" || op.Action != "Update" || op.Phase != "Verifying" || op.CancellationRequestedAt != nil || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || now.IsZero() || op.SubmittedAt.After(now) || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	done, err := c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	var baseline *fabricReplicationDependencies
	for read := 0; read < 2; read++ {
		deps, err := c.replicationDependencies(ctx, path)
		if err != nil {
			return nil, err
		}
		if deps.state.siteID != op.ParentID || deps.state.fabricID != op.TargetID || strings.Join(strings.Split(op.Resource, ":")[:2], ":") != fabricReplicationResultResource(path, mode, deps) {
			return nil, errConfigurationStale
		}
		if !deps.state.exists || deps.state.mode != mode {
			return nil, errConfigurationBusy
		}
		physical := map[string]string{}
		for _, device := range deps.physical {
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
		if baseline != nil && (deps.digest != baseline.digest || !reflect.DeepEqual(deps.physical, baseline.physical)) {
			return nil, errConfigurationStale
		}
		baseline = deps
	}
	raw, _ := json.Marshal([]string{path, op.ParentID, op.TargetID})
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(now.UTC())
	return &cvk.NetworkControllerConfigurationActionApplication{Section: "fabric", Resource: fmt.Sprintf("fabric-replication:%x", sha256.Sum256(raw)), Operation: *verified, VerifiedAt: &at}, nil
}
