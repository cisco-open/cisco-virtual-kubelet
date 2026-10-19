// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"strings"
	"time"
)

// A retained cleanup is an imperative completion receipt, not ownership of any
// later replacement record. It suppresses repeat cleanup only while its original
// physical scope and native NON-FAULTY/absent result still agree.
func deviceReplacementUnmarkApplied(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, snapshot *deviceReplacementSnapshot, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	if state == nil || config == nil || snapshot == nil {
		return nil, errConfigurationTransition
	}
	scope, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		return nil, err
	}
	var found *cvk.NetworkControllerConfigurationActionApplication
	for _, entry := range state.ActionApplications {
		op := entry.Operation
		if !strings.HasPrefix(entry.Resource, "rma-unmark:") && !strings.HasPrefix(op.Resource, "rma-unmark:") {
			continue
		}
		binding, err := parseRMAUnmarkResource(op.Resource)
		if err != nil || entry.Resource != op.Resource || entry.Section != "inventory" || op.Section != "inventory" {
			return nil, errConfigurationStale
		}
		if op.TargetID != snapshot.target.ID && op.TargetSerial != snapshot.serial {
			continue
		}
		if found != nil {
			return nil, errConfigurationOwned
		}
		if binding.scope != scope || op.TargetID != snapshot.target.ID || op.TargetSerial != snapshot.serial || op.ParentID != snapshot.siteID || op.Action != "Delete" || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || entry.VerifiedAt == nil || entry.VerifiedAt.IsZero() || entry.VerifiedAt.Before(op.SubmittedAt) || entry.VerifiedAt.After(now) || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 {
			return nil, errConfigurationStale
		}
		mark, err := ownedRMAMarkReceipt(state, config, scope, op.TargetID, op.TargetSerial, op.ParentID, now)
		if err != nil {
			return nil, err
		}
		if mark != nil {
			if err := ownedRMAUnmarkOrigin(state, config, &op, now); err != nil {
				return nil, err
			}
		}
		if record := snapshot.record; record != nil {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
			if err != nil || serial != op.TargetSerial || record.ID != binding.recordID || record.CreatedAt != binding.createdAt || record.Status != "NON-FAULTY" || record.WorkflowID != "" || record.WorkflowFailedStep != "" || record.ReplacedAt != 0 || record.ReplacementSerial != "" {
				return nil, errConfigurationStale
			}
		}
		found = entry.DeepCopy()
	}
	return found, nil
}
