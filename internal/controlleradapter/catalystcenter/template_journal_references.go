// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Known CVK references only. Application receipts are historical references, not
// proof that a native deployment is running or completed. Neither an empty set
// nor retirement of a journal slot proves appliance-wide deployment absence.
type templateJournalReference struct {
	Journal, OperationID, DeploymentID, Kind, BaseID, VersionID string
	Position                                                    int
	Version                                                     int64
}

func templateJournalReferences(state *cvk.NetworkControllerConfigurationStatus, aliases map[string]bool, now time.Time) ([]templateJournalReference, error) {
	if len(aliases) == 0 || now.IsZero() {
		return nil, errConfigurationTransition
	}
	for id, included := range aliases {
		if !included || !validAPIID(id) {
			return nil, errConfigurationTransition
		}
	}
	out := []templateJournalReference{}
	if state == nil {
		return out, nil
	}
	if len(state.ActionApplications) > 256 {
		return nil, errConfigurationTransition
	}
	seenOperations := map[string]*cvk.NetworkControllerConfigurationOperation{}
	seenTasks := map[string]string{}
	add := func(op *cvk.NetworkControllerConfigurationOperation, journal string) error {
		if op == nil || !isTemplateDeploymentResource(op.Resource) {
			// A template graph cannot disappear merely by editing the resource key.
			if op != nil && op.DependencyVersions != nil && op.DependencyVersions.Kind == "template-graph-v1" {
				return errConfigurationTransition
			}
			return nil
		}
		if op.Section != "inventory" || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || len(op.SSIDCredentialHistory) != 0 || (op.SourceUID == "") != (op.SourceVersion == "") {
			return errConfigurationTransition
		}
		if _, err := contract.OperationDigest(op); err != nil {
			return errConfigurationTransition
		}
		binding, err := templateReceiptBinding(op)
		if err != nil {
			return err
		}
		switch op.Phase {
		case "Prepared":
			if op.TaskID != "" || op.SubmittedAt != nil {
				return errConfigurationTransition
			}
		case "Submitting", "Unknown":
		case "Accepted", "Verifying", "Verified":
			if op.TaskID == "" || op.SubmittedAt == nil {
				return errConfigurationTransition
			}
		default:
			return errConfigurationTransition
		}
		if op.PreparedAt != nil && (op.PreparedAt.IsZero() || op.PreparedAt.After(now)) {
			return errConfigurationTransition
		}
		if op.SubmittedAt != nil && (op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || op.PreparedAt != nil && op.SubmittedAt.Before(op.PreparedAt)) {
			return errConfigurationTransition
		}
		deploymentID := ""
		if op.TaskID != "" {
			deploymentID, err = canonicalDeploymentID(op.TaskID)
			if err != nil || op.SubmittedAt == nil {
				return errConfigurationTransition
			}
			if previous, ok := seenTasks[deploymentID]; ok && previous != op.ID {
				return errConfigurationTransition
			}
			seenTasks[deploymentID] = op.ID
		}
		if previous, ok := seenOperations[op.ID]; ok && !reflect.DeepEqual(previous, op) {
			return errConfigurationTransition
		}
		seenOperations[op.ID] = op
		for position, version := range binding.templates {
			if aliases[version.baseID] || aliases[version.version.ID] {
				out = append(out, templateJournalReference{Journal: journal, OperationID: op.ID, DeploymentID: deploymentID, Kind: binding.kind, BaseID: version.baseID, VersionID: version.version.ID, Version: version.version.Number, Position: position})
			}
		}
		return nil
	}
	if err := add(state.Operation, "operation"); err != nil {
		return nil, err
	}
	seenReceipts := map[string]bool{}
	seenResources := map[string]bool{}
	for _, receipt := range state.ActionApplications {
		op := &receipt.Operation
		if !isTemplateDeploymentResource(receipt.Resource) && !isTemplateDeploymentResource(op.Resource) && (op.DependencyVersions == nil || op.DependencyVersions.Kind != "template-graph-v1") {
			continue
		}
		if receipt.Section != "inventory" || receipt.Section != op.Section || receipt.Resource != op.Resource || op.Phase != "Verified" || seenReceipts[op.ID] || seenResources[receipt.Resource] || receipt.VerifiedAt != nil && (receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.After(now) || op.SubmittedAt != nil && receipt.VerifiedAt.Before(op.SubmittedAt)) {
			return nil, errConfigurationTransition
		}
		seenReceipts[op.ID] = true
		seenResources[receipt.Resource] = true
		if err := add(op, "application"); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.OperationID != b.OperationID {
			return a.OperationID < b.OperationID
		}
		if a.Journal != b.Journal {
			return a.Journal < b.Journal
		}
		return a.Position < b.Position
	})
	return out, nil
}

// Read the existing durable controller journal independently of the current
// manifest/section owner: deleting a config must not erase retained references.
// This is deliberately scoped to this NetworkController, not all namespaces,
// endpoint aliases, UI/API users, scheduled jobs or appliance deployment history.
func (a *adapter) retainedTemplateDeploymentReferences(ctx context.Context, target templateSummary) ([]templateJournalReference, error) {
	if a == nil || a.client == nil {
		return nil, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	aliases, err := a.client.templateReferenceAliases(ctx, target)
	if err != nil {
		return nil, err
	}
	refs, err := templateJournalReferences(nc.Status.Configuration, aliases, time.Now())
	if err != nil {
		return nil, err
	}
	freshAliases, err := a.client.templateReferenceAliases(ctx, target)
	if err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.UID != fresh.UID || nc.ResourceVersion != fresh.ResourceVersion || !reflect.DeepEqual(nc.Status.Configuration, fresh.Status.Configuration) || !reflect.DeepEqual(aliases, freshAliases) {
		return nil, errConfigurationStale
	}
	return refs, nil
}
