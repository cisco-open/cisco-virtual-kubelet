// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"

// A parent UUID may change only through a retained, verified parent lifecycle.
// This structural validator uses Current readers and supplied archive cohorts
// to avoid recursion through the whole-history validator. Native authentication
// of a current replacement's keyed baseline remains the executor's job.
func siteRecreationParentProof(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, child siteHistoryCohort, archives map[string]siteHistoryCohort, currentOnly bool) (*siteCreatorRecord, error) {
	if state == nil || op == nil {
		return nil, errConfigurationOwned
	}
	parts, ok := siteRecreationParts(op.Resource)
	if !ok {
		return nil, errConfigurationOwned
	}
	if len(parts) == 6 {
		if op.ParentID != child.Retired.Operation.ParentID {
			return nil, errConfigurationOwned
		}
		return nil, nil
	}
	if op.ParentID == child.Retired.Operation.ParentID {
		return nil, errConfigurationOwned
	}
	current, err := siteCreatedRecordsCurrent(state)
	if err != nil {
		return nil, err
	}
	candidates := []siteCreatorRecord{}
	for _, record := range current {
		candidates = append(candidates, record)
	}
	if !currentOnly {
		for _, cohort := range archives {
			local := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{cohort.Creator}}
			rows, err := siteCreatedRecordsCurrent(local)
			if err != nil {
				return nil, err
			}
			for _, record := range rows {
				candidates = append(candidates, record)
			}
		}
	}
	var parent *siteCreatorRecord
	for _, record := range candidates {
		if siteReceiptDigest(record.receipt) == parts[6] {
			if parent != nil {
				return nil, errConfigurationOwned
			}
			copy := record
			parent = &copy
		}
	}
	if parent == nil || parent.id != op.ParentID || parent.keyID != parts[3] || !validSiteParent(child.Kind, parent.kind) || parent.receipt.Operation.ConfigName != op.ConfigName || parent.receipt.Operation.ConfigUID != op.ConfigUID || parent.receipt.Operation.ConfigGeneration > op.ConfigGeneration || parent.receipt.Operation.ControllerGeneration > op.ControllerGeneration || op.ID == parent.receipt.Operation.ID || op.TaskID != "" && op.TaskID == parent.receipt.Operation.TaskID || op.SubmittedAt != nil && op.SubmittedAt.Before(parent.receipt.VerifiedAt) {
		return nil, errConfigurationOwned
	}
	predecessor, digest, ok := siteRecreationParent(parent.receipt.Operation.Resource)
	old, found := archives[predecessor]
	if !ok || !found || digest != old.Digest || old.ID != child.Retired.Operation.ParentID || old.ID == parent.id || old.Kind != parent.kind || old.Path != siteResourcePathDigest(parent.receipt.Operation.Resource) || old.Retired.Operation.ConfigName != op.ConfigName || old.Retired.Operation.ConfigUID != op.ConfigUID || old.Retired.Operation.SubmittedAt == nil || old.Retired.Operation.SubmittedAt.Before(child.Retired.VerifiedAt) || parent.receipt.Operation.SubmittedAt == nil || parent.receipt.Operation.SubmittedAt.Before(old.Retired.VerifiedAt) {
		return nil, errConfigurationOwned
	}
	if currentOnly {
		retired, err := siteRetiredRecordsCurrent(state)
		if err != nil {
			return nil, err
		}
		for _, row := range retired {
			if row.Operation.TargetID == parent.id {
				return nil, errConfigurationOwned
			}
		}
	}
	return parent, nil
}
